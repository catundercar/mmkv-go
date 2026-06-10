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

// TestMMKVRecoverRepairsOnOpen is the regression guard for salvage+append CRC
// poisoning: a writable WithRecoverOnError open must repair the store with a
// full write-back (like MMKV's OnErrorRecover load), so a later append starts
// from a CRC base that matches the bytes. Before the fix, the append updated
// the CRC incrementally from the stale meta value, and the next default open
// failed CRC on both snapshots and discarded everything — losing the append.
func TestMMKVRecoverRepairsOnOpen(t *testing.T) {
	dir := t.TempDir()
	const id = "recfix"
	dp := dataPathFor(dir, id)

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
	meta, err := os.ReadFile(dp + crcSuffix)
	if err != nil {
		t.Fatal(err)
	}
	end := 4 + int(binary.LittleEndian.Uint32(meta[offActualSize:]))
	seqBefore := binary.LittleEndian.Uint32(meta[offSequence:])
	m.Close()

	// corrupt the LAST region byte (a value byte of "c": structure intact, CRC
	// broken for both the current and the lastConfirmed snapshot)
	data, err := os.ReadFile(dp)
	if err != nil {
		t.Fatal(err)
	}
	data[end-1] ^= 0xFF
	if err := os.WriteFile(dp, data, 0o666); err != nil {
		t.Fatal(err)
	}

	r, err := MMKVWithID(dir, id, WithRecoverOnError())
	if err != nil {
		t.Fatalf("recover open: %v", err)
	}
	// repaired at open: flag consumed, on-disk CRC matches the bytes again,
	// sequence bumped so other processes clean-reload
	if r.needFullWriteback {
		t.Error("open did not repair: needFullWriteback still set")
	}
	if !r.crcValid(r.actualSize, r.info.crcDigest) {
		t.Error("open did not repair: region CRC still broken")
	}
	if r.info.sequence == seqBefore {
		t.Error("repair write-back did not bump the sequence")
	}
	// the append that used to poison the file
	if err := r.SetString("d", "delta"); err != nil {
		t.Fatal(err)
	}
	r.Close()

	// a DEFAULT open (no recover) must now see a fully valid store
	m2, err := MMKVWithID(dir, id)
	if err != nil {
		t.Fatal(err)
	}
	defer m2.Close()
	if got := m2.Count(); got != 4 {
		t.Fatalf("post-repair store lost data: count=%d, want 4", got)
	}
	for k, want := range map[string]string{"a": "alpha", "b": "bravo", "d": "delta"} {
		if v, ok := m2.GetString(k); !ok || v != want {
			t.Errorf("%s = %q,%v, want %q", k, v, ok, want)
		}
	}
	if !m2.Contains("c") { // salvaged with a garbled value byte, but present
		t.Error("c missing after salvage+repair")
	}
}

// TestMMKVNeedFullWritebackRoutesWrites pins the lazy repair path (a salvage
// during a cross-process reload can't rewrite under a shared flock): with
// needFullWriteback set, the next set and the next remove must take the full
// write-back instead of the in-place fast paths, then clear the flag.
func TestMMKVNeedFullWritebackRoutesWrites(t *testing.T) {
	m, err := MMKVWithID(t.TempDir(), "lazyfix")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	m.SetString("a", "alpha")
	m.SetString("b", "bravo")

	m.needFullWriteback = true // simulate a runtime-reload salvage
	seq := m.info.sequence
	if err := m.SetString("a", "alpha2"); err != nil {
		t.Fatal(err)
	}
	if m.needFullWriteback {
		t.Error("set did not clear needFullWriteback")
	}
	if m.info.sequence == seq {
		t.Error("set did not take the full write-back (sequence unchanged)")
	}
	if !m.crcValid(m.actualSize, m.info.crcDigest) {
		t.Error("region CRC broken after routed set")
	}

	m.needFullWriteback = true
	seq = m.info.sequence
	if err := m.RemoveValueForKey("b"); err != nil {
		t.Fatal(err)
	}
	if m.needFullWriteback {
		t.Error("remove did not clear needFullWriteback")
	}
	if m.info.sequence == seq {
		t.Error("remove did not take the full write-back (sequence unchanged)")
	}
	if !m.crcValid(m.actualSize, m.info.crcDigest) {
		t.Error("region CRC broken after routed remove")
	}
	if v, ok := m.GetString("a"); !ok || v != "alpha2" {
		t.Errorf("a = %q,%v", v, ok)
	}
}

// RemoveStorageInstance is a test helper to drop the cached instance + files.
func RemoveStorageInstance(t *testing.T, dir, id string) {
	t.Helper()
	if err := RemoveStorage(dir, id); err != nil {
		t.Fatal(err)
	}
}
