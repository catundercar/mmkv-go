//go:build !unix

package mmkv

import (
	"errors"
	"os"
)

func flockShared(f *os.File) error {
	return errors.New("mmkv: only supported on POSIX (needs flock)")
}

func flockExclusive(f *os.File) error {
	return errors.New("mmkv: only supported on POSIX (needs flock)")
}

func flockUnlock(f *os.File) {}

func mmapReadonly(f *os.File, size int) ([]byte, error) {
	return nil, errors.New("mmkv: only supported on POSIX (needs mmap)")
}

func munmap(b []byte) error { return nil }
