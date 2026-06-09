//go:build unix

package mmkv

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestMemoryFileWriteReadback(t *testing.T) {
	p := filepath.Join(t.TempDir(), "data")
	mf, err := openMemoryFile(p, 100)
	if err != nil {
		t.Fatal(err)
	}
	page := syscall.Getpagesize()
	if mf.fileSize() != page {
		t.Fatalf("size = %d, want one page %d", mf.fileSize(), page)
	}
	payload := []byte("hello mmkv")
	copy(mf.memory(), payload)
	if err := mf.msync(true); err != nil {
		t.Fatal(err)
	}
	if err := mf.close(); err != nil {
		t.Fatal(err)
	}
	// the write must be durable on disk for another opener (here, os.ReadFile).
	got, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(got[:len(payload)]) != string(payload) {
		t.Fatalf("readback = %q, want %q", got[:len(payload)], payload)
	}
}

func TestMemoryFileTruncateGrow(t *testing.T) {
	p := filepath.Join(t.TempDir(), "data")
	mf, err := openMemoryFile(p, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer mf.close()
	page := syscall.Getpagesize()
	marker := []byte{0xAA, 0xBB, 0xCC, 0xDD}
	copy(mf.memory(), marker)

	if err := mf.truncate(page*2 + 1); err != nil {
		t.Fatal(err)
	}
	if want := page * 3; mf.fileSize() != want { // page-rounded
		t.Fatalf("grown size = %d, want %d", mf.fileSize(), want)
	}
	mem := mf.memory()
	if len(mem) != page*3 {
		t.Fatalf("mapping len = %d, want %d", len(mem), page*3)
	}
	for i, b := range marker {
		if mem[i] != b { // existing data preserved across remap
			t.Fatalf("byte %d = %#x, want %#x (data lost on grow)", i, mem[i], b)
		}
	}
	if mem[page] != 0 { // grown region zero-filled
		t.Fatalf("grown region not zero: mem[%d] = %#x", page, mem[page])
	}
}
