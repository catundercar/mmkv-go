//go:build unix

package mmkv

import (
	"bytes"
	"testing"
)

// overridePairSize returns the encoded size of one key/blob pair as setBlob
// builds it: writeData(key) + writeData(blob).
func overridePairSize(key string, blob []byte) int {
	o := &codedOutput{}
	o.writeData([]byte(key))
	o.writeData(blob)
	return len(o.bytes())
}

// TestSingleKeyOverrideKeepsRegionFlat is the regression guard for the
// single-key override fast path: repeated sets of one key must rewrite the
// region in place (actualSize pinned at holder+pair) instead of appending,
// which without override fills the file every ~400 sets and pays a full
// write-back + msync per cycle (the BenchmarkSetInt32_MMKV arm64 CI anomaly).
func TestSingleKeyOverrideKeepsRegionFlat(t *testing.T) {
	dir := t.TempDir()
	m, err := MMKVWithID(dir, "ov")
	if err != nil {
		t.Fatal(err)
	}
	// 20000 sets ≈ 50x what a one-page file holds as appends; values span
	// several varint widths so the pair size changes along the way.
	for i := 0; i < 20000; i++ {
		if err := m.SetInt32("k", int32(i)); err != nil {
			t.Fatal(err)
		}
		want := uint32(len(itemSizeHolderBytes) + overridePairSize("k", int32Blob(int32(i))))
		if m.actualSize != want {
			t.Fatalf("set #%d: actualSize = %d, want %d (override not taken)", i, m.actualSize, want)
		}
	}
	if v, ok := m.GetInt32("k"); !ok || v != 19999 {
		t.Fatalf("GetInt32 = %d,%v", v, ok)
	}
	if n := m.data.fileSize(); n != pageRoundUp(0) {
		t.Errorf("file grew to %d; override should never grow it", n)
	}
	// The on-disk bytes must be a valid store on their own: reopen from disk.
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	m2, err := MMKVWithID(dir, "ov")
	if err != nil {
		t.Fatal(err)
	}
	defer m2.Close()
	if v, ok := m2.GetInt32("k"); !ok || v != 19999 {
		t.Fatalf("after reopen: GetInt32 = %d,%v", v, ok)
	}
}

// TestOverrideRegionMatchesFullWriteback: an overridden region must be
// byte-identical (and CRC-identical) to a fresh full write-back of the same
// single-key state — i.e. override changes no on-disk format.
func TestOverrideRegionMatchesFullWriteback(t *testing.T) {
	a, err := MMKVWithID(t.TempDir(), "x")
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := MMKVWithID(t.TempDir(), "x")
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	if err := a.SetString("s", "first"); err != nil { // empty dict: full write-back
		t.Fatal(err)
	}
	if err := a.SetString("s", "second"); err != nil { // single key: override
		t.Fatal(err)
	}
	if err := b.SetString("s", "second"); err != nil { // same final state in one write
		t.Fatal(err)
	}
	if a.actualSize != b.actualSize {
		t.Fatalf("actualSize: override %d, write-back %d", a.actualSize, b.actualSize)
	}
	regA := a.data.memory()[4 : 4+int(a.actualSize)]
	regB := b.data.memory()[4 : 4+int(b.actualSize)]
	if !bytes.Equal(regA, regB) {
		t.Errorf("regions differ:\noverride  % x\nwriteback % x", regA, regB)
	}
	if a.crcDigest != b.crcDigest {
		t.Errorf("crc: override %08x, write-back %08x", a.crcDigest, b.crcDigest)
	}
}

// TestOverrideAfterRemoveAll covers the needOverride branch: once every key is
// removed (tombstones still on disk), the next set rewrites the region from
// its start, discarding the dead bytes without a full write-back.
func TestOverrideAfterRemoveAll(t *testing.T) {
	dir := t.TempDir()
	m, err := MMKVWithID(dir, "rmov")
	if err != nil {
		t.Fatal(err)
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(m.SetInt32("a", 1))
	must(m.SetInt32("b", 2))
	must(m.RemoveValueForKey("a"))
	must(m.RemoveValueForKey("b"))
	if len(m.dict) != 0 || m.actualSize == 0 {
		t.Fatalf("precondition: dict=%d actualSize=%d", len(m.dict), m.actualSize)
	}
	must(m.SetString("c", "v"))
	want := uint32(len(itemSizeHolderBytes) + overridePairSize("c", stringBlob("v")))
	if m.actualSize != want {
		t.Fatalf("actualSize = %d, want %d (needOverride not taken)", m.actualSize, want)
	}
	must(m.Close())
	m2, err := MMKVWithID(dir, "rmov")
	if err != nil {
		t.Fatal(err)
	}
	defer m2.Close()
	if v, ok := m2.GetString("c"); !ok || v != "v" {
		t.Fatalf("c = %q,%v", v, ok)
	}
	if _, ok := m2.GetInt32("a"); ok {
		t.Error("a survived the override")
	}
	if _, ok := m2.GetInt32("b"); ok {
		t.Error("b survived the override")
	}
}

// TestMultiProcessNeverOverrides: in multi-process mode sets must keep
// appending (the C++ override path is single-process only; a rewound tail
// would race other processes reading the shared mapping).
func TestMultiProcessNeverOverrides(t *testing.T) {
	m, err := MMKVWithID(t.TempDir(), "mp", WithMultiProcess())
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if err := m.SetInt32("k", 0); err != nil {
		t.Fatal(err)
	}
	base := m.actualSize
	if err := m.SetInt32("k", 1); err != nil {
		t.Fatal(err)
	}
	if m.actualSize <= base {
		t.Fatalf("multi-process set must append: actualSize %d -> %d", base, m.actualSize)
	}
}

// TestOverrideFallsBackWhenPairExceedsFile: a pair too large for the current
// file skips override (checkSizeForOverride), grows via the full write-back,
// and later small sets of the same key override again inside the larger file.
func TestOverrideFallsBackWhenPairExceedsFile(t *testing.T) {
	dir := t.TempDir()
	m, err := MMKVWithID(dir, "big")
	if err != nil {
		t.Fatal(err)
	}
	big := make([]byte, pageRoundUp(0)) // pair > file: forces grow + write-back
	for i := range big {
		big[i] = byte(i % 251)
	}
	if err := m.SetBytes("k", big); err != nil {
		t.Fatal(err)
	}
	grown := m.data.fileSize()
	if grown <= pageRoundUp(0) {
		t.Fatalf("file did not grow: %d", grown)
	}
	if err := m.SetInt32("k", 7); err != nil { // small again: override resumes
		t.Fatal(err)
	}
	want := uint32(len(itemSizeHolderBytes) + overridePairSize("k", int32Blob(7)))
	if m.actualSize != want {
		t.Fatalf("actualSize = %d, want %d (override not resumed)", m.actualSize, want)
	}
	if n := m.data.fileSize(); n != grown {
		t.Errorf("override changed the file size: %d -> %d", grown, n)
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	m2, err := MMKVWithID(dir, "big")
	if err != nil {
		t.Fatal(err)
	}
	defer m2.Close()
	if v, ok := m2.GetInt32("k"); !ok || v != 7 {
		t.Fatalf("k = %d,%v", v, ok)
	}
}
