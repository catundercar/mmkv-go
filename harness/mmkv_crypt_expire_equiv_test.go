//go:build mmkvconfig

// Bidirectional encrypted/expire differential for the live read+write MMKV type
// against the cgo unified-config API (v2.4.0+). Gated `mmkvconfig` like
// crypt_expire_test.go; reuses bigBytes from that file.
package harness

import (
	"bytes"
	"testing"

	mmkv "github.com/catundercar/mmkv-go"
	cgommkv "tencent.com/mmkv"
)

// TestMMKVEncWriteCgoReads: pure-Go encrypted writes → C++ reads with the key.
func TestMMKVEncWriteCgoReads(t *testing.T) {
	dir := ensureInit(t)
	key := []byte("0123456789abcdef") // AES-128

	m, err := mmkv.MMKVWithID(dir, "mmkv_enc_w", mmkv.WithCryptKey(key))
	if err != nil {
		t.Fatal(err)
	}
	m.ClearAll()
	m.SetInt32("i", -42)
	m.SetUInt64("u", 18446744073709551615)
	m.SetString("s", "secret 机密🔒")
	m.SetBytes("b4k", bigBytes(4096)) // multi-block CFB
	if err := m.Sync(); err != nil {
		t.Fatal(err)
	}
	m.Close()

	r := cgommkv.MMKVWithIDAndConfig("mmkv_enc_w", cgommkv.Config{
		Encryption: &cgommkv.EncryptionConfig{Key: key},
	})
	if r.GetInt32("i") != -42 {
		t.Errorf("i = %d", r.GetInt32("i"))
	}
	if r.GetUInt64("u") != 18446744073709551615 {
		t.Errorf("u = %d", r.GetUInt64("u"))
	}
	if r.GetString("s") != "secret 机密🔒" {
		t.Errorf("s = %q", r.GetString("s"))
	}
	if !bytes.Equal(r.GetBytes("b4k"), bigBytes(4096)) {
		t.Errorf("b4k mismatch")
	}
}

// TestCgoEncWriteMMKVReads: C++ encrypted writes → pure-Go MMKV reads.
func TestCgoEncWriteMMKVReads(t *testing.T) {
	dir := ensureInit(t)
	key := []byte("0123456789abcdef0123456789abcdef") // AES-256

	w := cgommkv.MMKVWithIDAndConfig("cgo_enc_w", cgommkv.Config{
		Encryption: &cgommkv.EncryptionConfig{Key: key, AES256: true},
	})
	w.ClearAll()
	w.SetInt32(-42, "i")
	w.SetString("secret 机密🔒", "s")
	w.SetBytes(bigBytes(4096), "b4k")
	w.Sync(true)

	m, err := mmkv.MMKVWithID(dir, "cgo_enc_w", mmkv.WithCryptKey(key))
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if v, ok := m.GetInt32("i"); !ok || v != -42 {
		t.Errorf("i = %v,%v", v, ok)
	}
	if v, ok := m.GetString("s"); !ok || v != "secret 机密🔒" {
		t.Errorf("s = %q,%v", v, ok)
	}
	if v, ok := m.GetBytes("b4k"); !ok || !bytes.Equal(v, bigBytes(4096)) {
		t.Errorf("b4k mismatch ok=%v", ok)
	}
}

// TestMMKVExpireWriteCgoReads: pure-Go expire-enabled writes → C++ reads
// (timestamps stripped on both sides).
func TestMMKVExpireWriteCgoReads(t *testing.T) {
	dir := ensureInit(t)
	m, err := mmkv.MMKVWithID(dir, "mmkv_exp_w")
	if err != nil {
		t.Fatal(err)
	}
	m.ClearAll()
	if err := m.EnableAutoKeyExpire(mmkv.ExpireNever); err != nil {
		t.Fatal(err)
	}
	m.SetInt32("i", 7)
	m.SetString("s", "hi 世界")
	m.SetBytes("b", []byte{9, 8, 7})
	if err := m.Sync(); err != nil {
		t.Fatal(err)
	}
	m.Close()

	r := cgommkv.MMKVWithIDAndConfig("mmkv_exp_w", cgommkv.Config{
		Expiration: &cgommkv.ExpirationConfig{Enabled: true, ExpiredInSeconds: 0},
	})
	if r.GetInt32WithDefault("i", -1) != 7 {
		t.Errorf("i = %d", r.GetInt32WithDefault("i", -1))
	}
	if r.GetString("s") != "hi 世界" {
		t.Errorf("s = %q", r.GetString("s"))
	}
	if !bytes.Equal(r.GetBytes("b"), []byte{9, 8, 7}) {
		t.Errorf("b = % x", r.GetBytes("b"))
	}
}
