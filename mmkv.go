//go:build unix

package mmkv

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"math"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"unsafe"
)

// ErrClosed is returned by operations on a closed MMKV.
var ErrClosed = errors.New("mmkv: instance closed")

// ErrReadOnly is returned by mutating operations on a read-only instance.
var ErrReadOnly = errors.New("mmkv: instance is read-only")

// MMKV is a cgo-free, read+write MMKV instance: it reads and writes the official
// on-disk format and uses the same flock protocol, so it interoperates with the
// C++ library over the same files across processes. Open it with MMKVWithID.
//
// Concurrency mirrors MMKV C++: every operation takes the instance thread lock
// (serializing goroutines) and, in multi-process mode, a cross-process flock on
// the ".crc" file (shared for reads, exclusive for writes); single-process mode
// skips the flock, keeping reads cheap. One instance is shared per file per
// process (see MMKVWithID).
//
// View lifetime: GetBytes/GetString return zero-copy views into the live mapping
// that are valid only until the next call on this instance (a write or a
// cross-process reload can remap the data). Use the Copy variants to retain.
// POSIX-only (Linux + macOS).
type MMKV struct {
	rootDir      string
	mmapID       string
	key          string // registry key (absolute data path)
	multiProcess bool
	readOnly     bool

	mu sync.Mutex // thread lock; serializes goroutines for this instance
	fl *fileLock  // cross-process flock on the .crc fd (used only when multiProcess)

	data  *memoryFile
	meta  *memoryFile
	crypt *aesCFB // nil = plaintext

	info       metaInfo
	dict       map[string][]byte // key -> value blob (view into the plaintext region)
	actualSize uint32
	crcDigest  uint32
	needLoad   bool
	closed     bool
	lastErr    error

	// needFullWriteback: the on-disk region failed its CRC and was salvaged
	// (greedy decode), so the running crcDigest is poisoned — incremental
	// appends would persist a wrong CRC. The next write must be a full
	// write-back (MMKV's checkDataValid sets needFullWriteback on
	// OnErrorRecover and rewrites at load; we also repair at open and route
	// later writes off the fast paths until a rewrite lands).
	needFullWriteback bool

	enableExpire   bool   // values carry a trailing 4-byte expire timestamp
	expiredSeconds uint32 // default per-set duration; 0 = never (in-memory only)

	compareBeforeSet bool // skip a write when the new value equals the stored one (in-memory only)

	onContentChanged func() // called after a cross-process reload (optional)
	recover          bool   // on unrecoverable CRC failure, greedy-salvage instead of discarding
}

type mmkvConfig struct {
	multiProcess     bool
	readOnly         bool
	cryptKey         []byte
	onContentChanged func()
	recover          bool
}

// MMKVOption configures an MMKV instance at open time.
type MMKVOption func(*mmkvConfig)

// WithMultiProcess opens the instance for cross-process use: every read takes a
// shared flock and every write an exclusive flock on the ".crc" file,
// interlocking with other processes (Go or C++). The writer side of any process
// sharing the file MUST also be multi-process. Without it, flock is skipped and
// reads stay fast (single-process, like MMKV's default).
func WithMultiProcess() MMKVOption { return func(c *mmkvConfig) { c.multiProcess = true } }

// WithReadOnly opens the instance read-only: the files are mapped O_RDONLY and
// every mutating call returns ErrReadOnly. The file must already exist. This is
// the single-type way to read a store you must not (or cannot) write.
func WithReadOnly() MMKVOption { return func(c *mmkvConfig) { c.readOnly = true } }

// WithCryptKey opens an AES-encrypted instance. The AES width follows MMKV: a
// key longer than 16 bytes selects AES-256, otherwise AES-128 (truncated/zero-
// padded). Pass the same key the file was written with; open the same file with
// consistent options across the process (the instance is cached by path).
func WithCryptKey(key []byte) MMKVOption {
	return func(c *mmkvConfig) { c.cryptKey = append([]byte(nil), key...) }
}

// WithContentChangedHandler registers a callback invoked after the instance
// reloads because another process changed the file (multi-process mode). It runs
// while the instance lock is held, so do not call back into this instance from
// it; keep it short.
func WithContentChangedHandler(fn func()) MMKVOption {
	return func(c *mmkvConfig) { c.onContentChanged = fn }
}

// WithRecoverOnError salvages as much as possible (greedy decode) when the data
// region fails its CRC and the last-confirmed snapshot can't be restored either,
// instead of discarding to empty (MMKV's OnErrorRecover). A writable open then
// repairs the store immediately with a full write-back; a salvage during a
// cross-process reload is repaired by the next write. Read-only instances just
// serve the salvaged view.
func WithRecoverOnError() MMKVOption {
	return func(c *mmkvConfig) { c.recover = true }
}

