//go:build unix

package mmkv

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"sort"
)

// ---- typed setters ----

func (m *MMKV) SetBool(key string, v bool) error       { return m.setValue(key, boolBlob(v)) }
func (m *MMKV) SetInt32(key string, v int32) error     { return m.setValue(key, int32Blob(v)) }
func (m *MMKV) SetInt64(key string, v int64) error     { return m.setValue(key, int64Blob(v)) }
func (m *MMKV) SetUInt32(key string, v uint32) error   { return m.setValue(key, uint32Blob(v)) }
func (m *MMKV) SetUInt64(key string, v uint64) error   { return m.setValue(key, uint64Blob(v)) }
func (m *MMKV) SetFloat32(key string, v float32) error { return m.setValue(key, float32Blob(v)) }
func (m *MMKV) SetFloat64(key string, v float64) error { return m.setValue(key, float64Blob(v)) }
func (m *MMKV) SetString(key, v string) error          { return m.setValue(key, stringBlob(v)) }
func (m *MMKV) SetBytes(key string, v []byte) error    { return m.setValue(key, bytesBlob(v)) }

// SetStringSlice stores a []string (MMKV's vector<string>).
func (m *MMKV) SetStringSlice(key string, v []string) error {
	return m.setValue(key, stringSliceBlob(v))
}

func (m *MMKV) setValue(key string, blob []byte) error {
	m.lockExclusive()
	defer m.unlockExclusive()
	if m.closed {
		return ErrClosed
	}
	if m.readOnly {
		return ErrReadOnly
	}
	m.checkLoadData()
	return m.setValueLocked(key, blob)
}

// setValueLocked applies compareBeforeSet + expiration then writes. Caller holds
// the exclusive lock and has already rejected closed/read-only and run
// checkLoadData.
func (m *MMKV) setValueLocked(key string, blob []byte) error {
	// compareBeforeSet (mutually exclusive with expiration): skip a redundant
	// write when the stored value already equals the new one.
	if m.compareBeforeSet && !m.enableExpire {
		if old, ok := m.dict[key]; ok && bytes.Equal(old, blob) {
			return nil
		}
	}
	if m.enableExpire {
		blob = appendExpire(blob, m.expiredSeconds)
	}
	return m.setBlob(key, blob)
}

