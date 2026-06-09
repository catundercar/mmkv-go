//go:build unix

package mmkv

import (
	"bytes"
	"math"
	"testing"
)

func TestMMKVEncryptedRoundTrip(t *testing.T) {
	dir := t.TempDir()
	key := []byte("0123456789abcdef") // 16 bytes → AES-128

	m, err := MMKVWithID(dir, "enc", WithCryptKey(key))
	if err != nil {
		t.Fatal(err)
	}
	m.SetInt32("i", -42)
	m.SetString("s", "secret 机密🔒")
	m.SetBytes("b", []byte{1, 2, 3, 4, 5})
	m.SetUInt64("u", math.MaxUint64)
	if err := m.Sync(); err != nil {
		t.Fatal(err)
	}

	// the on-disk region must NOT be plaintext (encryption actually happened)
	if v, ok := m.GetString("s"); !ok || v != "secret 机密🔒" {
		t.Fatalf("in-memory s = %q,%v", v, ok)
	}
	raw := m.data.memory()[4 : 4+m.actualSize]
	if bytes.Contains(raw, []byte("secret")) {
		t.Errorf("plaintext leaked into the encrypted region")
	}
	m.Close()

	// reopen with the key → values decrypt
	m2, err := MMKVWithID(dir, "enc", WithCryptKey(key))
	if err != nil {
		t.Fatal(err)
	}
	defer m2.Close()
	if v, ok := m2.GetInt32("i"); !ok || v != -42 {
		t.Errorf("i = %v,%v", v, ok)
	}
	if v, ok := m2.GetString("s"); !ok || v != "secret 机密🔒" {
		t.Errorf("s = %q,%v", v, ok)
	}
	if v, ok := m2.GetBytes("b"); !ok || !bytes.Equal(v, []byte{1, 2, 3, 4, 5}) {
		t.Errorf("b = % x,%v", v, ok)
	}
	if v, ok := m2.GetUInt64("u"); !ok || v != math.MaxUint64 {
		t.Errorf("u = %v,%v", v, ok)
	}
}

func TestMMKVEncryptedAES256(t *testing.T) {
	dir := t.TempDir()
	key := []byte("0123456789abcdef0123456789abcdef") // 32 bytes → AES-256
	m, err := MMKVWithID(dir, "enc256", WithCryptKey(key))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 50; i++ { // multiple sets → multiple encrypted full write-backs
		m.SetString("k"+string(rune('a'+i%26)), "value-with-some-length")
	}
	m.SetInt32("x", 7)
	m.Sync()
	m.Close()

	m2, err := MMKVWithID(dir, "enc256", WithCryptKey(key))
	if err != nil {
		t.Fatal(err)
	}
	defer m2.Close()
	if v, ok := m2.GetInt32("x"); !ok || v != 7 {
		t.Errorf("x = %v,%v", v, ok)
	}
	if v, ok := m2.GetString("k" + string(rune('a'))); !ok || v != "value-with-some-length" {
		t.Errorf("ka = %q,%v", v, ok)
	}
}

func TestMMKVReKey(t *testing.T) {
	dir := t.TempDir()

	// start plaintext
	m, err := MMKVWithID(dir, "rk")
	if err != nil {
		t.Fatal(err)
	}
	m.SetInt32("i", 100)
	m.SetString("s", "data")

	// plaintext → encrypted
	key1 := []byte("key-one-16-bytes")
	if err := m.ReKey(key1); err != nil {
		t.Fatal(err)
	}
	if !m.IsEncryptionEnabled() {
		t.Fatal("not encrypted after ReKey")
	}
	if v, ok := m.GetInt32("i"); !ok || v != 100 {
		t.Errorf("i after encrypt = %v,%v", v, ok)
	}
	raw := m.data.memory()[4 : 4+m.actualSize]
	if bytes.Contains(raw, []byte("data")) {
		t.Errorf("plaintext leaked after ReKey to encrypted")
	}

	// change key
	key2 := []byte("key-two-32-bytes-key-two-32-byte!") // 33 → AES-256
	if err := m.ReKey(key2); err != nil {
		t.Fatal(err)
	}
	if v, ok := m.GetString("s"); !ok || v != "data" {
		t.Errorf("s after key change = %q,%v", v, ok)
	}

	// encrypted → plaintext
	if err := m.ReKey(nil); err != nil {
		t.Fatal(err)
	}
	if m.IsEncryptionEnabled() {
		t.Fatal("still encrypted after ReKey(nil)")
	}
	if v, ok := m.GetInt32("i"); !ok || v != 100 {
		t.Errorf("i after decrypt = %v,%v", v, ok)
	}
	m.Close()

	// reopen plaintext (no key) → readable
	m2, err := MMKVWithID(dir, "rk")
	if err != nil {
		t.Fatal(err)
	}
	defer m2.Close()
	if v, ok := m2.GetString("s"); !ok || v != "data" {
		t.Errorf("s after decrypt+reopen = %q,%v", v, ok)
	}
}