var (
	gInstanceMu sync.Mutex
	gInstances  = map[string]*MMKV{}
)

func registryKey(rootDir, mmapID string) string {
	p := dataPathFor(rootDir, mmapID)
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return filepath.Clean(p)
}

// MMKVWithID opens (or returns the already-open) read+write instance for
// <rootDir>/<encodeFilePath(mmapID)> (+ ".crc"). One instance is cached per file
// per process — concurrent callers get the same *MMKV, which is required for
// correctness (two instances would each flock the same fd without mutual
// exclusion and diverge in memory). POSIX-only.
func MMKVWithID(rootDir, mmapID string, opts ...MMKVOption) (*MMKV, error) {
	var cfg mmkvConfig
	for _, o := range opts {
		o(&cfg)
	}
	key := registryKey(rootDir, mmapID)

	gInstanceMu.Lock()
	defer gInstanceMu.Unlock()
	if m := gInstances[key]; m != nil && !m.closed {
		return m, nil
	}
	m := &MMKV{
		rootDir:          rootDir,
		mmapID:           mmapID,
		key:              key,
		multiProcess:     cfg.multiProcess,
		readOnly:         cfg.readOnly,
		dict:             map[string][]byte{},
		onContentChanged: cfg.onContentChanged,
		recover:          cfg.recover,
	}
	if len(cfg.cryptKey) > 0 {
		m.crypt = newAESCFB(cfg.cryptKey)
	}
	if err := m.open(); err != nil {
		return nil, err
	}
	gInstances[key] = m
	return m, nil
}

func (m *MMKV) open() error {
	dataPath := dataPathFor(m.rootDir, m.mmapID)
	if m.readOnly {
		data, err := openMemoryFileReadOnly(dataPath)
		if err != nil {
			return err
		}
		meta, err := openMemoryFileReadOnly(dataPath + crcSuffix)
		if err != nil {
			_ = data.close()
			return err
		}
		m.data, m.meta = data, meta
		m.fl = newFileLock(meta.f)
		return m.loadData()
	}
	if err := os.MkdirAll(filepath.Dir(dataPath), 0o777); err != nil {
		return fmt.Errorf("mmkv: mkdir: %w", err)
	}
	data, err := openMemoryFile(dataPath, 0) // floor: one page
	if err != nil {
		return err
	}
	meta, err := openMemoryFile(dataPath+crcSuffix, metaSize)
	if err != nil {
		_ = data.close()
		return err
	}
	m.data = data
	m.meta = meta
	m.fl = newFileLock(meta.f)
	if err := m.loadData(); err != nil {
		return err
	}
	// Repair a salvaged store right away, like MMKV's OnErrorRecover load
	// (loadFromFile runs a fullWriteback when checkDataValid flags it): rewrite
	// region+meta from the salvaged dict so the on-disk CRC is consistent again
	// before any append, and bump the sequence so other processes clean-reload.
	if m.needFullWriteback {
		if m.multiProcess {
			_ = m.fl.lock(exclusiveLock)
			defer func() { _ = m.fl.unlock(exclusiveLock) }()
		}
		return m.fullWriteback()
	}
	return nil
}

// ---- locking (process flock is a no-op in single-process mode) ----

func (m *MMKV) lockShared() {
	m.mu.Lock()
	if m.multiProcess {
		_ = m.fl.lock(sharedLock)
	}
}

func (m *MMKV) unlockShared() {
	if m.multiProcess {
		_ = m.fl.unlock(sharedLock)
	}
	m.mu.Unlock()
}

func (m *MMKV) lockExclusive() {
	m.mu.Lock()
	if m.multiProcess {
		_ = m.fl.lock(exclusiveLock)
	}
}

func (m *MMKV) unlockExclusive() {
	if m.multiProcess {
		_ = m.fl.unlock(exclusiveLock)
	}
	m.mu.Unlock()
}

// checkLoadData is the cross-process freshness probe (MMKV's checkLoadData):
// compare our in-memory meta against the live mmap'd .crc. A sequence change
// means another process did a full write-back/clear/trim (the data file may have
// grown/shrunk) → remap + full reload; a crc/actualSize change at the same
// sequence means a plain append → reload (the data mapping already sees the new
// bytes via MAP_SHARED). Single-process needs no probe. Caller holds the lock.
func (m *MMKV) checkLoadData() {
	if m.needLoad {
		m.needLoad = false
		m.reloadBestEffort(true)
		return
	}
	if !m.multiProcess {
		return
	}
	live, err := parseMeta(m.meta.memory())
	if err != nil {
		m.lastErr = err
		return
	}
	if live.sequence != m.info.sequence {
		m.reloadBestEffort(true)
		m.notifyChanged()
	} else if live.crcDigest != m.crcDigest || live.actualSize != m.actualSize {
		m.reloadBestEffort(false)
		m.notifyChanged()
	}
}

