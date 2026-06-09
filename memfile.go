//go:build unix

package mmkv

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// memoryFile is a read-write mmap of a file, the writer-side counterpart to the
// reader's read-only mmap. It mirrors MMKV's MemoryFile on POSIX
// (Core/MemoryFile.cpp): the backing file is page-rounded, grown via ftruncate +
// re-mmap, and flushed via msync.
//
// Lifetime hazard: truncate() unmaps the old mapping, so any []byte previously
// returned by memory() is invalid afterwards — never retain a view across a
// truncate/close (same constraint as a remap in C++).
type memoryFile struct {
	f    *os.File
	data []byte
	size int
}

// pageRoundUp rounds n up to a multiple of the page size, with a one-page floor
// (MMKV's DEFAULT_MMAP_SIZE is one page).
func pageRoundUp(n int) int {
	p := unix.Getpagesize()
	if n <= p {
		return p
	}
	return ((n + p - 1) / p) * p
}

// openMemoryFile opens path read-write (creating it if needed), ensures it is at
// least minSize bytes (page-rounded), and mmaps it MAP_SHARED.
func openMemoryFile(path string, minSize int) (*memoryFile, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o666)
	if err != nil {
		return nil, fmt.Errorf("mmkv: open %s: %w", path, err)
	}
	st, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	size := int(st.Size())
	if want := pageRoundUp(minSize); size < want {
		if err := f.Truncate(int64(want)); err != nil {
			_ = f.Close()
			return nil, fmt.Errorf("mmkv: truncate %s: %w", path, err)
		}
		size = want
	}
	data, err := unix.Mmap(int(f.Fd()), 0, size, unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED)
	if err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("mmkv: mmap %s: %w", path, err)
	}
	return &memoryFile{f: f, data: data, size: size}, nil
}

func (mf *memoryFile) memory() []byte { return mf.data }
func (mf *memoryFile) fileSize() int  { return mf.size }

// truncate resizes the file to a page-rounded newSize and re-mmaps it. A grown
// region reads as zero (ftruncate semantics). Invalidates any prior memory().
func (mf *memoryFile) truncate(newSize int) error {
	want := pageRoundUp(newSize)
	if want == mf.size {
		return nil
	}
	if err := mf.f.Truncate(int64(want)); err != nil {
		return fmt.Errorf("mmkv: truncate: %w", err)
	}
	if mf.data != nil {
		if err := unix.Munmap(mf.data); err != nil {
			return fmt.Errorf("mmkv: munmap: %w", err)
		}
		mf.data = nil
	}
	data, err := unix.Mmap(int(mf.f.Fd()), 0, want, unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED)
	if err != nil {
		return fmt.Errorf("mmkv: mmap: %w", err)
	}
	mf.data = data
	mf.size = want
	return nil
}

// remap re-maps to the file's current on-disk size without changing it — used
// to pick up a grow/shrink performed by another process (cross-process reload).
// Invalidates any prior memory().
func (mf *memoryFile) remap() error {
	st, err := mf.f.Stat()
	if err != nil {
		return err
	}
	size := int(st.Size())
	if size == mf.size && mf.data != nil {
		return nil
	}
	if mf.data != nil {
		if err := unix.Munmap(mf.data); err != nil {
			return fmt.Errorf("mmkv: munmap: %w", err)
		}
		mf.data = nil
	}
	data, err := unix.Mmap(int(mf.f.Fd()), 0, size, unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED)
	if err != nil {
		return fmt.Errorf("mmkv: mmap: %w", err)
	}
	mf.data = data
	mf.size = size
	return nil
}

// msync flushes the mapping; sync=true is MS_SYNC (durable), false MS_ASYNC.
func (mf *memoryFile) msync(sync bool) error {
	flag := unix.MS_ASYNC
	if sync {
		flag = unix.MS_SYNC
	}
	if err := unix.Msync(mf.data, flag); err != nil {
		return fmt.Errorf("mmkv: msync: %w", err)
	}
	return nil
}

func (mf *memoryFile) close() error {
	var err error
	if mf.data != nil {
		err = unix.Munmap(mf.data)
		mf.data = nil
	}
	if mf.f != nil {
		if cerr := mf.f.Close(); err == nil {
			err = cerr
		}
		mf.f = nil
	}
	return err
}
