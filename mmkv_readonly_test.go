//go:build unix

package mmkv

import (
	"errors"
	"testing"
)

func TestMMKVReadOnly(t *testing.T) {
	dir := t.TempDir()
	const id = "ro"

	// write some data with a normal instance
	w, err := MMKVWithID(dir, id)
	if err != nil {
		t.Fatal(err)
	}
	w.SetInt32("i", 42)
	w.SetString("s", "hello")
	w.Sync()
	w.Close()

	// open read-only
	r, err := MMKVWithID(dir, id, WithReadOnly())
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	// reads work
	if v, ok := r.GetInt32("i"); !ok || v != 42 {
		t.Errorf("i = %v,%v", v, ok)
	}
	if v, ok := r.GetString("s"); !ok || v != "hello" {
		t.Errorf("s = %q,%v", v, ok)
	}

	// every mutator returns ErrReadOnly
	if err := r.SetInt32("i", 7); !errors.Is(err, ErrReadOnly) {
		t.Errorf("SetInt32 on RO = %v, want ErrReadOnly", err)
	}
	if err := r.SetString("x", "y"); !errors.Is(err, ErrReadOnly) {
		t.Errorf("SetString on RO = %v", err)
	}
	if err := r.RemoveValueForKey("i"); !errors.Is(err, ErrReadOnly) {
		t.Errorf("Remove on RO = %v", err)
	}
	if err := r.RemoveValuesForKeys([]string{"i"}); !errors.Is(err, ErrReadOnly) {
		t.Errorf("RemoveValues on RO = %v", err)
	}
	if err := r.ClearAll(); !errors.Is(err, ErrReadOnly) {
		t.Errorf("ClearAll on RO = %v", err)
	}
	if err := r.Trim(); !errors.Is(err, ErrReadOnly) {
		t.Errorf("Trim on RO = %v", err)
	}
	if err := r.EnableAutoKeyExpire(0); !errors.Is(err, ErrReadOnly) {
		t.Errorf("EnableAutoKeyExpire on RO = %v", err)
	}
	if err := r.ReKey([]byte("0123456789abcdef")); !errors.Is(err, ErrReadOnly) {
		t.Errorf("ReKey on RO = %v", err)
	}
	if err := r.Sync(); err != nil { // Sync is a no-op, not an error
		t.Errorf("Sync on RO = %v, want nil", err)
	}

	// data unchanged after the rejected writes
	if v, ok := r.GetInt32("i"); !ok || v != 42 {
		t.Errorf("i changed despite RO: %v,%v", v, ok)
	}

	// opening read-only a missing instance fails
	if _, err := MMKVWithID(t.TempDir(), "missing", WithReadOnly()); err == nil {
		t.Error("read-only open of a missing file should fail")
	}
}