func (m *MMKV) notifyChanged() {
	if m.onContentChanged != nil {
		m.onContentChanged()
	}
}

// reloadBestEffort re-decodes from the files; on error it keeps the prior
// in-memory state and records lastErr (reads keep serving what they had).
func (m *MMKV) reloadBestEffort(remap bool) {
	if remap {
		if err := m.data.remap(); err != nil {
			m.lastErr = err
			return
		}
	}
	if err := m.loadData(); err != nil {
		m.lastErr = err
	}
}

func (m *MMKV) readActualSize() uint32 {
	if m.info.version >= versionActualSize {
		return m.info.actualSize
	}
	mem := m.data.memory()
	if len(mem) >= 4 {
		return binary.LittleEndian.Uint32(mem[:4])
	}
	return 0
}

// loadData reads the meta and decodes the data region into dict, with MMKV's
// recovery: accept the current (actualSize, crc); else roll back to the
// last-confirmed snapshot; else discard to empty. Caller holds the lock.
func (m *MMKV) loadData() error {
	mi, err := parseMeta(m.meta.memory())
	if err != nil {
		return err
	}
	m.info = *mi
	if m.info.version > maxSupportedVersion {
		return fmt.Errorf("%w: %d", ErrUnsupportedVersion, m.info.version)
	}
	// expiration is persisted as a meta flag; the per-set duration is in-memory.
	m.enableExpire = m.info.version >= versionFlag && m.info.expireEnabled()
	fileSize := len(m.data.memory())

	actual := m.readActualSize()
	if int(actual) < fileSize && int(actual)+4 <= fileSize && m.crcValid(actual, m.info.crcDigest) {
		return m.decodeRegion(actual, m.info.crcDigest)
	}
	// rollback to the last fully-synced snapshot
	if la := m.info.lastActualSize; int(la) < fileSize && int(la)+4 <= fileSize && m.crcValid(la, m.info.lastCRCDigest) {
		m.info.actualSize = la
		m.info.crcDigest = m.info.lastCRCDigest
		return m.decodeRegion(la, m.info.lastCRCDigest)
	}
	if m.recover {
		return m.salvage()
	}
	// discard to empty (last resort; matches MMKV's non-recover strategy)
	m.dict = map[string][]byte{}
	m.actualSize = 0
	m.crcDigest = 0
	return nil
}

// salvage greedily decodes a corrupt region (clamped to the file) into dict,
// keeping the meta-derived size/crc so a multi-process reload doesn't loop, and
// flags needFullWriteback: a writable open repairs the store immediately
// (MMKV's OnErrorRecover load does a fullWriteback); a salvage during a
// runtime reload is repaired by the next write instead (which may only hold a
// shared flock here, so it cannot rewrite in place).
func (m *MMKV) salvage() error {
	fileSize := len(m.data.memory())
	actual := m.info.actualSize
	if int(actual)+4 > fileSize {
		if fileSize >= 4 {
			actual = uint32(fileSize - 4)
		} else {
			actual = 0
		}
	}
	region := m.data.memory()[4 : 4+actual]
	plain := region
	if m.crypt != nil {
		if p, err := m.crypt.Decrypt(region, m.info.iv[:]); err == nil {
			plain = p
		}
	}
	m.dict = parseDictGreedy(plain)
	m.actualSize = m.info.actualSize
	m.crcDigest = m.info.crcDigest
	m.needFullWriteback = true
	return nil
}

func (m *MMKV) crcValid(actual, want uint32) bool {
	return crc32.ChecksumIEEE(m.data.memory()[4:4+actual]) == want
}

func (m *MMKV) decodeRegion(actual, crc uint32) error {
	region := m.data.memory()[4 : 4+actual] // ciphertext when encrypted (CRC is over this)
	plain := region
	if m.crypt != nil {
		p, err := m.crypt.Decrypt(region, m.info.iv[:])
		if err != nil {
			return err
		}
		plain = p // heap buffer; dict views point into it (kept alive by dict)
	}
	dict, err := parseDict(plain)
	if err != nil {
		return err
	}
	m.dict = dict
	m.actualSize = actual
	m.crcDigest = crc
	return nil
}

