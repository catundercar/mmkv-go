//go:build unix

package mmkv

import (
	"encoding/binary"
	"os"
	"testing"
)

// TestMMKVContentChangedHandler simulates another process bumping the meta
// sequence on disk and verifies the handler fires on the next access (the
// checkLoadData reload path).
func TestMMKVContentChangedHandler(t *testing.T) {
	dir := t.TempDir()
	const id = "cc"
	changed := 0
	m, err := MMKVWithID(dir, id, WithMultiProcess(), WithContentChangedHandler(func() { changed++ }))
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	m.SetInt32("x", 1)
	m.Sync()

	// simulate another process: bump the on-disk sequence (data unchanged, still
	// CRC-valid) so the reload succeeds and content-changed fires.
	crcPath := dataPathFor(dir, id) + crcSuffix
	f, err := os.OpenFile(crcPath, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	var buf [4]byte
	if _, err := f.ReadAt(buf[:], offSequence); err != nil {
		t.Fatal(err)
	}
	binary.LittleEndian.PutUint32(buf[:], binary.LittleEndian.Uint32(buf[:])+1)
	if _, err := f.WriteAt(buf[:], offSequence); err != nil {
		t.Fatal(err)
	}
	f.Close()

	if v, ok := m.GetInt32("x"); !ok || v != 1 { // triggers checkLoadData → reload
		t.Errorf("x = %v,%v", v, ok)
	}
	if changed == 0 {
		t.Error("content-changed handler did not fire")
	}
}

// TestMMKVRecoverOnError corrupts the data region so the CRC fails and checks
// that WithRecoverOnError salvages data while the default discards to empty.
// Trim is used so the last-confirmed snapshot equals the current one — otherwise
// the loader would (correctly) roll back to an earlier good snapshot instead of
// exercising the discard/recover path.
func TestMMKVRecoverOnError(t *testing.T) {
	dir := t.TempDir()
	const id = "rec"
	dp := dataPathFor(dir, id)

	regionEnd := func() int { // 4 + actualSize, read from the meta
		meta, err := os.ReadFile(dp + crcSuffix)
		if err != nil {
			t.Fatal(err)
		}
		return 4 + int(binary.LittleEndian.Uint32(meta[offActualSize:]))
	}
	write := func() int {
		m, err := MMKVWithID(dir, id)
		if err != nil {
			t.Fatal(err)
		}
		m.SetString("a", "alpha")
		m.SetString("b", "bravo")
		m.SetString("c", "charlie")
		if err := m.Trim(); err != nil { // lastConfirmed := current snapshot
			t.Fatal(err)
		}
		end := regionEnd()
		m.Close()
		return end
	}

	// default: corrupt a mid-region byte → both current and lastConfirmed fail
	// CRC → discard to empty.
	end := write()
	data, err := os.ReadFile(dp)
	if err != nil {
		t.Fatal(err)
	}
	data[4+(end-4)/2] ^= 0xFF
	if err := os.WriteFile(dp, data, 0o666); err != nil {
		t.Fatal(err)
	}
	d, err := MMKVWithID(dir, id)
	if err != nil {
		t.Fatalf("default open after corruption: %v", err)
	}
	if d.Count() != 0 {
		t.Errorf("default recovery should discard to empty, count=%d", d.Count())
	}
	d.Close()
	RemoveStorageInstance(t, dir, id)

	// recover: corrupt the LAST region byte (a value byte: structure intact, CRC
	// broken) → greedy salvage keeps all keys.
	end = write()
	data, _ = os.ReadFile(dp)
	data[end-1] ^= 0xFF
	os.WriteFile(dp, data, 0o666)

	r, err := MMKVWithID(dir, id, WithRecoverOnError())
	if err != nil {
		t.Fatalf("recover open: %v", err)
	}
	defer r.Close()
	if r.Count() == 0 {
		t.Error("recover should have salvaged keys")
	}
}

// RemoveStorageInstance is a test helper to drop the cached instance + files.
func RemoveStorageInstance(t *testing.T, dir, id string) {
	t.Helper()
	if err := RemoveStorage(dir, id); err != nil {
		t.Fatal(err)
	}
}
