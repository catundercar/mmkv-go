package mmkvgo

import (
	"path"
	"syscall"
	"testing"

	"tencent.com/mmkv"
)

func TestNewMMKVMemoryFile(t *testing.T) {
	fpath := path.Join("test", "data")
	mmkv.InitializeMMKVWithLogLevel(fpath, mmkv.MMKVLogInfo)
	kv := mmkv.DefaultMMKV()
	kv.SetInt32(1, "test")
	kv.Close()

	DEFAULT_MMAP_SIZE = uint64(syscall.Getpagesize())
	t.Logf("DEFAULT_MMAP_SIZE: %d\n", DEFAULT_MMAP_SIZE)
	memoryFile, err := NewMMKVMemoryFile(path.Join(fpath, "mmkv.default.crc"), DEFAULT_MMAP_SIZE, true)
	if err != nil {
		t.Fatalf("Failed to create memory file: %v", err)
	}
	defer memoryFile.Close()
	err = memoryFile.ReloadFromFile(DEFAULT_MMAP_SIZE)
	if err != nil {
		t.Fatalf("Failed to reload from file: %v", err)
	}

	metaInfo := &MetaInfo{}
	err = metaInfo.ReadMetaInfoFromMemoryFile(memoryFile)
	if err != nil {
		t.Fatalf("Failed to read meta info: %v", err)
	}
	t.Logf("MetaInfo:")
	t.Logf("MetaInfo: %+v", metaInfo)
}

func BenchmarkReadMetaInfoFromMemoryFile(b *testing.B) {

	fpath := path.Join("test", "data")
	DEFAULT_MMAP_SIZE = uint64(syscall.Getpagesize())
	memoryFile, err := NewMMKVMemoryFile(path.Join(fpath, "mmkv.default.crc"), DEFAULT_MMAP_SIZE, true)
	if err != nil {
		b.Fatalf("Failed to create memory file: %v", err)
	}
	defer memoryFile.Close()
	err = memoryFile.ReloadFromFile(DEFAULT_MMAP_SIZE)
	if err != nil {
		b.Fatalf("Failed to reload from file: %v", err)
	}

	metaInfo := &MetaInfo{}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		err = metaInfo.ReadMetaInfoFromMemoryFile(memoryFile)
		if err != nil {
			b.Fatalf("Failed to read meta info: %v", err)
		}
	}
}

func BenchmarkReadMetaInfoFromMemoryFileCopy(b *testing.B) {
	fpath := path.Join("test", "data")
	DEFAULT_MMAP_SIZE = uint64(syscall.Getpagesize())
	memoryFile, err := NewMMKVMemoryFile(path.Join(fpath, "mmkv.default.crc"), DEFAULT_MMAP_SIZE, true)
	if err != nil {
		b.Fatalf("Failed to create memory file: %v", err)
	}
	defer memoryFile.Close()
	err = memoryFile.ReloadFromFile(DEFAULT_MMAP_SIZE)
	if err != nil {
		b.Fatalf("Failed to reload from file: %v", err)
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, err = ReadMetaInfoFromMemoryFile(memoryFile)
		if err != nil {
			b.Fatalf("Failed to read meta info: %v", err)
		}
	}
}