// Close unmaps the files and drops the instance from the registry. Outstanding
// GetBytes/GetString views become invalid.
func (m *MMKV) Close() error {
	gInstanceMu.Lock()
	if gInstances[m.key] == m {
		delete(gInstances, m.key)
	}
	gInstanceMu.Unlock()

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil
	}
	m.closed = true
	var err error
	if m.data != nil {
		err = m.data.close()
	}
	if m.meta != nil {
		if e := m.meta.close(); err == nil {
			err = e
		}
	}
	return err
}

// Err returns the last best-effort reload error, if any.
func (m *MMKV) Err() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lastErr
}

// MmapID returns the instance's mmapID; RootDir returns its root directory.
func (m *MMKV) MmapID() string  { return m.mmapID }
func (m *MMKV) RootDir() string { return m.rootDir }

// ClearMemoryCache drops the in-memory dictionary so the next access reloads
// from disk (matches MMKV's clearMemoryCache). It does not change the files.
func (m *MMKV) ClearMemoryCache() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return
	}
	m.dict = map[string][]byte{}
	m.actualSize = 0
	m.crcDigest = 0
	m.needLoad = true
}

// value returns the value blob for key. With expiration on, the trailing 4-byte
// little-endian timestamp is stripped and an expired key (timestamp != 0 && <=
// now) reads as absent (matches MMKV). Caller holds the lock.
func (m *MMKV) value(key string) ([]byte, bool) {
	v, ok := m.dict[key]
	if !ok {
		return nil, false
	}
	if m.enableExpire && len(v) >= 4 {
		t := binary.LittleEndian.Uint32(v[len(v)-4:])
		if t != 0 && t <= nowUnix() {
			return nil, false // expired
		}
		v = v[:len(v)-4]
	}
	return v, true
}

// bytesValue strips the inner length layer MMKV wraps around string/[]byte
// values (see encode.go / reader.go). Caller holds the lock.
func (m *MMKV) bytesValue(key string) ([]byte, bool) {
	v, ok := m.value(key)
	if !ok {
		return nil, false
	}
	b, err := newCodedInput(v).readBytes()
	if err != nil {
		return nil, false
	}
	return b, true
}

// ---- typed getters ----

func (m *MMKV) GetBool(key string) (bool, bool) {
	m.lockShared()
	defer m.unlockShared()
	if m.closed {
		return false, false
	}
	m.checkLoadData()
	v, ok := m.value(key)
	if !ok {
		return false, false
	}
	b, err := newCodedInput(v).readBool()
	return b, err == nil
}

func (m *MMKV) GetInt32(key string) (int32, bool) {
	m.lockShared()
	defer m.unlockShared()
	if m.closed {
		return 0, false
	}
	m.checkLoadData()
	v, ok := m.value(key)
	if !ok {
		return 0, false
	}
	x, err := newCodedInput(v).readInt32()
	return x, err == nil
}

func (m *MMKV) GetInt64(key string) (int64, bool) {
	m.lockShared()
	defer m.unlockShared()
	if m.closed {
		return 0, false
	}
	m.checkLoadData()
	v, ok := m.value(key)
	if !ok {
		return 0, false
	}
	x, err := newCodedInput(v).readVarint64()
	return int64(x), err == nil
}

func (m *MMKV) GetUInt32(key string) (uint32, bool) {
	m.lockShared()
	defer m.unlockShared()
	if m.closed {
		return 0, false
	}
	m.checkLoadData()
	v, ok := m.value(key)
	if !ok {
		return 0, false
	}
	x, err := newCodedInput(v).readUInt32()
	return x, err == nil
}

func (m *MMKV) GetUInt64(key string) (uint64, bool) {
	m.lockShared()
	defer m.unlockShared()
	if m.closed {
		return 0, false
	}
	m.checkLoadData()
	v, ok := m.value(key)
	if !ok {
		return 0, false
	}
	x, err := newCodedInput(v).readVarint64()
	return x, err == nil
}

func (m *MMKV) GetFloat32(key string) (float32, bool) {
	m.lockShared()
	defer m.unlockShared()
	if m.closed {
		return 0, false
	}
	m.checkLoadData()
	v, ok := m.value(key)
	if !ok {
		return 0, false
	}
	bits, err := newCodedInput(v).readFixed32()
	if err != nil {
		return 0, false
	}
	return math.Float32frombits(bits), true
}

func (m *MMKV) GetFloat64(key string) (float64, bool) {
	m.lockShared()
	defer m.unlockShared()
	if m.closed {
		return 0, false
	}
	m.checkLoadData()
	v, ok := m.value(key)
	if !ok {
		return 0, false
	}
	bits, err := newCodedInput(v).readFixed64()
	if err != nil {
		return 0, false
	}
	return math.Float64frombits(bits), true
}

