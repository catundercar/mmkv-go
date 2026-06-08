package harness

import (
	"os"
	"sync"
	"testing"

	cgommkv "tencent.com/mmkv"
)

// MMKV.initializeMMKV is a process-global, one-shot call: the first root dir
// wins and later calls with other dirs are ignored. So every test/benchmark in
// this package must share ONE root dir (and use distinct mmapIDs).
var (
	initDir  string
	initOnce sync.Once
)

func ensureInit(tb testing.TB) string {
	initOnce.Do(func() {
		d, err := os.MkdirTemp("", "mmkvtests")
		if err != nil {
			tb.Fatal(err)
		}
		cgommkv.InitializeMMKVWithLogLevel(d, cgommkv.MMKVLogNone)
		initDir = d
	})
	return initDir
}
