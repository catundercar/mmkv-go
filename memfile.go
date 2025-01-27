package mmkvgo

import (
	"os"
	"syscall"
)

var DEFAULT_MMAP_SIZE uint64

type MMKVMemoryFile struct {
	diskFile *os.File
	mSize    uint64 // memory size
	readOnly bool
	data     []byte
}

func NewMMKVMemoryFile(path string, size uint64, readOnly bool) (*MMKVMemoryFile, error) {
	diskFile, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0666)
	if err != nil {
		return nil, err
	}

	m := &MMKVMemoryFile{
		diskFile: diskFile,
		readOnly: readOnly,
		mSize:    size,
	}
	return m, nil
}

func (m *MMKVMemoryFile) mmap() error {
	mode := syscall.PROT_READ
	if m.readOnly {
		mode = syscall.PROT_READ
	} else {
		mode = syscall.PROT_READ | syscall.PROT_WRITE
	}
	data, err := syscall.Mmap(int(m.diskFile.Fd()), 0, int(m.mSize), mode, syscall.MAP_SHARED)
	if err != nil {
		return err
	}
	m.data = data
	return nil
}

func (m *MMKVMemoryFile) ReloadFromFile(expectedCapacity uint64) error {
	return m.mmap()
}

func (m *MMKVMemoryFile) Close() error {
	return m.diskFile.Close()
}
