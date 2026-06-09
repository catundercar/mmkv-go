//go:build unix

package mmkv

import "testing"

func TestMMKVClearMemoryCacheAndAccessors(t *testing.T) {
	dir := t.TempDir()
	m, err := MMKVWithID(dir, "misc")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()

	if m.MmapID() != "misc" || m.RootDir() != dir {
		t.Errorf("accessors: id=%q root=%q", m.MmapID(), m.RootDir())
	}

	m.SetInt32("x", 99)
	m.Sync()
	m.ClearMemoryCache() // drops in-memory cache; next read reloads from disk
	if v, ok := m.GetInt32("x"); !ok || v != 99 {
		t.Errorf("after ClearMemoryCache, x = %v,%v (should reload from disk)", v, ok)
	}
}

// TestMMKVRemainingGetters covers the interface methods not exercised elsewhere:
// AllKeys, GetStringCopy, GetBytesCopy, Async, Err.
func TestMMKVRemainingGetters(t *testing.T) {
	dir := t.TempDir()
	m, err := MMKVWithID(dir, "rest")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()

	m.SetString("s", "copyme")
	m.SetBytes("b", []byte{1, 2, 3})
	m.SetInt32("i", 1)

	// AllKeys is sorted
	keys := m.AllKeys()
	if len(keys) != 3 || keys[0] != "b" || keys[1] != "i" || keys[2] != "s" {
		t.Errorf("AllKeys = %q, want [b i s]", keys)
	}

	// copy getters return independent values
	if v, ok := m.GetStringCopy("s"); !ok || v != "copyme" {
		t.Errorf("GetStringCopy = %q,%v", v, ok)
	}
	cp, ok := m.GetBytesCopy("b")
	if !ok || len(cp) != 3 {
		t.Fatalf("GetBytesCopy = % x,%v", cp, ok)
	}
	cp[0] = 0xFF // mutating the copy must not affect the store
	if v, _ := m.GetBytes("b"); v[0] != 1 {
		t.Errorf("GetBytesCopy was not independent: store[0]=%d", v[0])
	}

	if err := m.Async(); err != nil { // MS_ASYNC flush
		t.Errorf("Async = %v", err)
	}
	if err := m.Err(); err != nil {
		t.Errorf("Err on a healthy instance = %v", err)
	}
}
