package mmkvgo

import (
	"fmt"
	"unsafe"
)

const (
	AESKeyLen   = 16
	ReservedLen = 16
)

type MetaInfo struct {
	// added in version 0
	crc32 uint32

	// added in version 1
	version  uint32
	sequence uint32 // full write back count

	// added in version 2
	aesVector [AESKeyLen]byte // random iv for encryption, aes.BlockSize (16 bytes)

	// added in version 3, try to reduce file corruption
	actualSize     uint32
	lastActualSize uint32
	lastCRC32      uint32

	//_reversed []byte // 64 bytes
}

func (m *MetaInfo) ReadMetaInfoFromMemoryFile(mf *MMKVMemoryFile) error {
	metainfo, err := ReadMetaInfoFromMemoryFile(mf)
	if err != nil {
		return err
	}
	*m = *metainfo
	return nil
}

func ReadMetaInfoFromMemoryFile(mf *MMKVMemoryFile) (*MetaInfo, error) {
	if len(mf.data) < int(unsafe.Sizeof(MetaInfo{})) {
		return nil, fmt.Errorf("data too small, %d < %d", len(mf.data), int(unsafe.Sizeof(MetaInfo{})))
	}
	return (*MetaInfo)(unsafe.Pointer(&mf.data[0])), nil
}
