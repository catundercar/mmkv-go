package test

import (
	"fmt"
	"testing"

	cgommkv "tencent.com/mmkv"
	"unlock-music.dev/mmkv"
)

var rootDir = "data"

func init() {
	cgommkv.InitializeMMKVWithLogLevel(rootDir, cgommkv.MMKVLogError)
	kv := cgommkv.DefaultMMKV()
	fmt.Println(kv.SetString("world", "hello"))  // checkAndLoad data
	fmt.Println(kv.SetInt32(22, "int32"))        // checkAndLoad data
	fmt.Println(kv.SetUInt32(22, "uint32"))      // checkAndLoad data
	fmt.Println(kv.SetInt64(-22, "int64"))       // checkAndLoad data
	fmt.Println(kv.SetUInt64(22, "uint64"))      // checkAndLoad data
	fmt.Println(kv.SetFloat32(22.22, "float32")) // checkAndLoad data
	fmt.Println(kv.SetFloat64(22.22, "float64")) // checkAndLoad data
}

func TestMMKV(t *testing.T) {
	kv := cgommkv.DefaultMMKV()
	fmt.Println(string(kv.GetBytes("hello")))

	m, err := mmkv.NewManager(rootDir)
	if err != nil {
		t.Fatal(err)
	}

	gkv, err := m.OpenVault("mmkv.default")
	if err != nil {
		t.Fatal(err)
	}
	v, err := gkv.GetBytes("hello")
	if err != nil {
		t.Fatal(err)
	}
	fmt.Println(string(v))
}

func BenchmarkMMKVCGo(b *testing.B) {
	kv := cgommkv.DefaultMMKV()
	kv.GetBytes("hello") // checkAndLoad data

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = kv.GetBytes("hello")
	}
}

func BenchmarkMMKVGo(b *testing.B) {
	m, err := mmkv.NewManager(rootDir)
	if err != nil {
		b.Fatal(err)
	}

	kv, err := m.OpenVault("")
	if err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, _ = kv.GetBytes("hello")
	}
}
