package mmkv

import (
	"os"
	"path/filepath"
	"testing"
)

// TestBackupOne backs up the fixture, then opens the backup and asserts it is a
// valid snapshot identical to the original (same differential oracle).
func TestBackupOne(t *testing.T) {
	if _, err := os.Stat("testdata/plain.crc"); err != nil {
		t.Skipf("no fixture: %v", err)
	}
	dst := t.TempDir()
	if err := BackupOne("testdata", "plain", dst); err != nil {
		t.Fatalf("BackupOne: %v", err)
	}
	for _, name := range []string{"plain", "plain.crc"} {
		if _, err := os.Stat(filepath.Join(dst, name)); err != nil {
			t.Fatalf("backup missing %s: %v", name, err)
		}
	}
	r, err := Open(dst, "plain")
	if err != nil {
		t.Fatalf("open backup: %v", err)
	}
	defer r.Close()
	verifyEntries(t, r, "testdata/expected.json")
}
