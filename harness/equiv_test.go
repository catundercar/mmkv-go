package harness

import (
	"bytes"
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/catundercar/mmkv-go"
	cgommkv "tencent.com/mmkv"
)

// TestCgoEqualsPurego is the production correctness gate: for a file written by
// the official cgo library, every value read back via cgo MUST equal the value
// read via puremmkv (pure Go). Run per MMKV version × arch in CI — a format
// change in any MMKV release that breaks the pure-Go reader turns this red.
//
// It also sanity-checks each read against the value originally set, so a bug
// shared by both readers can't hide.
func TestCgoEqualsPurego(t *testing.T) {
	dir := ensureInit(t)
	const id = "equiv"
	w := cgommkv.MMKVWithID(id)
	w.ClearAll()

	// helpers: write via cgo, then assert cgo-read == pure-read (== expected).
	var checks []func(t *testing.T, r *mmkv.Reader)

	addBool := func(key string, v bool) {
		w.SetBool(v, key)
		checks = append(checks, func(t *testing.T, r *mmkv.Reader) {
			c := w.GetBool(key)
			p, ok := r.GetBool(key)
			if !ok || c != p || c != v {
				t.Fatalf("bool %q: set=%v cgo=%v pure=%v ok=%v", key, v, c, p, ok)
			}
		})
	}
	addI32 := func(key string, v int32) {
		w.SetInt32(v, key)
		checks = append(checks, func(t *testing.T, r *mmkv.Reader) {
			c := w.GetInt32(key)
			p, ok := r.GetInt32(key)
			if !ok || c != p || c != v {
				t.Fatalf("int32 %q: set=%d cgo=%d pure=%d ok=%v", key, v, c, p, ok)
			}
		})
	}
	addI64 := func(key string, v int64) {
		w.SetInt64(v, key)
		checks = append(checks, func(t *testing.T, r *mmkv.Reader) {
			c := w.GetInt64(key)
			p, ok := r.GetInt64(key)
			if !ok || c != p || c != v {
				t.Fatalf("int64 %q: set=%d cgo=%d pure=%d ok=%v", key, v, c, p, ok)
			}
		})
	}
	addU32 := func(key string, v uint32) {
		w.SetUInt32(v, key)
		checks = append(checks, func(t *testing.T, r *mmkv.Reader) {
			c := w.GetUInt32(key)
			p, ok := r.GetUInt32(key)
			if !ok || c != p || c != v {
				t.Fatalf("uint32 %q: set=%d cgo=%d pure=%d ok=%v", key, v, c, p, ok)
			}
		})
	}
	addU64 := func(key string, v uint64) {
		w.SetUInt64(v, key)
		checks = append(checks, func(t *testing.T, r *mmkv.Reader) {
			c := w.GetUInt64(key)
			p, ok := r.GetUInt64(key)
			if !ok || c != p || c != v {
				t.Fatalf("uint64 %q: set=%d cgo=%d pure=%d ok=%v", key, v, c, p, ok)
			}
		})
	}
	addF32 := func(key string, v float32) {
		w.SetFloat32(v, key)
		checks = append(checks, func(t *testing.T, r *mmkv.Reader) {
			c := w.GetFloat32(key)
			p, ok := r.GetFloat32(key)
			if !ok || !eqF32(c, p) || !eqF32(c, v) {
				t.Fatalf("float32 %q: set=%v cgo=%v pure=%v ok=%v", key, v, c, p, ok)
			}
		})
	}
	addF64 := func(key string, v float64) {
		w.SetFloat64(v, key)
		checks = append(checks, func(t *testing.T, r *mmkv.Reader) {
			c := w.GetFloat64(key)
			p, ok := r.GetFloat64(key)
			if !ok || !eqF64(c, p) || !eqF64(c, v) {
				t.Fatalf("float64 %q: set=%v cgo=%v pure=%v ok=%v", key, v, c, p, ok)
			}
		})
	}
	addStr := func(key, v string) {
		w.SetString(v, key)
		checks = append(checks, func(t *testing.T, r *mmkv.Reader) {
			c := w.GetString(key)
			p, ok := r.GetString(key)
			if !ok || c != p || c != v {
				t.Fatalf("string %q (len %d): cgo==pure? %v, cgo==set? %v, ok=%v", key, len(v), c == p, c == v, ok)
			}
		})
	}
	addBytes := func(key string, v []byte) {
		w.SetBytes(v, key)
		checks = append(checks, func(t *testing.T, r *mmkv.Reader) {
			c := w.GetBytes(key)
			p, ok := r.GetBytes(key)
			if !ok || !bytes.Equal(c, p) || !bytes.Equal(c, v) {
				t.Fatalf("bytes %q (len %d): cgo(len=%d)==pure(len=%d)? %v, ==set? %v, ok=%v",
					key, len(v), len(c), len(p), bytes.Equal(c, p), bytes.Equal(c, v), ok)
			}
		})
	}
	addAbsent := func(key string) {
		checks = append(checks, func(t *testing.T, r *mmkv.Reader) {
			if r.Contains(key) {
				t.Fatalf("absent %q: purego still contains it", key)
			}
			if c := w.GetBytes(key); len(c) != 0 {
				t.Fatalf("absent %q: cgo returned %d bytes", key, len(c))
			}
		})
	}

	// ---- scalars incl. boundary values ----
	addBool("b_t", true)
	addBool("b_f", false)
	for _, v := range []int32{0, 1, -1, math.MinInt32, math.MaxInt32} {
		addI32(fmt.Sprintf("i32_%d", v), v)
	}
	for _, v := range []int64{0, 1, -1, math.MinInt64, math.MaxInt64} {
		addI64(fmt.Sprintf("i64_%d", v), v)
	}
	for _, v := range []uint32{0, 1, math.MaxUint32} {
		addU32(fmt.Sprintf("u32_%d", v), v)
	}
	for _, v := range []uint64{0, 1, math.MaxUint64} {
		addU64(fmt.Sprintf("u64_%d", v), v)
	}
	for i, v := range []float32{0, 1.5, -2.5, math.MaxFloat32, math.SmallestNonzeroFloat32, float32(math.Inf(1))} {
		addF32(fmt.Sprintf("f32_%d", i), v)
	}
	for i, v := range []float64{0, 1.5, -2.5, math.MaxFloat64, math.SmallestNonzeroFloat64, math.Inf(-1)} {
		addF64(fmt.Sprintf("f64_%d", i), v)
	}

	// ---- strings ----
	addStr("s_ascii", "hello world")
	addStr("s_unicode", "你好,世界🌍 MMKV")
	addStr("s_big", strings.Repeat("x", 1<<20)) // 1MB

	// ---- bytes across sizes ----
	for _, n := range []int{1, 16, 256, 4096, 65536, 1 << 20} {
		b := make([]byte, n)
		for i := range b {
			b[i] = byte(i*31 + 7)
		}
		addBytes(fmt.Sprintf("by_%d", n), b)
	}

	// ---- overwrite (last write wins) ----
	w.SetInt32(111, "ow")
	addI32("ow", 222) // addI32 sets 222 last → expect 222

	// ---- delete -> absent in both readers ----
	w.SetInt32(7, "del")
	w.RemoveKey("del")
	addAbsent("del")

	w.Sync(true)

	r, err := mmkv.Open(dir, id)
	if err != nil {
		t.Fatalf("purego Open: %v", err)
	}
	defer r.Close()

	for i, chk := range checks {
		t.Run(fmt.Sprintf("case_%d", i), func(t *testing.T) { chk(t, r) })
	}
	t.Logf("cgo≡purego verified across %d cases", len(checks))
}

func eqF32(a, b float32) bool {
	if math.IsNaN(float64(a)) && math.IsNaN(float64(b)) {
		return true
	}
	return a == b
}

func eqF64(a, b float64) bool {
	if math.IsNaN(a) && math.IsNaN(b) {
		return true
	}
	return a == b
}
