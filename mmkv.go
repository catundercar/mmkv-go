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

	mu sync.Mutex // thread lock; serializes goroutines for this instance
	fl *fileLock  // cross-process flock on the .crc fd (used only when multiProcess)

	data *memoryFile
	meta *memoryFile

	info       metaInfo
	dict       map[string][]byte // key -> value blob (view into the plaintext region)
	actualSize uint32
	crcDigest  uint32
	needLoad   bool
	closed     bool
	lastErr    error
}

type mmkvConfig struct {
	multiProcess bool
}

// MMKVOption configures an MMKV instance at open time.
type MMKVOption func(*mmkvConfig)

// WithMultiProcess opens the instance for cross-process use: every read takes a
// shared flock and every write an exclusive flock on the ".crc" file,
// interlocking with other processes (Go or C++). The writer side of any process
// sharing the file MUST also be multi-process. Without it, flock is skipped and
// reads stay fast (single-process, like MMKV's default).
func WithMultiProcess() MMKVOption { return func(c *mmkvConfig) { c.multiProcess = true } }

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
		rootDir:      rootDir,
		mmapID:       mmapID,
		key:          key,
		multiProcess: cfg.multiProcess,
		dict:         map[string][]byte{},
	}
	if err := m.open(); err != nil {
		return nil, err
	}
	gInstances[key] = m
	return m, nil
}

func (m *MMKV) open() error {
	dataPath := dataPathFor(m.rootDir, m.mmapID)
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
	return m.loadData()
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
	} else if live.crcDigest != m.crcDigest || live.actualSize != m.actualSize {
		m.reloadBestEffort(false)
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
	// discard to empty (last resort; matches MMKV's non-recover strategy)
	m.dict = map[string][]byte{}
	m.actualSize = 0
	m.crcDigest = 0
	return nil
}

func (m *MMKV) crcValid(actual, want uint32) bool {
	return crc32.ChecksumIEEE(m.data.memory()[4:4+actual]) == want
}

func (m *MMKV) decodeRegion(actual, crc uint32) error {
	region := m.data.memory()[4 : 4+actual]
	dict, err := parseDict(region) // plaintext; encryption decrypts here in a later phase
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

// value returns the value blob for key (expiration handling is added in a later
// phase). Caller holds the lock.
func (m *MMKV) value(key string) ([]byte, bool) {
	v, ok := m.dict[key]
	return v, ok
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
	return len(m.dict)
}

// AllKeys returns all keys, sorted.
func (m *MMKV) AllKeys() []string {
	m.lockShared()
	defer m.unlockShared()
	if m.closed {
		return nil
	}
	m.checkLoadData()
	keys := make([]string, 0, len(m.dict))
	for k := range m.dict {
		keys = append(keys, k)
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
