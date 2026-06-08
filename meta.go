package mmkv

import (
	"encoding/binary"
	"fmt"
)

// metaInfo mirrors mmkv::MMKVMetaInfo (Core/MMKVMetaInfo.hpp), a fixed-layout
// little-endian struct dumped to the "<mmapID>.crc" file.
type metaInfo struct {
	crcDigest  uint32
	version    uint32
	sequence   uint32
	iv         [16]byte
	actualSize uint32
	flags      uint64
}

// Field offsets within the struct (AES_IV_LEN = 16).
const (
	offCRC        = 0
	offVersion    = 4
	offSequence   = 8
	offIV         = 12
	offActualSize = 28
	offFlags      = 104
	metaSize      = 112
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
		crcDigest:  binary.LittleEndian.Uint32(buf[offCRC:]),
		version:    binary.LittleEndian.Uint32(buf[offVersion:]),
		sequence:   binary.LittleEndian.Uint32(buf[offSequence:]),
		actualSize: binary.LittleEndian.Uint32(buf[offActualSize:]),
		flags:      binary.LittleEndian.Uint64(buf[offFlags:]),
	}
	copy(m.iv[:], buf[offIV:offIV+16])
	return m, nil
}

func (m *metaInfo) expireEnabled() bool { return m.flags&flagEnableExpire != 0 }
