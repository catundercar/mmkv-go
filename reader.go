// Package mmkv is a cgo-free, read-only decoder for MMKV files. It parses the
// on-disk format directly in Go so the read path never crosses the cgo boundary.
// Writes are out of scope — keep using the official cgo library for those.
// See doc/DESIGN.md for the format spec and boundaries.
//
// Scope: plaintext or AES-encrypted (WithEncryption), optional key expiration,
// read-only, single-writer/multi-reader.
//
// Freshness, like MMKV C++: every read is transparently up to date. The reader
// mmaps ".crc" and, on each read, cheaply compares the writer's change canary
// (crcDigest, actualSize, sequence); when it changed it reloads the snapshot
// under a shared flock — interlocking with an MMKV writer opened
// MMKV_MULTI_PROCESS. No manual refresh, near-zero cost when nothing changed.
// POSIX-only (matches MMKV's flock + mmap).
package mmkv

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"math"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"
)

var (
	// ErrUnsupportedVersion guards against silent breakage on a future MMKV
	// on-disk format. Fall back to the cgo library when you see this.
	ErrUnsupportedVersion = errors.New("mmkv: unsupported MMKV file version")
	// ErrCRCMismatch means the data region failed its CRC32 check — corrupt,
	// torn write, or (commonly) an encrypted file read without (the right) key.
	ErrCRCMismatch = errors.New("mmkv: CRC mismatch")
)

// snapshot is an immutable parsed view of the file. Readers load it atomically;
// reloads build a new one and swap the pointer, so concurrent readers always see
// a consistent snapshot and never race with a refresh.
type snapshot struct {
	meta    *metaInfo
	backing []byte            // owns the value slices below
	m       map[string][]byte // key -> value slot blob (views into backing)
	expire  bool              // values carry a trailing 4-byte expire timestamp
}

// Reader is a cgo-free read-only view of one MMKV instance.
type Reader struct {
	rootDir string
	mmapID  string
	dec     Decryptor

	snap atomic.Pointer[snapshot]

	crcFile  *os.File // kept open for mmap + flock
	crcMmap  []byte   // read-only mmap of .crc (change probed on each read)
	reloadMu sync.Mutex
	// change canary of the currently published snapshot. MMKV bumps crcDigest +
	// actualSize on normal writes and sequence only on full write-back, so we
	// must compare all three (mirrors MMKV's checkLoadData).
	loadedCRC    atomic.Uint32
	loadedActual atomic.Uint32
	loadedSeq    atomic.Uint32
	lastErr      atomic.Pointer[error]
}

// Option configures a Reader.
type Option func(*Reader)

