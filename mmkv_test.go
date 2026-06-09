//go:build unix

package mmkv

import (
	"bytes"
	"fmt"
	"math"
	"strings"
	"testing"
)

func TestMMKVReadWriteRoundTrip(t *testing.T) {
	dir := t.TempDir()
	m, err := MMKVWithID(dir, "rw")
	if err != nil {
		t.Fatal(err)
	}

	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(m.SetBool("b", true))
	must(m.SetInt32("i32", -123456))
	must(m.SetInt64("i64", math.MinInt64))
	must(m.SetUInt32("u32", math.MaxUint32))
	must(m.SetUInt64("u64", math.MaxUint64))
	must(m.SetFloat32("f32", -2.5))
	must(m.SetFloat64("f64", math.MaxFloat64))
	must(m.SetString("s", "你好,世界🌍"))
	must(m.SetBytes("by", []byte{0, 1, 2, 250, 251, 252}))

	check := func(m *MMKV) {
		t.Helper()
		if v, ok := m.GetBool("b"); !ok || !v {
			t.Errorf("b=%v,%v", v, ok)
		}
		if v, ok := m.GetInt32("i32"); !ok || v != -123456 {
			t.Errorf("i32=%v,%v", v, ok)
		}
		if v, ok := m.GetInt64("i64"); !ok || v != math.MinInt64 {
			t.Errorf("i64=%v,%v", v, ok)
		}
		if v, ok := m.GetUInt32("u32"); !ok || v != math.MaxUint32 {
			t.Errorf("u32=%v,%v", v, ok)
		}
		if v, ok := m.GetUInt64("u64"); !ok || v != math.MaxUint64 {
			t.Errorf("u64=%v,%v", v, ok)
		}
		if v, ok := m.GetFloat32("f32"); !ok || v != -2.5 {
			t.Errorf("f32=%v,%v", v, ok)
		}
		if v, ok := m.GetFloat64("f64"); !ok || v != math.MaxFloat64 {
			t.Errorf("f64=%v,%v", v, ok)
		}
		if v, ok := m.GetString("s"); !ok || v != "你好,世界🌍" {
			t.Errorf("s=%q,%v", v, ok)
		}
		if v, ok := m.GetBytes("by"); !ok || !bytes.Equal(v, []byte{0, 1, 2, 250, 251, 252}) {
			t.Errorf("by=% x,%v", v, ok)
		}
	}
	check(m)
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}

	// reopen: values persist
	m2, err := MMKVWithID(dir, "rw")
	if err != nil {
		t.Fatal(err)
	}
	defer m2.Close()
	check(m2)
}

func TestMMKVAppendAndGrow(t *testing.T) {
	dir := t.TempDir()
	m, err := MMKVWithID(dir, "grow")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()

	const n = 400
	val := strings.Repeat("z", 200) // ~80KB total → forces at least one grow past a page
	for i := 0; i < n; i++ {
		if err := m.SetString(fmt.Sprintf("k%04d", i), fmt.Sprintf("%s-%d", val, i)); err != nil {
			t.Fatalf("set %d: %v", i, err)
		}
	}
	if got := m.Count(); got != n {
		t.Fatalf("count=%d, want %d", got, n)
	}
	if m.TotalSize() <= 4096 {
		t.Errorf("file did not grow: TotalSize=%d", m.TotalSize())
	}
	for i := 0; i < n; i++ {
		want := fmt.Sprintf("%s-%d", val, i)
		if got, ok := m.GetString(fmt.Sprintf("k%04d", i)); !ok || got != want {
			t.Fatalf("k%04d mismatch ok=%v", i, ok)
		}
	}

	// persists across reopen
	m.Close()
	m2, err := MMKVWithID(dir, "grow")
	if err != nil {
		t.Fatal(err)
	}
	defer m2.Close()
	if got := m2.Count(); got != n {
		t.Fatalf("after reopen count=%d, want %d", got, n)
	}
	if got, ok := m2.GetString("k0399"); !ok || got != fmt.Sprintf("%s-%d", val, 399) {
		t.Fatalf("k0399 after reopen ok=%v", ok)
	}
}

func TestMMKVOverwriteRemoveClear(t *testing.T) {
	dir := t.TempDir()
	m, err := MMKVWithID(dir, "ops")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()

	// overwrite: last write wins
	m.SetInt32("ow", 111)
	m.SetInt32("ow", 222)
	if v, ok := m.GetInt32("ow"); !ok || v != 222 {
		t.Fatalf("overwrite ow=%v,%v", v, ok)
	}

	// remove single (tombstone via append)
	m.SetString("del", "bye")
	if err := m.RemoveValueForKey("del"); err != nil {
		t.Fatal(err)
	}
	if m.Contains("del") {
		t.Errorf("del still present after remove")
	}
	// removal persists across reopen (tombstone replays correctly)
	m.Close()
	m, err = MMKVWithID(dir, "ops")
	if err != nil {
		t.Fatal(err)
	}
	if m.Contains("del") {
		t.Errorf("del present after reopen")
	}
	if v, ok := m.GetInt32("ow"); !ok || v != 222 {
		t.Errorf("ow lost after reopen: %v,%v", v, ok)
	}

	// batch remove
	m.SetInt32("a", 1)
	m.SetInt32("b", 2)
	m.SetInt32("c", 3)
	if err := m.RemoveValuesForKeys([]string{"a", "c"}); err != nil {
		t.Fatal(err)
	}
	if m.Contains("a") || m.Contains("c") || !m.Contains("b") {
		t.Errorf("batch remove wrong: a=%v c=%v b=%v", m.Contains("a"), m.Contains("c"), m.Contains("b"))
	}

	// clearAll
	if err := m.ClearAll(); err != nil {
		t.Fatal(err)
	}
	if m.Count() != 0 {
		t.Errorf("after ClearAll count=%d", m.Count())
	}
	// still usable after clear
	m.SetInt32("fresh", 7)
	if v, ok := m.GetInt32("fresh"); !ok || v != 7 {
		t.Errorf("post-clear set/get failed: %v,%v", v, ok)
	}
}

func TestMMKVRegistrySharesInstance(t *testing.T) {
	dir := t.TempDir()
	a, err := MMKVWithID(dir, "reg")
	if err != nil {
		t.Fatal(err)
	}
	b, err := MMKVWithID(dir, "reg")
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Fatal("MMKVWithID returned different instances for the same file")
	}
	a.SetInt32("x", 42)
	if v, ok := b.GetInt32("x"); !ok || v != 42 {
		t.Errorf("shared instance not coherent: %v,%v", v, ok)
	}
	a.Close()
	// after close, a fresh instance is created
	c, err := MMKVWithID(dir, "reg")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if c == a {
		t.Error("closed instance was reused")
	}
	if v, ok := c.GetInt32("x"); !ok || v != 42 {
		t.Errorf("reopened instance lost data: %v,%v", v, ok)
	}
}
