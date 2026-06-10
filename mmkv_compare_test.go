//go:build unix

package mmkv

import "testing"

func TestMMKVCompareBeforeSet(t *testing.T) {
	dir := t.TempDir()
	m, err := MMKVWithID(dir, "cbs")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()

	// A second key keeps sets on the append path: with a single key the
	// override fast path rewrites in place and ActualSize() stops being a
	// usable "did it write" probe (same-size rewrites don't grow the region).
	m.SetString("pad", "x")
	m.SetString("k", "value")
	m.EnableCompareBeforeSet()
	if !m.IsCompareBeforeSetEnabled() {
		t.Fatal("not enabled")
	}

	before := m.ActualSize()
	// setting the SAME value must be a no-op (no growth)
	for i := 0; i < 100; i++ {
		m.SetString("k", "value")
	}
	if m.ActualSize() != before {
		t.Errorf("redundant sets grew the store: %d -> %d", before, m.ActualSize())
	}
	if v, ok := m.GetString("k"); !ok || v != "value" {
		t.Errorf("k = %q,%v", v, ok)
	}

	// a DIFFERENT value must still be written
	m.SetString("k", "changed")
	if m.ActualSize() == before {
		t.Errorf("changed value was not written")
	}
	if v, ok := m.GetString("k"); !ok || v != "changed" {
		t.Errorf("k after change = %q,%v", v, ok)
	}

	// disable → redundant set writes again
	m.DisableCompareBeforeSet()
	sz := m.ActualSize()
	m.SetString("k", "changed")
	if m.ActualSize() == sz {
		t.Errorf("disabled compareBeforeSet still skipped the write")
	}
}

// TestMMKVCompareBeforeSetExpireExclusive: enabling expiration disables
// compareBeforeSet, and enabling compareBeforeSet under expiration is a no-op.
func TestMMKVCompareBeforeSetExpireExclusive(t *testing.T) {
	dir := t.TempDir()
	m, err := MMKVWithID(dir, "cbsx")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()

	m.EnableCompareBeforeSet()
	if err := m.EnableAutoKeyExpire(ExpireNever); err != nil {
		t.Fatal(err)
	}
	if m.IsCompareBeforeSetEnabled() {
		t.Error("compareBeforeSet should be off after enabling expiration")
	}
	m.EnableCompareBeforeSet()
	if m.IsCompareBeforeSetEnabled() {
		t.Error("compareBeforeSet should be a no-op while expiration is on")
	}
}
