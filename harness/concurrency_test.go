package harness

import (
	"testing"

	"github.com/catundercar/mmkv-go"
	cgommkv "tencent.com/mmkv"
)

const writeCount = 3000

// startWriter launches a cgo writer (MMKV_MULTI_PROCESS) that hammers the file.
// Each round writes a payload whose bytes are all identical (so a torn read that
// mixes two writes is detectable) plus an int32 "seq".
func startWriter(dir, id string) <-chan struct{} {
	w := cgommkv.MMKVWithIDAndMode(id, cgommkv.MMKV_MULTI_PROCESS)
	w.ClearAll()
	w.SetInt32(0, "seq")
	w.SetBytes([]byte{0}, "payload")
	w.Sync(true)

	done := make(chan struct{})
	go func() {
		defer close(done)
		for k := 1; k <= writeCount; k++ {
			pl := make([]byte, 8+(k%200))
			for i := range pl {
				pl[i] = byte(k)
			}
			w.SetBytes(pl, "payload")
			w.SetInt32(int32(k), "seq")
			w.Sync(true)
		}
	}()
	return done
}

// TestLiveReadConcurrent is the user's scenario: a cgo writer hammers the file
// while a pure-Go reader does plain Gets — freshness is transparent (check-on-read,
// the default). Each read must observe a consistent snapshot (no torn payload),
// the reader must reach the writer's final value (changes load transparently), and
// the shared flock taken on reload must prevent any CRC error.
func TestLiveReadConcurrent(t *testing.T) {
	dir := ensureInit(t)
	const id = "live"
	done := startWriter(dir, id)

	r, err := mmkv.Open(dir, id)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer r.Close()

	var reads int
	var maxSeq int32
	read := func() {
		if pl, ok := r.GetBytes("payload"); ok {
			for i := 1; i < len(pl); i++ {
				if pl[i] != pl[0] {
					t.Fatalf("TORN READ: payload[%d]=%d != [0]=%d len=%d", i, pl[i], pl[0], len(pl))
				}
			}
		}
		if s, ok := r.GetInt32("seq"); ok && s > maxSeq {
			maxSeq = s
		}
		reads++
	}

loop:
	for {
		read()
		select {
		case <-done:
			break loop
		default:
		}
	}
	read() // final read transparently loads the last committed write

	if err := r.Err(); err != nil {
		t.Fatalf("reload error (flock should prevent torn reads): %v", err)
	}
	if maxSeq != writeCount {
		t.Fatalf("reader did not load latest change: maxSeq=%d want %d", maxSeq, writeCount)
	}
	t.Logf("OK: %d transparent reads, observed seq up to %d, zero torn reads, Err=nil", reads, maxSeq)
}