// GetBytes returns a zero-copy view; valid only until the next call on this
// instance. Use GetBytesCopy to retain.
func (m *MMKV) GetBytes(key string) ([]byte, bool) {
	m.lockShared()
	defer m.unlockShared()
	if m.closed {
		return nil, false
	}
	m.checkLoadData()
	return m.bytesValue(key)
}

// GetBytesCopy returns an independent copy of the value.
func (m *MMKV) GetBytesCopy(key string) ([]byte, bool) {
	m.lockShared()
	defer m.unlockShared()
	if m.closed {
		return nil, false
	}
	m.checkLoadData()
	v, ok := m.bytesValue(key)
	if !ok {
		return nil, false
	}
	out := make([]byte, len(v))
	copy(out, v)
	return out, true
}

// GetString returns a zero-copy string view; valid only until the next call on
// this instance, and the bytes must not be mutated. Use GetStringCopy to retain.
func (m *MMKV) GetString(key string) (string, bool) {
	m.lockShared()
	defer m.unlockShared()
	if m.closed {
		return "", false
	}
	m.checkLoadData()
	v, ok := m.bytesValue(key)
	if !ok {
		return "", false
	}
	if len(v) == 0 {
		return "", true
	}
	return unsafe.String(unsafe.SliceData(v), len(v)), true
}

// GetStringCopy returns the value as an independent string.
func (m *MMKV) GetStringCopy(key string) (string, bool) {
	m.lockShared()
	defer m.unlockShared()
	if m.closed {
		return "", false
	}
	m.checkLoadData()
	v, ok := m.bytesValue(key)
	if !ok {
		return "", false
	}
	return string(v), true
}

// GetStringSlice returns the value as a []string (MMKV's vector<string>). The
// strings are copies, so the result outlives the next call.
func (m *MMKV) GetStringSlice(key string) ([]string, bool) {
	m.lockShared()
	defer m.unlockShared()
	if m.closed {
		return nil, false
	}
	m.checkLoadData()
	items, ok := m.bytesValue(key)
	if !ok {
		return nil, false
	}
	return decodeStringSlice(items), true
}

// GetValueSize returns the number of stored value bytes for key (the encoded
// value, expire timestamp stripped), or -1 if absent.
func (m *MMKV) GetValueSize(key string) int {
	m.lockShared()
	defer m.unlockShared()
	if m.closed {
		return -1
	}
	m.checkLoadData()
	v, ok := m.value(key)
	if !ok {
		return -1
	}
	return len(v)
}

// WriteValueToBuffer copies the stored value bytes for key into buf and returns
// the number copied, or -1 if the key is absent or buf is too small.
func (m *MMKV) WriteValueToBuffer(key string, buf []byte) int {
	m.lockShared()
	defer m.unlockShared()
	if m.closed {
		return -1
	}
	m.checkLoadData()
	v, ok := m.value(key)
	if !ok || len(buf) < len(v) {
		return -1
	}
	return copy(buf, v)
}

// Contains reports whether key is present.
func (m *MMKV) Contains(key string) bool {
	m.lockShared()
	defer m.unlockShared()
	if m.closed {
		return false
	}
	m.checkLoadData()
	_, ok := m.value(key)
	return ok
}

// Count returns the number of keys.
func (m *MMKV) Count() int {
	m.lockShared()
	defer m.unlockShared()
	if m.closed {
		return 0
	}
	m.checkLoadData()
	if !m.enableExpire {
		return len(m.dict)
	}
	n := 0
	for k := range m.dict {
		if _, ok := m.value(k); ok {
			n++
		}
	}
	return n
}

// AllKeys returns all live (non-expired) keys, sorted.
func (m *MMKV) AllKeys() []string {
	m.lockShared()
	defer m.unlockShared()
	if m.closed {
		return nil
	}
	m.checkLoadData()
	keys := make([]string, 0, len(m.dict))
	for k := range m.dict {
		if _, ok := m.value(k); ok {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys
}

// TotalSize returns the data file size (bytes). ActualSize returns the bytes of
// the live KV region.
func (m *MMKV) TotalSize() int {
	m.lockShared()
	defer m.unlockShared()
	if m.closed {
		return 0
	}
	return m.data.fileSize()
}

func (m *MMKV) ActualSize() int {
	m.lockShared()
	defer m.unlockShared()
	if m.closed {
		return 0
	}
	m.checkLoadData()
	return int(m.actualSize)
}
