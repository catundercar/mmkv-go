//go:build unix

package mmkv

import (
	"errors"
	"os"
	"syscall"
)

// flockShared takes a blocking shared (LOCK_SH) flock — the same primitive and
// lock file (.crc) MMKV uses on non-Android POSIX platforms, so it interlocks
// with an MMKV writer's exclusive lock across processes.
func flockShared(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_SH)
}

func flockUnlock(f *os.File) {
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}

// mmapReadonly maps the whole file read-only & shared, so writes by another
// process to the same file pages are visible without a syscall.
func mmapReadonly(f *os.File, size int) ([]byte, error) {
	if size < metaSize {
		return nil, errors.New("puremmkv: .crc smaller than meta struct")
	}
	return syscall.Mmap(int(f.Fd()), 0, size, syscall.PROT_READ, syscall.MAP_SHARED)
}

func munmap(b []byte) error {
	if len(b) == 0 {
		return nil
	}
	return syscall.Munmap(b)
}
