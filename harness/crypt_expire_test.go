//go:build mmkvconfig

// These differential tests use the unified MMKVWithIDAndConfig / Config API,
// which only exists in newer MMKV Go bindings (v2.4.0+). They are gated behind
// the `mmkvconfig` build tag and run only on those versions (see run_cell.sh).
// The on-disk encryption/expiration format is version-stable, and CFB
// correctness is independently checked by the NIST vector in crypt_test.go.
package harness

import (
	"bytes"
	"testing"
	"time"

	mmkv "github.com/catundercar/mmkv-go"
	cgommkv "tencent.com/mmkv"
)

func bigBytes(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i*7 + 1)
	}
	return b
}

// populate writes one value per type kind (+ a 4KB blob to exercise multi-block
// CFB) and returns a verifier asserting a purego Reader reads each identically
// to cgo. Works for plaintext, encrypted, and expire-enabled instances alike —
// the crypt/expire layer is orthogonal to typed decoding.
func populate(w cgommkv.MMKV) func(*testing.T, *mmkv.Reader) {
	w.SetBool(true, "b")
	w.SetInt32(-2147483648, "i32")
	w.SetInt64(9223372036854775807, "i64")
	w.SetUInt32(4294967295, "u32")
	w.SetUInt64(18446744073709551615, "u64")
	w.SetFloat32(3.14159, "f32")
	w.SetFloat64(2.718281828459045, "f64")
	w.SetString("你好,世界🌍 mmkv", "s")
	w.SetBytes([]byte{0, 1, 2, 255, 254}, "by")
	w.SetBytes(bigBytes(4096), "by4k")
	w.Sync(true)

	return func(t *testing.T, r *mmkv.Reader) {
		t.Helper()
		eq := func(name string, a, b any) {
			if a != b {
				t.Fatalf("%s: purego=%v cgo=%v", name, a, b)
			}
		}
		gb, _ := r.GetBool("b")
		eq("bool", gb, w.GetBool("b"))
		g32, _ := r.GetInt32("i32")
		eq("int32", g32, w.GetInt32("i32"))
		g64, _ := r.GetInt64("i64")
		eq("int64", g64, w.GetInt64("i64"))
		gu32, _ := r.GetUInt32("u32")
		eq("uint32", gu32, w.GetUInt32("u32"))
		gu64, _ := r.GetUInt64("u64")
		eq("uint64", gu64, w.GetUInt64("u64"))
		gf32, _ := r.GetFloat32("f32")
		eq("float32", gf32, w.GetFloat32("f32"))
		gf64, _ := r.GetFloat64("f64")
		eq("float64", gf64, w.GetFloat64("f64"))
		gs, _ := r.GetString("s")
		eq("string", gs, w.GetString("s"))
		if gby, _ := r.GetBytes("by"); !bytes.Equal(gby, w.GetBytes("by")) {
			t.Fatalf("bytes mismatch")
		}
		if g4k, _ := r.GetBytes("by4k"); !bytes.Equal(g4k, w.GetBytes("by4k")) {
			t.Fatalf("bytes 4k mismatch: purego len=%d cgo len=%d", len(g4k), len(w.GetBytes("by4k")))
		}
	}
}

func TestEncryptedAES128(t *testing.T) {
	dir := ensureInit(t)
	key := []byte("0123456789abcdef") // 16 bytes -> AES-128
	w := cgommkv.MMKVWithIDAndConfig("enc128", cgommkv.Config{
		Encryption: &cgommkv.EncryptionConfig{Key: key},
	})
	w.ClearAll()
	verify := populate(w)
	r, err := mmkv.Open(dir, "enc128", mmkv.WithEncryption(key))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer r.Close()
	verify(t, r)
	t.Log("AES-128: cgo ≡ purego")
}

func TestEncryptedAES256(t *testing.T) {
	dir := ensureInit(t)
	key := []byte("0123456789abcdef0123456789abcdef") // 32 bytes -> AES-256
	w := cgommkv.MMKVWithIDAndConfig("enc256", cgommkv.Config{
		Encryption: &cgommkv.EncryptionConfig{Key: key, AES256: true},
	})
	w.ClearAll()
	verify := populate(w)
	r, err := mmkv.Open(dir, "enc256", mmkv.WithEncryption(key))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer r.Close()
	verify(t, r)
	t.Log("AES-256: cgo ≡ purego")
}

// TestExpireNever: every value carries a trailing expire timestamp of 0 (never).
// purego must strip the 4 bytes and read each value identically to cgo.
func TestExpireNever(t *testing.T) {
	dir := ensureInit(t)
	w := cgommkv.MMKVWithIDAndConfig("expnever", cgommkv.Config{
		Expiration: &cgommkv.ExpirationConfig{Enabled: true, ExpiredInSeconds: 0},
	})
	w.ClearAll()
	verify := populate(w)
	r, err := mmkv.Open(dir, "expnever")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer r.Close()
	verify(t, r)
	t.Log("expire(never): cgo ≡ purego (4-byte suffix stripped)")
}

// TestExpireActual: a key with a short TTL must read as absent in BOTH cgo and
// purego after it expires; a never-expire key must remain.
func TestExpireActual(t *testing.T) {
	dir := ensureInit(t)
	w := cgommkv.MMKVWithIDAndConfig("expnow", cgommkv.Config{
		Expiration: &cgommkv.ExpirationConfig{Enabled: true},
	})
	w.ClearAll()
	w.SetInt32(111, "keep")          // default duration 0 -> never expires
	w.SetInt32Expire(222, "gone", 2) // expires in 2s
	w.Sync(true)

	r, err := mmkv.Open(dir, "expnow")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer r.Close()
	if g, ok := r.GetInt32("keep"); !ok || g != 111 {
		t.Fatalf("keep before expiry: %v %v", g, ok)
	}

	time.Sleep(3 * time.Second)

	// cgo: expired key reads as the default (absent); keep remains.
	if w.GetInt32WithDefault("gone", -1) != -1 {
		t.Fatal("cgo: 'gone' should be expired")
	}
	if w.GetInt32WithDefault("keep", -1) != 111 {
		t.Fatal("cgo: 'keep' should remain")
	}
	// purego: same verdict.
	if r.Contains("gone") {
		t.Fatal("purego: 'gone' should be expired")
	}
	if g, ok := r.GetInt32("keep"); !ok || g != 111 {
		t.Fatalf("purego: 'keep' should remain: %v %v", g, ok)
	}
	t.Log("expire(actual): cgo ≡ purego (expired key absent in both)")
}