// Open loads <rootDir>/<encodeFilePath(mmapID)> (+ ".crc") and keeps the reader
// transparently fresh (check-on-read; see package doc). POSIX-only.
func Open(rootDir, mmapID string, opts ...Option) (*Reader, error) {
	r := &Reader{rootDir: rootDir, mmapID: mmapID}
	for _, o := range opts {
		o(r)
	}
	if err := r.openLive(); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *Reader) openLive() error {
	f, err := os.Open(r.metaPath())
	if err != nil {
		return fmt.Errorf("mmkv: open meta: %w", err)
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	mm, err := mmapReadonly(f, int(info.Size()))
	if err != nil {
		f.Close()
		return fmt.Errorf("mmkv: mmap meta: %w", err)
	}
	r.crcFile = f
	r.crcMmap = mm
	// initial load under shared lock.
	r.reloadMu.Lock()
	defer r.reloadMu.Unlock()
	return r.reloadLocked()
}

// reloadFrom builds and publishes a snapshot from already-read meta bytes; it
// reads & parses the data file itself.
func (r *Reader) reloadFrom(metaBuf []byte) error {
	snap, err := r.buildSnapshot(metaBuf)
	if err != nil {
		return err
	}
	r.snap.Store(snap)
	r.loadedCRC.Store(snap.meta.crcDigest)
	r.loadedActual.Store(snap.meta.actualSize)
	r.loadedSeq.Store(snap.meta.sequence)
	return nil
}

// metaTuple reads the change canary (crc, actualSize, sequence) from the mmap'd
// .crc — a cheap lockless probe. A transient mismatch (writer mid-update) just
// triggers a reload under the flock, where the state is consistent.
func (r *Reader) metaTuple() (crc, actual, seq uint32) {
	crc = atomic.LoadUint32((*uint32)(unsafe.Pointer(&r.crcMmap[offCRC])))
	actual = atomic.LoadUint32((*uint32)(unsafe.Pointer(&r.crcMmap[offActualSize])))
	seq = atomic.LoadUint32((*uint32)(unsafe.Pointer(&r.crcMmap[offSequence])))
	return
}

func (r *Reader) tupleFresh(crc, actual, seq uint32) bool {
	return crc == r.loadedCRC.Load() && actual == r.loadedActual.Load() && seq == r.loadedSeq.Load()
}

// reloadLocked refreshes from the mmap'd meta while holding a shared flock on
// .crc (live mode). Caller holds reloadMu.
func (r *Reader) reloadLocked() error {
	if err := flockShared(r.crcFile); err != nil {
		return fmt.Errorf("mmkv: shared lock: %w", err)
	}
	defer flockUnlock(r.crcFile)
	return r.reloadFrom(r.crcMmap)
}

func (r *Reader) buildSnapshot(metaBuf []byte) (*snapshot, error) {
	meta, err := parseMeta(metaBuf)
	if err != nil {
		return nil, err
	}
	if meta.version > maxSupportedVersion {
		return nil, fmt.Errorf("%w: %d", ErrUnsupportedVersion, meta.version)
	}

	data, err := os.ReadFile(r.dataPath())
	if err != nil {
		return nil, fmt.Errorf("mmkv: read data: %w", err)
	}

	var actual uint32
	if meta.version >= versionActualSize {
		actual = meta.actualSize
	} else if len(data) >= 4 {
		actual = binary.LittleEndian.Uint32(data)
	}
	if int(actual)+4 > len(data) {
		return nil, fmt.Errorf("mmkv: actualSize %d + 4 > file size %d", actual, len(data))
	}
	region := data[4 : 4+actual]

	if crc32.ChecksumIEEE(region) != meta.crcDigest {
		return nil, ErrCRCMismatch
	}

	plain := region
	if r.dec != nil {
		if plain, err = r.dec.Decrypt(region, meta.iv[:]); err != nil {
			return nil, err
		}
	}
	m, err := parseDict(plain)
	if err != nil {
		return nil, err
	}
	return &snapshot{
		meta:    meta,
		backing: data,
		m:       m,
		expire:  meta.version >= versionFlag && meta.expireEnabled(),
	}, nil
}

// ensureFresh cheaply checks the mmap'd change canary and reloads if a writer
// advanced it. Best-effort: on reload error it keeps the last good snapshot and
// records the error (see Err()).
func (r *Reader) ensureFresh() {
	if r.tupleFresh(r.metaTuple()) {
		return
	}
	r.reloadMu.Lock()
	defer r.reloadMu.Unlock()
	if r.tupleFresh(r.metaTuple()) {
		return // another goroutine reloaded
	}
	if err := r.reloadLocked(); err != nil {
		r.lastErr.Store(&err)
	}
}

// Err returns the last error from a best-effort reload, if any. Reads keep
// serving the last good snapshot when a reload fails.
func (r *Reader) Err() error {
	if p := r.lastErr.Load(); p != nil {
		return *p
	}
	return nil
}

// Close releases buffers, the mmap and the fd. Value slices from
// GetBytes/GetString become invalid afterwards.
func (r *Reader) Close() error {
	r.snap.Store(nil)
	if r.crcMmap != nil {
		_ = munmap(r.crcMmap)
		r.crcMmap = nil
	}
	if r.crcFile != nil {
		err := r.crcFile.Close()
		r.crcFile = nil
		return err
	}
	return nil
}

// current returns the snapshot to read from, transparently refreshing first.
func (r *Reader) current() *snapshot {
	r.ensureFresh()
	return r.snap.Load()
}

func nowUnix() uint32 { return uint32(time.Now().Unix()) }

// value returns the value slot for key with the trailing expire timestamp
// stripped, or ok=false if the key is absent or expired. With expiration off it
// returns the raw slot. Expire: the last 4 bytes are a little-endian unix-seconds
// timestamp; 0 = never; expired when != 0 && <= now (matches MMKV).
func (s *snapshot) value(key string) ([]byte, bool) {
	v, ok := s.m[key]
	if !ok {
		return nil, false
	}
	if s.expire && len(v) >= 4 {
		t := binary.LittleEndian.Uint32(v[len(v)-4:])
		if t != 0 && t <= nowUnix() {
			return nil, false // expired
		}
		v = v[:len(v)-4]
	}
	return v, true
}

// Keys returns all live (non-expired) keys, sorted.
func (r *Reader) Keys() []string {
	s := r.current()
	keys := make([]string, 0, len(s.m))
	for k := range s.m {
		if _, ok := s.value(k); ok {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys
}

// Contains reports whether key exists and is not expired.
func (r *Reader) Contains(key string) bool {
	_, ok := r.current().value(key)
	return ok
}

// bytesValue strips the inner length-delimited layer MMKV wraps around
// string/[]byte values (the value slot is len-prefixed, and its content is a
// second len-prefixed field). Scalars are not wrapped this way.
func (s *snapshot) bytesValue(key string) ([]byte, bool) {
	v, ok := s.value(key)
	if !ok {
		return nil, false
	}
	b, err := newCodedInput(v).readBytes()
	if err != nil {
		return nil, false
	}
	return b, true
}

// GetBytes returns the value as a view into the reader's buffer (no copy); valid
// until the next reload/Close. Do not mutate it. Use GetBytesCopy for an
// independent slice (recommended in live mode).
func (r *Reader) GetBytes(key string) ([]byte, bool) {
	return r.current().bytesValue(key)
}

// GetBytesCopy returns an independent copy of the value.
func (r *Reader) GetBytesCopy(key string) ([]byte, bool) {
	v, ok := r.current().bytesValue(key)
	if !ok {
		return nil, false
	}
	out := make([]byte, len(v))
	copy(out, v)
	return out, true
}

// GetString returns the value as a zero-copy string view over the reader's
// buffer; valid until the next reload/Close, and the underlying bytes must not
// be mutated (e.g. via a GetBytes alias). The view aliases heap memory and is
// kept alive by the GC while held. Use GetStringCopy for an independent string
// (recommended for multi-goroutine live use).
func (r *Reader) GetString(key string) (string, bool) {
	v, ok := r.current().bytesValue(key)
	if !ok {
		return "", false
	}
	if len(v) == 0 {
		return "", true
	}
	return unsafe.String(unsafe.SliceData(v), len(v)), true
}

// GetStringCopy returns the value as an independent string (a copy).
func (r *Reader) GetStringCopy(key string) (string, bool) {
	v, ok := r.current().bytesValue(key)
	if !ok {
		return "", false
	}
	return string(v), true
}

func (r *Reader) GetBool(key string) (bool, bool) {
	v, ok := r.current().value(key)
	if !ok {
		return false, false
	}
	b, err := newCodedInput(v).readBool()
	return b, err == nil
}

func (r *Reader) GetInt32(key string) (int32, bool) {
	v, ok := r.current().value(key)
	if !ok {
		return 0, false
	}
	x, err := newCodedInput(v).readInt32()
	return x, err == nil
}

func (r *Reader) GetInt64(key string) (int64, bool) {
	v, ok := r.current().value(key)
	if !ok {
		return 0, false
	}
	x, err := newCodedInput(v).readVarint64()
	return int64(x), err == nil
}

func (r *Reader) GetUInt32(key string) (uint32, bool) {
	v, ok := r.current().value(key)
	if !ok {
		return 0, false
	}
	x, err := newCodedInput(v).readUInt32()
	return x, err == nil
}

func (r *Reader) GetUInt64(key string) (uint64, bool) {
	v, ok := r.current().value(key)
	if !ok {
		return 0, false
	}
	x, err := newCodedInput(v).readVarint64()
	return x, err == nil
}

func (r *Reader) GetFloat32(key string) (float32, bool) {
	v, ok := r.current().value(key)
	if !ok {
		return 0, false
	}
	bits, err := newCodedInput(v).readFixed32()
	if err != nil {
		return 0, false
	}
	return math.Float32frombits(bits), true
}

func (r *Reader) GetFloat64(key string) (float64, bool) {
	v, ok := r.current().value(key)
	if !ok {
		return 0, false
	}
	bits, err := newCodedInput(v).readFixed64()
	if err != nil {
		return 0, false
	}
	return math.Float64frombits(bits), true
}

// parseDictGreedy is the tolerant variant used for corruption recovery: it
// replays as many well-formed pairs as it can and stops at the first malformed
// one instead of erroring (mirrors MMKV's greedy decode on CRC failure).
func parseDictGreedy(plain []byte) map[string][]byte {
	m := make(map[string][]byte)
	ci := newCodedInput(plain)
	if ci.atEnd() {
		return m
	}
	if _, err := ci.readVarint64(); err != nil {
		return m
	}
	for !ci.atEnd() {
		key, err := ci.readBytes()
		if err != nil {
			break
		}
		if len(key) == 0 {
			continue
		}
		val, err := ci.readBytes()
		if err != nil {
			break
		}
		if len(val) > 0 {
			m[string(key)] = val
		} else {
			delete(m, string(key))
		}
	}
	return m
}

// parseDict replays the append log into a last-write-wins map. Value slices are
// views into plain. Mirrors MiniPBCoder::decodeOneMap (MiniPBCoder.cpp:504).
func parseDict(plain []byte) (map[string][]byte, error) {
	m := make(map[string][]byte)
	ci := newCodedInput(plain)
	if ci.atEnd() {
		return m, nil
	}
	if _, err := ci.readVarint64(); err != nil { // leading dictionary-size varint
		return nil, err
	}
	for !ci.atEnd() {
		key, err := ci.readBytes()
		if err != nil {
			return nil, err
		}
		if len(key) == 0 {
			continue // mirror C: empty key, no value is read
		}
		val, err := ci.readBytes()
		if err != nil {
			return nil, err
		}
		if len(val) > 0 {
			m[string(key)] = val
		} else {
			delete(m, string(key)) // empty value == deletion marker
		}
	}
	return m, nil
}