// ImportFrom copies every live key from src into this instance (applying this
// instance's expiration/compareBeforeSet policy), returning the number of keys
// imported. src is snapshotted under its own lock first, then written here, so
// two instances importing from each other can't deadlock.
func (m *MMKV) ImportFrom(src *MMKV) (int, error) {
	if src == nil || src == m {
		return 0, nil
	}
	src.lockShared()
	if src.closed {
		src.unlockShared()
		return 0, ErrClosed
	}
	src.checkLoadData()
	keys := make([]string, 0, len(src.dict))
	vals := make([][]byte, 0, len(src.dict))
	for k := range src.dict {
		if v, ok := src.value(k); ok { // value() strips any expire timestamp
			keys = append(keys, k)
			vals = append(vals, append([]byte(nil), v...))
		}
	}
	src.unlockShared()

	m.lockExclusive()
	defer m.unlockExclusive()
	if m.closed {
		return 0, ErrClosed
	}
	if m.readOnly {
		return 0, ErrReadOnly
	}
	m.checkLoadData()
	n := 0
	for i, k := range keys {
		if err := m.setValueLocked(k, vals[i]); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

// setBlob writes key=blob the cheapest way MMKV allows: a single-key store in
// single-process mode rewrites the region from its start (override fast path),
// anything else appends the pair when it fits the free tail, and otherwise it
// compacts via a full write-back. Caller holds the exclusive lock.
func (m *MMKV) setBlob(key string, blob []byte) error {
	wasEmpty := len(m.dict) == 0
	o := &codedOutput{}
	o.writeData([]byte(key))
	o.writeData(blob)
	pair := o.bytes()

	// Both fast paths below write incrementally in place, which is only sound
	// when the running CRC matches the on-disk bytes — a salvaged store
	// (needFullWriteback, see salvage) must take the full write-back instead,
	// like MMKV's OnErrorRecover load.
	fastOK := m.crypt == nil && !m.needFullWriteback && m.info.version >= versionFlag

	// Single-key override fast path (MMKV >=1.3.x setDataForKey, the
	// onlyOneKey/needOverride branches): in single-process mode, when this key is
	// the only live one — or every key was removed but stale bytes remain — and
	// the pair fits the file as-is (checkSizeForOverride), rewrite the region
	// from its start instead of appending. Repeated sets of one key then never
	// fill the file, so they never trigger the periodic full write-back and its
	// msync. C++ keeps appending in multi-process mode (a rewound tail would race
	// other processes), and encrypted sets stay on the full write-back below.
	if fastOK && !m.multiProcess &&
		4+len(itemSizeHolderBytes)+len(pair) <= m.data.fileSize() {
		_, ok := m.dict[key]
		if (ok && len(m.dict) == 1) || (wasEmpty && m.actualSize > 0) {
			m.overrideRaw(key, pair, len(blob))
			return nil
		}
	}

	// The append fast path writes plaintext bytes in place; with encryption the
	// region is a single CFB stream, so an encrypted set always re-encrypts via a
	// full write-back (correct and simple; incremental encrypted append is a
	// future optimization).
	spaceLeft := m.data.fileSize() - 4 - int(m.actualSize)
	if fastOK && len(pair) <= spaceLeft && len(m.dict) > 0 {
		p := 4 + int(m.actualSize)
		m.appendRaw(pair)
		blobStart := p + (len(pair) - len(blob))
		m.dict[key] = m.data.memory()[blobStart : blobStart+len(blob)]
		return nil
	}
	m.dict[key] = blob // a fresh heap blob; replaced by an mmap view after the rewrite
	return m.fullWritebackGrow(!wasEmpty, len(pair))
}

// appendRaw writes pair at the free tail, advances actualSize, updates the
// running CRC incrementally, and writes only crc+actualSize to the meta (no
// sequence bump, no msync — exactly MMKV's plain append). Caller holds the lock.
func (m *MMKV) appendRaw(pair []byte) {
	p := 4 + int(m.actualSize)
	mem := m.data.memory()
	copy(mem[p:p+len(pair)], pair)
	m.actualSize += uint32(len(pair))
	m.crcDigest = crc32.Update(m.crcDigest, crc32.IEEETable, pair)
	m.info.actualSize = m.actualSize
	m.info.crcDigest = m.crcDigest
	meta := m.meta.memory()
	binary.LittleEndian.PutUint32(meta[offCRC:], m.crcDigest)
	binary.LittleEndian.PutUint32(meta[offActualSize:], m.actualSize)
}

// overrideRaw rewrites the region from its start as [itemSizeHolder][pair],
// dropping every prior append: actualSize and the running CRC restart from the
// fresh region, and only crc+actualSize are written to the meta — sequence
// kept, no msync (MMKV's doOverrideDataWithKey + recalculateCRCDigestOnly).
// Caller holds the lock and has checked the pair fits the file.
func (m *MMKV) overrideRaw(key string, pair []byte, blobLen int) {
	mem := m.data.memory()
	p := 4 + copy(mem[4:], itemSizeHolderBytes)
	copy(mem[p:], pair)
	m.actualSize = uint32(len(itemSizeHolderBytes) + len(pair))
	m.crcDigest = crc32.ChecksumIEEE(mem[4 : 4+int(m.actualSize)])
	m.info.actualSize = m.actualSize
	m.info.crcDigest = m.crcDigest
	meta := m.meta.memory()
	binary.LittleEndian.PutUint32(meta[offCRC:], m.crcDigest)
	binary.LittleEndian.PutUint32(meta[offActualSize:], m.actualSize)
	blobStart := p + (len(pair) - blobLen)
	m.dict[key] = mem[blobStart : blobStart+blobLen]
}

// fullWriteback re-packs the whole dict from offset 4 (fresh ItemSizeHolder),
// growing the file first if needed, then writes the full meta with a bumped
// sequence and an advanced last-confirmed point, and msyncs. Caller holds the
// exclusive lock.
func (m *MMKV) fullWriteback() error { return m.fullWritebackGrow(true, 0) }

// fullWritebackGrow is fullWriteback with MMKV's ensureMemorySize semantics.
// needSync: the very first insert into an empty dict skips the msync
// (ensureMemorySize passes needSync = !dic.empty()); every other write-back
// syncs. incoming: the byte size of the pair that triggered this write-back
// (0 for non-set callers like ReKey/expire) — it feeds the future-usage
// headroom term exactly like C++'s `newSize` (which is counted on top of the
// dict even for a same-key update).
func (m *MMKV) fullWritebackGrow(needSync bool, incoming int) error {
	keys := make([]string, 0, len(m.dict))
	for k := range m.dict {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	region := encodeRegion(keys, m.dict) // PLAINTEXT region, separate buffer (no aliasing with the mmap)
	total := len(region)

	// payload is what lands on disk: ciphertext when encrypted (a fresh IV per
	// full write-back, like MMKV), else the plaintext region. CFB preserves length.
	payload := region
	if m.crypt != nil {
		iv, err := randomIV()
		if err != nil {
			return err
		}
		ct, err := m.crypt.Encrypt(region, iv[:])
		if err != nil {
			return err
		}
		payload = ct
		m.info.iv = iv
	}

	// Growth with future-usage headroom (MMKV's expandAndWriteBack,
	// MMKV_IO.cpp:512): size for ~max(8, n/2) more average-sized items, not just
	// the current dict, so repeated sets amortize into appends instead of a full
	// write-back per set. File size is local policy — readers use actualSize and
	// ignore the free tail, so interop is unaffected. Non-set callers
	// (incoming == 0) grow to exact fit only, like C++ (their write-backs never
	// pass through ensureMemorySize).
	lenNeeded := 4 + len(payload) + incoming
	target := lenNeeded
	grow := lenNeeded >= m.data.fileSize()
	if incoming > 0 {
		laterDicCount := max(1, len(m.dict)+1)
		avgItemSize := (lenNeeded + laterDicCount - 1) / laterDicCount
		futureUsage := avgItemSize * max(8, laterDicCount/2)
		target = lenNeeded + futureUsage
		if needSync && target >= m.data.fileSize() {
			grow = true
		}
	}
	if grow {
		newSize := m.data.fileSize()
		if newSize < 1 {
			newSize = 1
		}
		for target >= newSize { // double until headroom fits (page-rounded by truncate)
			newSize *= 2
		}
		if err := m.data.truncate(newSize); err != nil {
			return err
		}
	}

	mem := m.data.memory()
	clear(mem[:4]) // legacy header stays zero for version>=3
	copy(mem[4:4+len(payload)], payload)
	m.actualSize = uint32(len(payload))
	m.crcDigest = crc32.ChecksumIEEE(mem[4 : 4+len(payload)]) // CRC over the on-disk bytes (ciphertext when encrypted)

	m.info.sequence++
	m.info.version = versionFlag
	m.info.actualSize = m.actualSize
	m.info.crcDigest = m.crcDigest
	m.info.lastActualSize = m.actualSize
	m.info.lastCRCDigest = m.crcDigest
	copy(m.meta.memory(), m.info.marshal())

	// rebuild dict views: from the plaintext region when encrypted, else the mmap.
	src := region
	if m.crypt == nil {
		src = mem[4 : 4+total]
	}
	dict, err := parseDict(src)
	if err != nil {
		return err
	}
	m.dict = dict
	m.needFullWriteback = false // region+meta rewritten from scratch: consistent again

	if !needSync {
		return nil
	}
	if err := m.data.msync(true); err != nil {
		return err
	}
	return m.meta.msync(true)
}

// RemoveValueForKey deletes key. It appends an empty-value tombstone when it
// fits (durable + visible cross-process), else compacts via a full write-back.
func (m *MMKV) RemoveValueForKey(key string) error {
	m.lockExclusive()
	defer m.unlockExclusive()
	if m.closed {
		return ErrClosed
	}
	if m.readOnly {
		return ErrReadOnly
	}
	m.checkLoadData()
	if _, ok := m.dict[key]; !ok {
		return nil
	}
	delete(m.dict, key)

	o := &codedOutput{}
	o.writeData([]byte(key))
	o.writeData(nil) // empty value = deletion marker
	tomb := o.bytes()
	spaceLeft := m.data.fileSize() - 4 - int(m.actualSize)
	// a salvaged store (needFullWriteback) rewrites instead of appending — its
	// running CRC is poisoned (see setBlob)
	if len(tomb) <= spaceLeft && m.info.version >= versionFlag && m.actualSize > 0 &&
		!m.needFullWriteback {
		m.appendRaw(tomb)
		return nil
	}
	return m.fullWriteback()
}

// RemoveValuesForKeys deletes several keys, compacting once.
func (m *MMKV) RemoveValuesForKeys(keys []string) error {
	m.lockExclusive()
	defer m.unlockExclusive()
	if m.closed {
		return ErrClosed
	}
	if m.readOnly {
		return ErrReadOnly
	}
	m.checkLoadData()
	removed := false
	for _, k := range keys {
		if _, ok := m.dict[k]; ok {
			delete(m.dict, k)
			removed = true
		}
	}
	if !removed {
		return nil
	}
	return m.fullWriteback()
}

// ClearAll resets the instance to empty, truncating the data file back to the
// default size and bumping the sequence so other processes reload.
func (m *MMKV) ClearAll() error {
	m.lockExclusive()
	defer m.unlockExclusive()
	if m.closed {
		return ErrClosed
	}
	if m.readOnly {
		return ErrReadOnly
	}
	m.checkLoadData()
	return m.clearAllLocked()
}

// clearAllLocked is the body of ClearAll; caller holds the exclusive lock.
func (m *MMKV) clearAllLocked() error {
	if err := m.data.truncate(0); err != nil { // page-rounded floor = one page
		return err
	}
	mem := m.data.memory()
	clear(mem[:4])
	m.dict = map[string][]byte{}
	m.actualSize = 0
	m.crcDigest = 0
	m.info.sequence++
	m.info.version = versionFlag
	m.info.actualSize = 0
	m.info.crcDigest = 0
	m.info.lastActualSize = 0
	m.info.lastCRCDigest = 0
	copy(m.meta.memory(), m.info.marshal())
	m.needFullWriteback = false // empty region + fresh meta: consistent again
	if err := m.data.msync(true); err != nil {
		return err
	}
	return m.meta.msync(true)
}

// Trim compacts the store (a full write-back) and then shrinks the data file
// toward ~2x the live size, floored at one page — matching MMKV's trim().
func (m *MMKV) Trim() error {
	m.lockExclusive()
	defer m.unlockExclusive()
	if m.closed {
		return ErrClosed
	}
	if m.readOnly {
		return ErrReadOnly
	}
	m.checkLoadData()
	if m.actualSize == 0 {
		return m.clearAllLocked()
	}
	if err := m.fullWriteback(); err != nil { // compact (also bumps the sequence)
		return err
	}
	floor := pageRoundUp(0)
	target := m.data.fileSize()
	for target > (int(m.actualSize)+4)*2 {
		target /= 2
	}
	if target < floor {
		target = floor
	}
	if target >= m.data.fileSize() {
		return nil // already tight
	}
	if err := m.data.truncate(target); err != nil { // remaps: invalidates dict views
		return err
	}
	if err := m.decodeRegion(m.actualSize, m.crcDigest); err != nil { // rebuild views into the new mapping
		return err
	}
	return m.meta.msync(true)
}

// Sync flushes pending writes to disk durably (MS_SYNC); Async uses MS_ASYNC.
func (m *MMKV) Sync() error  { return m.syncFlush(true) }
func (m *MMKV) Async() error { return m.syncFlush(false) }

func (m *MMKV) syncFlush(sync bool) error {
	m.lockExclusive()
	defer m.unlockExclusive()
	if m.closed {
		return ErrClosed
	}
	if m.readOnly {
		return nil // nothing to flush
	}
	if err := m.data.msync(sync); err != nil {
		return err
	}
	return m.meta.msync(sync)
}
