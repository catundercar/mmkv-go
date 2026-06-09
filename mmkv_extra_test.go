//go:build unix

package mmkv

import (
	"bytes"
	"reflect"
	"testing"
)

func TestStringSliceBlobLayout(t *testing.T) {
	// ["a","bc"] -> items = [01 'a' 02 'b' 'c'] (len 5); blob = writeData(items)
	// = [05 01 61 02 62 63]. Pins the wire layout against the C++ spec.
	got := stringSliceBlob([]string{"a", "bc"})
	want := []byte{0x05, 0x01, 0x61, 0x02, 0x62, 0x63}
	if !bytes.Equal(got, want) {
		t.Fatalf("stringSliceBlob = % x, want % x", got, want)
	}
}

func TestMMKVStringSliceRoundTrip(t *testing.T) {
	dir := t.TempDir()
	m, err := MMKVWithID(dir, "vec")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()

	cases := [][]string{
		{},
		{""},
		{"a"},
		{"alpha", "", "你好🌍", "with space", "z"},
	}
	for i, in := range cases {
		key := "k" + string(rune('0'+i))
		if err := m.SetStringSlice(key, in); err != nil {
			t.Fatal(err)
		}
		got, ok := m.GetStringSlice(key)
		if !ok {
			t.Fatalf("case %d: not ok", i)
		}
		// normalize nil vs empty
		if len(got) == 0 && len(in) == 0 {
			continue
		}
		if !reflect.DeepEqual(got, in) {
			t.Fatalf("case %d: got %q want %q", i, got, in)
		}
	}

	// persists across reopen
	m.Close()
	m2, err := MMKVWithID(dir, "vec")
	if err != nil {
		t.Fatal(err)
	}
	defer m2.Close()
	if got, ok := m2.GetStringSlice("k3"); !ok || !reflect.DeepEqual(got, cases[3]) {
		t.Fatalf("after reopen: got %q ok=%v", got, ok)
	}
}

func TestMMKVImportFrom(t *testing.T) {
	dir := t.TempDir()
	src, err := MMKVWithID(dir, "src")
	if err != nil {
		t.Fatal(err)
	}
	src.SetInt32("i", 42)
	src.SetString("s", "hello")
	src.SetStringSlice("v", []string{"x", "y"})

	dst, err := MMKVWithID(dir, "dst")
	if err != nil {
		t.Fatal(err)
	}
	defer dst.Close()
	dst.SetInt32("pre", 1) // pre-existing key survives

	n, err := dst.ImportFrom(src)
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Errorf("imported %d, want 3", n)
	}
	if v, ok := dst.GetInt32("i"); !ok || v != 42 {
		t.Errorf("i = %v,%v", v, ok)
	}
	if v, ok := dst.GetString("s"); !ok || v != "hello" {
		t.Errorf("s = %q,%v", v, ok)
	}
	if v, ok := dst.GetStringSlice("v"); !ok || !reflect.DeepEqual(v, []string{"x", "y"}) {
		t.Errorf("v = %q,%v", v, ok)
	}
	if v, ok := dst.GetInt32("pre"); !ok || v != 1 {
		t.Errorf("pre = %v,%v", v, ok)
	}
	src.Close()
}

func TestMMKVValueSizeAndBuffer(t *testing.T) {
	dir := t.TempDir()
	m, err := MMKVWithID(dir, "vs")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	m.SetString("s", "hello") // stored blob = writeData("hello") = [05 h e l l o] (6 bytes)

	sz := m.GetValueSize("s")
	if sz != 6 {
		t.Errorf("GetValueSize = %d, want 6", sz)
	}
	if m.GetValueSize("absent") != -1 {
		t.Errorf("GetValueSize(absent) should be -1")
	}

	buf := make([]byte, sz)
	if n := m.WriteValueToBuffer("s", buf); n != sz {
		t.Errorf("WriteValueToBuffer = %d, want %d", n, sz)
	}
	small := make([]byte, sz-1)
	if n := m.WriteValueToBuffer("s", small); n != -1 {
		t.Errorf("WriteValueToBuffer into too-small buf = %d, want -1", n)
	}
}
