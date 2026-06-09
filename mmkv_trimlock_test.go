//go:build unix

package mmkv

import (
	"fmt"
	"strings"
	"testing"
)

func TestMMKVTrim(t *testing.T) {
	dir := t.TempDir()
	m, err := MMKVWithID(dir, "trim")
	if err != nil {
		t.Fatal(err)
	}

	val := strings.Repeat("x", 100)
	for i := 0; i < 500; i++ {
		m.SetString(fmt.Sprintf("k%d", i), val)
	}
	for i := 0; i < 480; i++ {
		m.RemoveValueForKey(fmt.Sprintf("k%d", i))
	}
	big := m.TotalSize()

	if err := m.Trim(); err != nil {
		t.Fatal(err)
	}
	if m.TotalSize() >= big {
		t.Errorf("Trim did not shrink: %d -> %d", big, m.TotalSize())
	}
	if m.Count() != 20 {
		t.Errorf("count after trim = %d, want 20", m.Count())
	}
	for i := 480; i < 500; i++ {
		if v, ok := m.GetString(fmt.Sprintf("k%d", i)); !ok || v != val {
			t.Fatalf("k%d lost after trim ok=%v", i, ok)
		}
	}

	// survives reopen
	m.Close()
	m2, err := MMKVWithID(dir, "trim")
	if err != nil {
		t.Fatal(err)
	}
	defer m2.Close()
	if m2.Count() != 20 {
		t.Errorf("count after reopen = %d, want 20", m2.Count())
	}
	if v, ok := m2.GetString("k499"); !ok || v != val {
		t.Errorf("k499 after reopen ok=%v", ok)
	}
}

// TestMMKVLockNoDeadlock verifies the public lock does not deadlock with Get/Set
// inside the critical section (the flock re-enters by reference count).
func TestMMKVLockNoDeadlock(t *testing.T) {
	dir := t.TempDir()
	m, err := MMKVWithID(dir, "lk", WithMultiProcess())
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()

	m.Lock()
	if err := m.SetInt32("x", 1); err != nil { // nested write under the held lock
		t.Fatal(err)
	}
	if v, ok := m.GetInt32("x"); !ok || v != 1 { // nested read
		t.Fatalf("read under lock = %v,%v", v, ok)
	}
	m.Unlock()

	if !m.TryLock() {
		t.Fatal("TryLock should succeed when uncontended")
	}
	m.Unlock()

	// single-process: Lock/TryLock are no-ops but must not panic/deadlock
	sp, err := MMKVWithID(t.TempDir(), "sp")
	if err != nil {
		t.Fatal(err)
	}
	defer sp.Close()
	sp.Lock()
	sp.SetInt32("y", 2)
	sp.Unlock()
	if !sp.TryLock() {
		t.Fatal("single-process TryLock should return true")
	}
	sp.Unlock()
}
