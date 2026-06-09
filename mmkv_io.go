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

func (m *MMKV) setValue(key string, blob []byte) error {
	m.lockExclusive()
	defer m.unlockExclusive()
	if m.closed {
		return ErrClosed
	}
	m.checkLoadData()
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

// setBlob appends the pair when it fits the free tail (MMKV's append fast path);
// otherwise it compacts via a full write-back. Caller holds the exclusive lock.
func (m *MMKV) setBlob(key string, blob []byte) error {
	o := &codedOutput{}
	o.writeData([]byte(key))
	o.writeData(blob)
	pair := o.bytes()

	// The append fast path writes plaintext bytes in place; with encryption the
	// region is a single CFB stream, so an encrypted set always re-encrypts via a
	// full write-back (correct and simple; incremental encrypted append is a
	// future optimization).
	spaceLeft := m.data.fileSize() - 4 - int(m.actualSize)
	if m.crypt == nil && len(pair) <= spaceLeft && len(m.dict) > 0 && m.info.version >= versionFlag {
		p := 4 + int(m.actualSize)
		m.appendRaw(pair)
		blobStart := p + (len(pair) - len(blob))
		m.dict[key] = m.data.memory()[blobStart : blobStart+len(blob)]
		return nil
	}
	m.dict[key] = blob // a fresh heap blob; replaced by an mmap view after the rewrite
	return m.fullWriteback()
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

// fullWriteback re-packs the whole dict from offset 4 (fresh ItemSizeHolder),
// growing the file first if needed, then writes the full meta with a bumped
// sequence and an advanced last-confirmed point, and msyncs. Caller holds the
// exclusive lock.
func (m *MMKV) fullWriteback() error {
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

	if 4+len(payload) > m.data.fileSize() {
		newSize := m.data.fileSize()
		if newSize < 1 {
			newSize = 1
		}
		for 4+len(payload) >= newSize { // double until it fits (page-rounded by truncate)
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
	if len(tomb) <= spaceLeft && m.info.version >= versionFlag && m.actualSize > 0 {
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
	if err := m.data.msync(sync); err != nil {
		return err
	}
	return m.meta.msync(sync)
}
