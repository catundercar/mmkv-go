package mmkv

import (
	"encoding/binary"
	"fmt"
)

// metaInfo mirrors mmkv::MMKVMetaInfo (Core/MMKVMetaInfo.hpp), a fixed-layout
// little-endian struct dumped to the "<mmapID>.crc" file.
//
// lastActualSize/lastCRCDigest are the "last confirmed" rollback point: MMKV
// advances them only on a sequence-bumping full write-back (never on a plain
// append), so on load a torn append can be rolled back to the last fully-synced
// snapshot. See MMKV_IO.cpp writeActualSize / checkLastConfirmedInfo.
type metaInfo struct {
	crcDigest      uint32
	version        uint32
	sequence       uint32
	iv             [16]byte
	actualSize     uint32
	lastActualSize uint32
	lastCRCDigest  uint32
	flags          uint64
}

// Field offsets within the struct (AES_IV_LEN = 16).
const (
	offCRC            = 0
	offVersion        = 4
	offSequence       = 8
	offIV             = 12
	offActualSize     = 28
	offLastActualSize = 32
	offLastCRCDigest  = 36
	offFlags          = 104
	metaSize          = 112
)

// MMKVVersion values we care about.
const (
	versionActualSize = 3 // actualSize stored in meta
	versionFlag       = 4 // flags field present
	// maxSupportedVersion gates against future format drift.
	maxSupportedVersion = versionFlag
)

// flags bits (MMKVMetaInfo::MMKVMetaInfoFlag).
const flagEnableExpire = uint64(1) << 0

func parseMeta(buf []byte) (*metaInfo, error) {
	if len(buf) < metaSize {
		return nil, fmt.Errorf("mmkv: meta too short: %d < %d", len(buf), metaSize)
	}
	m := &metaInfo{
		crcDigest:      binary.LittleEndian.Uint32(buf[offCRC:]),
		version:        binary.LittleEndian.Uint32(buf[offVersion:]),
		sequence:       binary.LittleEndian.Uint32(buf[offSequence:]),
		actualSize:     binary.LittleEndian.Uint32(buf[offActualSize:]),
		lastActualSize: binary.LittleEndian.Uint32(buf[offLastActualSize:]),
		lastCRCDigest:  binary.LittleEndian.Uint32(buf[offLastCRCDigest:]),
		flags:          binary.LittleEndian.Uint64(buf[offFlags:]),
	}
	copy(m.iv[:], buf[offIV:offIV+16])
	return m, nil
}

// marshal serializes the 112-byte meta struct, little-endian (the inverse of
// parseMeta). Reserved bytes between lastCRCDigest and flags stay zero.
func (m *metaInfo) marshal() []byte {
	buf := make([]byte, metaSize)
	binary.LittleEndian.PutUint32(buf[offCRC:], m.crcDigest)
	binary.LittleEndian.PutUint32(buf[offVersion:], m.version)
	binary.LittleEndian.PutUint32(buf[offSequence:], m.sequence)
	copy(buf[offIV:offIV+16], m.iv[:])
	binary.LittleEndian.PutUint32(buf[offActualSize:], m.actualSize)
	binary.LittleEndian.PutUint32(buf[offLastActualSize:], m.lastActualSize)
	binary.LittleEndian.PutUint32(buf[offLastCRCDigest:], m.lastCRCDigest)
	binary.LittleEndian.PutUint64(buf[offFlags:], m.flags)
	return buf
}

func (m *metaInfo) expireEnabled() bool { return m.flags&flagEnableExpire != 0 }
