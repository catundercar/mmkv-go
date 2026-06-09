//go:build unix

package mmkv

import (
	"testing"
	"time"
)

func TestMMKVExpiration(t *testing.T) {
	dir := t.TempDir()
	m, err := MMKVWithID(dir, "exp")
	if err != nil {
		t.Fatal(err)
	}

	// populate before enabling
	m.SetInt32("keep", 7)
	m.SetString("s", "hi")

	// enable with ExpireNever: existing keys gain a never-timestamp and stay
	if err := m.EnableAutoKeyExpire(ExpireNever); err != nil {
		t.Fatal(err)
	}
	if !m.IsExpirationEnabled() {
		t.Fatal("expiration not enabled")
	}
	if v, ok := m.GetInt32("keep"); !ok || v != 7 {
		t.Errorf("keep after enable = %v,%v", v, ok)
	}
	if v, ok := m.GetString("s"); !ok || v != "hi" {
		t.Errorf("s after enable = %q,%v", v, ok)
	}
	m.SetInt32("n", 1)
	if v, ok := m.GetInt32("n"); !ok || v != 1 {
		t.Errorf("n under never = %v,%v", v, ok)
	}

	// flag + values persist across reopen
	m.Close()
	m, err = MMKVWithID(dir, "exp")
	if err != nil {
		t.Fatal(err)
	}
	if !m.IsExpirationEnabled() {
		t.Fatal("expiration flag not persisted")
	}
	if v, ok := m.GetInt32("keep"); !ok || v != 7 {
		t.Errorf("keep after reopen = %v,%v", v, ok)
	}

	// actual expiry: 1s duration, then a key should disappear after it elapses
	if err := m.EnableAutoKeyExpire(1); err != nil {
		t.Fatal(err)
	}
	m.SetInt32("tmp", 9)
	if v, ok := m.GetInt32("tmp"); !ok || v != 9 {
		t.Fatalf("tmp before expiry = %v,%v", v, ok)
	}
	time.Sleep(1100 * time.Millisecond)
	if _, ok := m.GetInt32("tmp"); ok {
		t.Errorf("tmp should have expired")
	}
	if m.Contains("tmp") {
		t.Errorf("Contains(tmp) true after expiry")
	}
	// "keep" was written with a never-timestamp, so it survives
	if v, ok := m.GetInt32("keep"); !ok || v != 7 {
		t.Errorf("keep wrongly expired = %v,%v", v, ok)
	}

	// disable: surviving keys remain, format reverts (timestamp stripped)
	if err := m.DisableAutoKeyExpire(); err != nil {
		t.Fatal(err)
	}
	if m.IsExpirationEnabled() {
		t.Fatal("still enabled after disable")
	}
	if v, ok := m.GetInt32("keep"); !ok || v != 7 {
		t.Errorf("keep after disable = %v,%v", v, ok)
	}
	m.Close()
	m, err = MMKVWithID(dir, "exp")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if m.IsExpirationEnabled() {
		t.Error("expiration re-enabled after reopen post-disable")
	}
	if v, ok := m.GetInt32("keep"); !ok || v != 7 {
		t.Errorf("keep after disable+reopen = %v,%v", v, ok)
	}
}

// TestMMKVExpireReadableByReader cross-checks that an expiring file the MMKV
// writer produces is decoded correctly by the (C++-validated) read-only Reader.
func TestMMKVExpireReadableByReader(t *testing.T) {
	dir := t.TempDir()
	m, err := MMKVWithID(dir, "expr")
	if err != nil {
		t.Fatal(err)
	}
	m.EnableAutoKeyExpire(ExpireNever)
	m.SetInt32("i", 42)
	m.SetString("s", "héllo")
	if err := m.Sync(); err != nil {
		t.Fatal(err)
	}
	m.Close()

	r, err := Open(dir, "expr")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if v, ok := r.GetInt32("i"); !ok || v != 42 {
		t.Errorf("reader i = %v,%v", v, ok)
	}
	if v, ok := r.GetString("s"); !ok || v != "héllo" {
		t.Errorf("reader s = %q,%v", v, ok)
	}
}
