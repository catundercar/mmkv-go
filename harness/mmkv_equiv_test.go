package harness

import (
	"bytes"
	"fmt"
	"math"
	"testing"

	"github.com/catundercar/mmkv-go"
	cgommkv "tencent.com/mmkv"
)

// TestMMKVWriteCgoReads drives the live read+write MMKV type through many Set
// calls (exercising the incremental append fast path, not just a batch
// write-back) and asserts the official C++ library reads every value back. This
// is the forward interop gate for the writer.
func TestMMKVWriteCgoReads(t *testing.T) {
	dir := ensureInit(t)
	const id = "mmkv_write_cgo_read"

	m, err := mmkv.MMKVWithID(dir, id)
	if err != nil {
		t.Fatal(err)
	}
	_ = m.ClearAll()
	m.SetBool("b", true)
	m.SetInt32("i32", -123456) // negative → 10-byte varint
	m.SetInt64("i64", math.MinInt64)
	m.SetUInt32("u32", math.MaxUint32)
	m.SetUInt64("u64", math.MaxUint64)
	m.SetFloat32("f32", 3.14159)
	m.SetFloat64("f64", -2.718281828)
	m.SetString("s", "你好,世界🌍 MMKV")
	m.SetBytes("by", []byte{0, 1, 2, 250, 251, 252})
	// many small appends after the first write-back: stress the append path
	for i := 0; i < 200; i++ {
		if err := m.SetInt32(fmt.Sprintf("n%03d", i), int32(i*7-3)); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}
	m.SetInt32("ow", 111)
	m.SetInt32("ow", 222) // overwrite
	if err := m.Sync(); err != nil {
		t.Fatal(err)
	}
	m.Close()

	r := cgommkv.MMKVWithID(id)
	if r.GetBool("b") != true {
		t.Errorf("bool b")
	}
	if r.GetInt32("i32") != -123456 {
		t.Errorf("i32 = %d", r.GetInt32("i32"))
	}
	if r.GetInt64("i64") != math.MinInt64 {
		t.Errorf("i64 = %d", r.GetInt64("i64"))
	}
	if r.GetUInt32("u32") != math.MaxUint32 {
		t.Errorf("u32 = %d", r.GetUInt32("u32"))
	}
	if r.GetUInt64("u64") != math.MaxUint64 {
		t.Errorf("u64 = %d", r.GetUInt64("u64"))
	}
	if !eqF32(r.GetFloat32("f32"), 3.14159) {
		t.Errorf("f32 = %v", r.GetFloat32("f32"))
	}
	if !eqF64(r.GetFloat64("f64"), -2.718281828) {
		t.Errorf("f64 = %v", r.GetFloat64("f64"))
	}
	if r.GetString("s") != "你好,世界🌍 MMKV" {
		t.Errorf("s = %q", r.GetString("s"))
	}
	if !bytes.Equal(r.GetBytes("by"), []byte{0, 1, 2, 250, 251, 252}) {
		t.Errorf("by = % x", r.GetBytes("by"))
	}
	for i := 0; i < 200; i++ {
		if got := r.GetInt32(fmt.Sprintf("n%03d", i)); got != int32(i*7-3) {
			t.Fatalf("append n%03d = %d, want %d", i, got, i*7-3)
		}
	}
	if r.GetInt32("ow") != 222 {
		t.Errorf("ow = %d, want 222", r.GetInt32("ow"))
	}
}

// TestCgoWriteMMKVReads is the reverse: the C++ library writes, the live MMKV
// type reads back equal.
func TestCgoWriteMMKVReads(t *testing.T) {
	dir := ensureInit(t)
	const id = "cgo_write_mmkv_read"

	w := cgommkv.MMKVWithID(id)
	w.ClearAll()
	w.SetBool(true, "b")
	w.SetInt32(-5, "i32")
	w.SetInt64(math.MaxInt64, "i64")
	w.SetUInt64(math.MaxUint64, "u64")
	w.SetFloat64(2.5, "f64")
	w.SetString("hello 世界", "s")
	w.SetBytes([]byte{9, 8, 7, 6}, "by")
	w.Sync(true)

	m, err := mmkv.MMKVWithID(dir, id)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if v, ok := m.GetBool("b"); !ok || !v {
		t.Errorf("b = %v,%v", v, ok)
	}
	if v, ok := m.GetInt32("i32"); !ok || v != -5 {
		t.Errorf("i32 = %v,%v", v, ok)
	}
	if v, ok := m.GetInt64("i64"); !ok || v != math.MaxInt64 {
		t.Errorf("i64 = %v,%v", v, ok)
	}
	if v, ok := m.GetUInt64("u64"); !ok || v != math.MaxUint64 {
		t.Errorf("u64 = %v,%v", v, ok)
	}
	if v, ok := m.GetFloat64("f64"); !ok || v != 2.5 {
		t.Errorf("f64 = %v,%v", v, ok)
	}
	if v, ok := m.GetString("s"); !ok || v != "hello 世界" {
		t.Errorf("s = %q,%v", v, ok)
	}
	if v, ok := m.GetBytes("by"); !ok || !bytes.Equal(v, []byte{9, 8, 7, 6}) {
		t.Errorf("by = % x,%v", v, ok)
	}
}
