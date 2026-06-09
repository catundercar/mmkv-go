//go:build unix

package mmkv

import (
	"path/filepath"
	"testing"
)

func TestMMKVFileUtilsAndNamespace(t *testing.T) {
	root := t.TempDir()

	if CheckExist(root, "none") {
		t.Error("CheckExist true for missing instance")
	}
	if IsFileValid(root, "none") {
		t.Error("IsFileValid true for missing instance")
	}

	ns := OpenNameSpace(root)
	m, err := ns.MMKVWithID("nsid")
	if err != nil {
		t.Fatal(err)
	}
	m.SetInt32("x", 5)
	m.Sync()

	if !CheckExist(root, "nsid") {
		t.Error("CheckExist false after create")
	}
	if !IsFileValid(root, "nsid") {
		t.Error("IsFileValid false for a valid instance")
	}
	m.Close()

	// RemoveStorage deletes both files
	if err := RemoveStorage(root, "nsid"); err != nil {
		t.Fatal(err)
	}
	if CheckExist(root, "nsid") {
		t.Error("CheckExist true after RemoveStorage")
	}
}

func TestMMKVBackupRestore(t *testing.T) {
	dir := t.TempDir()
	backup := filepath.Join(t.TempDir(), "bak")
	const id = "br"

	m, err := MMKVWithID(dir, id)
	if err != nil {
		t.Fatal(err)
	}
	m.SetInt32("i", 1)
	m.SetString("s", "v1")
	m.Sync()
	m.Close()

	if err := BackupOne(dir, id, backup); err != nil {
		t.Fatal(err)
	}

	// mutate the live file
	m, err = MMKVWithID(dir, id)
	if err != nil {
		t.Fatal(err)
	}
	m.SetInt32("i", 2)
	m.SetString("s", "v2")
	m.SetInt32("extra", 99)
	m.Sync()
	m.Close()

	// restore over it
	if err := RestoreOneFromDirectory(dir, id, backup); err != nil {
		t.Fatal(err)
	}
	m, err = MMKVWithID(dir, id)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if v, ok := m.GetInt32("i"); !ok || v != 1 {
		t.Errorf("i after restore = %v,%v, want 1", v, ok)
	}
	if v, ok := m.GetString("s"); !ok || v != "v1" {
		t.Errorf("s after restore = %q,%v, want v1", v, ok)
	}
	if m.Contains("extra") {
		t.Errorf("'extra' present after restore (should be gone)")
	}
}

// TestMMKVRestoreReloadsLiveInstance checks a cached instance picks up a restore
// on its next call (the needLoad remap+reload path).
func TestMMKVRestoreReloadsLiveInstance(t *testing.T) {
	dir := t.TempDir()
	backup := filepath.Join(t.TempDir(), "bak")
	const id = "live"

	m, err := MMKVWithID(dir, id)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	m.SetString("k", "original")
	m.Sync()
	if err := BackupOne(dir, id, backup); err != nil {
		t.Fatal(err)
	}

	m.SetString("k", "changed")
	if v, _ := m.GetString("k"); v != "changed" {
		t.Fatalf("pre-restore k = %q", v)
	}

	// restore while the instance is open (not concurrently accessed)
	if err := RestoreOneFromDirectory(dir, id, backup); err != nil {
		t.Fatal(err)
	}
	if v, ok := m.GetString("k"); !ok || v != "original" {
		t.Errorf("k after restore = %q,%v, want original (live reload)", v, ok)
	}
}
