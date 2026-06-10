// Head-to-head read benchmark in ONE environment, reading the SAME file:
//   - cgo copy   : official lib GetBytes/GetString (C.GoBytes -> Go heap copy)
//   - cgo shared : official lib GetBytesBuffer + ByteSliceView (zero-copy, Destroy)
//   - pure-Go    : puremmkv (no cgo at all)
//
// Run in the arm64 Linux container (needs cgo + shipped libs):
//
//	go test -bench . -benchmem ./...
package harness

import (
	"sync"
	"testing"

	"github.com/catundercar/mmkv-go"
	cgommkv "tencent.com/mmkv"
)

const (
	mmapID   = "benchpure"
	keyI32   = "i32"
	keySmall = "by_small"
	keyBig   = "by_4k"
	keyStr   = "s"
)

var (
	once   sync.Once
	cgoKV  cgommkv.MMKV
	pureR  *mmkv.Reader
	pureKV *mmkv.MMKV // read+write type, opened single-process, reading the same fixture
	bigVal []byte
)

func setup(tb testing.TB) {
	once.Do(func() {
		dir := ensureInit(tb)
		cgoKV = cgommkv.MMKVWithID(mmapID)
		cgoKV.ClearAll()

		bigVal = make([]byte, 4096)
		for i := range bigVal {
			bigVal[i] = byte(i % 251)
		}
		cgoKV.SetInt32(2147483647, keyI32)
		cgoKV.SetBytes([]byte{0, 1, 2, 3, 255, 254}, keySmall)
		cgoKV.SetBytes(bigVal, keyBig)
		cgoKV.SetString("hello world, a medium-ish string value", keyStr)
		cgoKV.Sync(true)

		var err error
		pureR, err = mmkv.Open(dir, mmapID)
		if err != nil {
			tb.Fatalf("mmkv.Open: %v", err)
		}
		pureKV, err = mmkv.MMKVWithID(dir, mmapID)
		if err != nil {
			tb.Fatalf("mmkv.MMKVWithID: %v", err)
		}
	})
}

// write-bench instances (separate IDs so the read fixture stays static).
var (
	writeOnce sync.Once
	cgoWKV    cgommkv.MMKV
	pureWKV   *mmkv.MMKV
)

func writeSetup(tb testing.TB) {
	setup(tb) // ensures bigVal + the shared init dir
	writeOnce.Do(func() {
		dir := ensureInit(tb)
		cgoWKV = cgommkv.MMKVWithID("benchwrite_cgo")
		cgoWKV.ClearAll()
		cgoWKV.SetInt32(0, keyI32)
		cgoWKV.Sync(true)
		var err error
		pureWKV, err = mmkv.MMKVWithID(dir, "benchwrite_pure")
		if err != nil {
			tb.Fatalf("mmkv.MMKVWithID(write): %v", err)
		}
		_ = pureWKV.ClearAll()
		_ = pureWKV.SetInt32(keyI32, 0)
		_ = pureWKV.Sync()
	})
}

// sinks prevent dead-code elimination.
var (
	sinkI32   int32
	sinkBytes []byte
	sinkStr   string
)

// ---- int32 ----

func BenchmarkInt32_Cgo(b *testing.B) {
	setup(b)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		sinkI32 = cgoKV.GetInt32(keyI32)
	}
}

func BenchmarkInt32_Pure(b *testing.B) {
	setup(b)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		sinkI32, _ = pureR.GetInt32(keyI32)
	}
}

// ---- bytes 4KB ----

func BenchmarkBytes4K_CgoCopy(b *testing.B) {
	setup(b)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		sinkBytes = cgoKV.GetBytes(keyBig)
	}
}

func BenchmarkBytes4K_CgoShared(b *testing.B) {
	setup(b)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		buf := cgoKV.GetBytesBuffer(keyBig)
		sinkBytes = buf.ByteSliceView()
		buf.Destroy()
	}
}

func BenchmarkBytes4K_PureView(b *testing.B) {
	setup(b)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		sinkBytes, _ = pureR.GetBytes(keyBig) // view, no copy
	}
}

func BenchmarkBytes4K_PureCopy(b *testing.B) {
	setup(b)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		sinkBytes, _ = pureR.GetBytesCopy(keyBig)
	}
}

// ---- bytes small ----

func BenchmarkBytesSmall_CgoCopy(b *testing.B) {
	setup(b)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		sinkBytes = cgoKV.GetBytes(keySmall)
	}
}

func BenchmarkBytesSmall_PureView(b *testing.B) {
	setup(b)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		sinkBytes, _ = pureR.GetBytes(keySmall)
	}
}

// ---- string ----

func BenchmarkString_CgoCopy(b *testing.B) {
	setup(b)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		sinkStr = cgoKV.GetString(keyStr) // C.GoStringN: copies C memory into a Go string
	}
}

func BenchmarkString_CgoShared(b *testing.B) {
	setup(b)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		buf := cgoKV.GetStringBuffer(keyStr)
		sinkStr = buf.StringView() // zero-copy string over C memory (no GoStringN copy)
		buf.Destroy()
	}
}

func BenchmarkString_PureView(b *testing.B) {
	setup(b)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		sinkStr, _ = pureR.GetString(keyStr) // zero-copy unsafe.String view
	}
}

func BenchmarkString_PureCopy(b *testing.B) {
	setup(b)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		sinkStr, _ = pureR.GetStringCopy(keyStr)
	}
}

// ---- read+write MMKV type, reads (single-process: mutex + map, no flock) ----

func BenchmarkInt32_MMKV(b *testing.B) {
	setup(b)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		sinkI32, _ = pureKV.GetInt32(keyI32)
	}
}

func BenchmarkBytes4K_MMKVView(b *testing.B) {
	setup(b)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		sinkBytes, _ = pureKV.GetBytes(keyBig)
	}
}

func BenchmarkString_MMKVView(b *testing.B) {
	setup(b)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		sinkStr, _ = pureKV.GetString(keyStr)
	}
}

// ---- writes: cgo vs pure-Go MMKV ----
// SetInt32 runs against a single-key store, so both sides take the single-key
// override fast path (rewrite in place, no write-back, no msync). SetBytes4K
// runs in a 2-key store, exercising the append fast path + periodic full
// write-back (which msyncs) on both sides.

func BenchmarkSetInt32_Cgo(b *testing.B) {
	writeSetup(b)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		cgoWKV.SetInt32(int32(i), keyI32)
	}
}

func BenchmarkSetInt32_MMKV(b *testing.B) {
	writeSetup(b)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = pureWKV.SetInt32(keyI32, int32(i))
	}
}

func BenchmarkSetBytes4K_Cgo(b *testing.B) {
	writeSetup(b)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		cgoWKV.SetBytes(bigVal, keyBig)
	}
}

func BenchmarkSetBytes4K_MMKV(b *testing.B) {
	writeSetup(b)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = pureWKV.SetBytes(keyBig, bigVal)
	}
}
