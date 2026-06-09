//go:build unix

package mmkv

import (
	"bytes"
	"fmt"
	"math"
	"strings"
	"testing"
)

// TestWriterReaderRoundTrip flushes a file with the Phase-A Writer and reads it
// back with the read-only Reader, asserting equality per type. The Reader is the
// oracle: it is independently gated against the official C++ library in CI
// (TestCgoEqualsPurego), so a Writer→Reader match means the bytes match what
// C++ produces — without needing cgo on the host. The harness adds the direct
// Writer→C++ differential.
func TestWriterReaderRoundTrip(t *testing.T) {
	dir := t.TempDir()
	const id = "wtest"
	w := NewWriter(dir, id)

	w.SetBool("b_t", true).SetBool("b_f", false)
	for _, v := range []int32{0, 1, -1, math.MinInt32, math.MaxInt32} {
		w.SetInt32(fmt.Sprintf("i32_%d", v), v)
	}
	for _, v := range []int64{0, 1, -1, math.MinInt64, math.MaxInt64} {
		w.SetInt64(fmt.Sprintf("i64_%d", v), v)
	}
	w.SetUInt32("u32", math.MaxUint32).SetUInt64("u64", math.MaxUint64)
	w.SetFloat32("f32", -2.5).SetFloat64("f64", math.MaxFloat64)
	w.SetString("s_ascii", "hello world")
	w.SetString("s_unicode", "你好,世界🌍 MMKV")
	w.SetString("s_big", strings.Repeat("x", 1<<16))
	w.SetBytes("by", []byte{0, 1, 2, 250, 251, 252})
	w.SetInt32("ow", 111).SetInt32("ow", 222) // overwrite: last wins

	if err := w.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	r, err := Open(dir, id)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer r.Close()

	if v, ok := r.GetBool("b_t"); !ok || v != true {
		t.Errorf("b_t = %v,%v", v, ok)
	}
	if v, ok := r.GetBool("b_f"); !ok || v != false {
		t.Errorf("b_f = %v,%v", v, ok)
	}
	for _, v := range []int32{0, 1, -1, math.MinInt32, math.MaxInt32} {
		if got, ok := r.GetInt32(fmt.Sprintf("i32_%d", v)); !ok || got != v {
			t.Errorf("i32 %d = %d,%v", v, got, ok)
		}
	}
	for _, v := range []int64{0, 1, -1, math.MinInt64, math.MaxInt64} {
		if got, ok := r.GetInt64(fmt.Sprintf("i64_%d", v)); !ok || got != v {
			t.Errorf("i64 %d = %d,%v", v, got, ok)
		}
	}
	if got, ok := r.GetUInt32("u32"); !ok || got != math.MaxUint32 {
		t.Errorf("u32 = %d,%v", got, ok)
	}
	if got, ok := r.GetUInt64("u64"); !ok || got != math.MaxUint64 {
		t.Errorf("u64 = %d,%v", got, ok)
	}
	if got, ok := r.GetFloat32("f32"); !ok || got != -2.5 {
		t.Errorf("f32 = %v,%v", got, ok)
	}
	if got, ok := r.GetFloat64("f64"); !ok || got != math.MaxFloat64 {
		t.Errorf("f64 = %v,%v", got, ok)
	}
	if got, ok := r.GetString("s_ascii"); !ok || got != "hello world" {
		t.Errorf("s_ascii = %q,%v", got, ok)
	}
	if got, ok := r.GetString("s_unicode"); !ok || got != "你好,世界🌍 MMKV" {
		t.Errorf("s_unicode = %q,%v", got, ok)
	}
	if got, ok := r.GetString("s_big"); !ok || got != strings.Repeat("x", 1<<16) {
		t.Errorf("s_big len = %d,%v", len(got), ok)
	}
	if got, ok := r.GetBytes("by"); !ok || !bytes.Equal(got, []byte{0, 1, 2, 250, 251, 252}) {
		t.Errorf("by = % x,%v", got, ok)
	}
	if got, ok := r.GetInt32("ow"); !ok || got != 222 {
		t.Errorf("ow = %d,%v (overwrite last-wins)", got, ok)
	}
}
