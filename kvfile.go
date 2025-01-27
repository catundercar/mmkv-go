package mmkvgo

import (
	"errors"
	"fmt"
	"unsafe"

	"google.golang.org/protobuf/encoding/protowire"
)

var ErrInvalidMMKVData = errors.New("invalid data format")
var ErrNotFound = errors.New("not found")

const (
	FileSizeByteOffset = 4
	MagicByteOffset    = 4
)

type KVFile struct {
	memFile  *MMKVMemoryFile
	indexMap map[string]int // key -> kv pair offset
}

func ReloadKVFile(memFile *MMKVMemoryFile) (*KVFile, error) {
	kvfile := &KVFile{
		memFile:  memFile,
		indexMap: make(map[string]int),
	}
	return kvfile, kvfile.loadFromFile()
}

func (kvfile *KVFile) ListKeys() []string {
	keys := make([]string, 0, len(kvfile.indexMap))
	for key := range kvfile.indexMap {
		keys = append(keys, key)
	}
	return keys
}

func (kvfile *KVFile) loadFromFile() error {
	data := kvfile.memFile.data
	if len(data) < 4 {
		return ErrInvalidMMKVData
	}

	var size uint32
	size = *(*uint32)(unsafe.Pointer(&data[0]))

	wire := NewProtoBuffer(data[FileSizeByteOffset+MagicByteOffset : size+FileSizeByteOffset])
	for {
		if len(wire.Unread()) == 0 {
			break
		}

		key, err := wire.DecodeStringBytes()
		if err != nil {
			return fmt.Errorf("failed to decode key: %w", err)
		}
		kvfile.indexMap[key] = wire.Position()
		// just read
		_, err = wire.DecodeRawBytes(false)
		if err != nil {
			return fmt.Errorf("failed to decode value: %w", err)
		}
	}
	return nil
}

func (kvfile *KVFile) GetBytes(key string) (v []byte, err error) {
	offset, err := kvfile.memOffset(key)
	if err != nil {
		return
	}

	v, n := protowire.ConsumeBytes(kvfile.memFile.data[offset:])
	if n < 0 {
		return v, protowire.ParseError(n)
	}
	return
}

func (kvfile *KVFile) GetString(key string) (string, error) {
	return kvfile.getBuffer(key).ToString()
}

func (kvfile *KVFile) GetInt64(key string) (int64, error) {
	return kvfile.getBuffer(key).ToInt64()
}

func (kvfile *KVFile) GetInt32(key string) (int32, error) {
	return kvfile.getBuffer(key).ToInt32()
}

func (kvfile *KVFile) GetUInt64(key string) (v uint64, err error) {
	return kvfile.getBuffer(key).ToUInt64()
}

func (kvfile *KVFile) GetUInt32(key string) (uint32, error) {
	return kvfile.getBuffer(key).ToUInt32()
}

func (kvfile *KVFile) GetFloat32(key string) (float32, error) {
	return kvfile.getBuffer(key).ToFloat32()
}

func (kvfile *KVFile) GetFloat64(key string) (float64, error) {
	return kvfile.getBuffer(key).ToFloat64()
}

func (kvfile *KVFile) getBuffer(key string) buffer {
	offset, err := kvfile.memOffset(key)
	return buffer{data: kvfile.memFile.data, offset: offset, err: err}
}

func (kvfile *KVFile) memOffset(key string) (int, error) {
	offset, ok := kvfile.indexMap[key]
	if !ok {
		return 0, ErrNotFound
	}
	return FileSizeByteOffset + MagicByteOffset + offset, nil
}
