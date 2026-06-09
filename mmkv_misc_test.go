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
