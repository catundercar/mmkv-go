package harness

import (
	"bytes"
	"math"
	"testing"

	"github.com/catundercar/mmkv-go"
	cgommkv "tencent.com/mmkv"
)

// TestPuregoWriteCgoReads is the reverse interop gate: a file written by the
// pure-Go Writer MUST be read back identically by the official cgo library.
// Together with TestCgoEqualsPurego (C++ writes → Go reads) this is the
// bidirectional differential the full-interop design requires.
//
// Note: the Phase-A Writer emits version=4 (Flag), which MMKV >= v1.3.0 reads
// natively; wiring this into the per-version CI gate (and the older-version
// version-targeting) is part of completing Phase A.
func TestPuregoWriteCgoReads(t *testing.T) {
	dir := ensureInit(t)
	const id = "purego_write"

	w := mmkv.NewWriter(dir, id)
	w.SetBool("b", true)
	w.SetInt32("i32", -123456) // negative → 10-byte varint, the interop footgun
	w.SetInt32("i32max", math.MaxInt32)
	w.SetInt64("i64", math.MinInt64)
	w.SetUInt32("u32", math.MaxUint32)
	w.SetUInt64("u64", math.MaxUint64)
	w.SetFloat32("f32", 3.14159)
	w.SetFloat64("f64", -2.718281828)
	w.SetString("s", "你好,世界🌍 MMKV")
	w.SetBytes("by", []byte{0, 1, 2, 250, 251, 252})
	w.SetInt32("ow", 111)
	w.SetInt32("ow", 222) // overwrite: last value wins
	if err := w.Flush(); err != nil {
		t.Fatalf("Writer.Flush: %v", err)
	}

	// First cgo open of this id loads from the file the Writer just produced.
	m := cgommkv.MMKVWithID(id)

	if got := m.GetBool("b"); got != true {
		t.Errorf("bool b = %v, want true", got)
	}
	if got := m.GetInt32("i32"); got != -123456 {
		t.Errorf("int32 i32 = %d, want -123456", got)
	}
	if got := m.GetInt32("i32max"); got != math.MaxInt32 {
		t.Errorf("int32 i32max = %d", got)
	}
	if got := m.GetInt64("i64"); got != math.MinInt64 {
		t.Errorf("int64 i64 = %d", got)
	}
	if got := m.GetUInt32("u32"); got != math.MaxUint32 {
		t.Errorf("uint32 u32 = %d", got)
	}
	if got := m.GetUInt64("u64"); got != math.MaxUint64 {
		t.Errorf("uint64 u64 = %d", got)
	}
	if got := m.GetFloat32("f32"); !eqF32(got, 3.14159) {
		t.Errorf("float32 f32 = %v", got)
	}
	if got := m.GetFloat64("f64"); !eqF64(got, -2.718281828) {
		t.Errorf("float64 f64 = %v", got)
	}
	if got := m.GetString("s"); got != "你好,世界🌍 MMKV" {
		t.Errorf("string s = %q", got)
	}
	if got := m.GetBytes("by"); !bytes.Equal(got, []byte{0, 1, 2, 250, 251, 252}) {
		t.Errorf("bytes by = % x", got)
	}
	if got := m.GetInt32("ow"); got != 222 {
		t.Errorf("int32 ow = %d, want 222 (overwrite)", got)
	}
}
