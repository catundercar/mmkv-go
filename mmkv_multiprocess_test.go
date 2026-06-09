//go:build unix

package mmkv

import (
	"fmt"
	"os"
	"os/exec"
	"testing"
	"time"
)

const (
	mp2Writes  = 800
	mp2Readers = 3
)

// TestMMKVMultiProcess is the real single-writer + multi-reader scenario for the
// live read+write MMKV type across SEPARATE OS processes — all pure Go. One
// writer process holds the exclusive flock per write; several reader processes
// take the shared flock and reload via checkLoadData. Readers use GetBytesCopy
// (copied under the shared flock), so any torn payload would mean the
// cross-process flock interlock failed.
//
// It re-execs this test binary with MMKV_MP2_ROLE to act as writer/reader; the
// children report via exit code.
func TestMMKVMultiProcess(t *testing.T) {
	switch os.Getenv("MMKV_MP2_ROLE") {
	case "writer":
		mp2Writer()
		return
	case "reader":
		mp2Reader()
		return
	}

	dir := t.TempDir()
	const id = "mp2"

	// Seed the file so readers can Open immediately, then release our handle.
	seed, err := MMKVWithID(dir, id, WithMultiProcess())
	if err != nil {
		t.Fatal(err)
	}
	seed.SetInt32("seq", 0)
	seed.SetBytes("payload", []byte{0})
	seed.Sync()
	seed.Close()

	baseEnv := append(os.Environ(), "MMKV_MP2_DIR="+dir, "MMKV_MP2_ID="+id)
	spawn := func(role string) *exec.Cmd {
		c := exec.Command(os.Args[0], "-test.run=^TestMMKVMultiProcess$", "-test.v")
		c.Env = append(baseEnv, "MMKV_MP2_ROLE="+role)
		c.Stdout, c.Stderr = os.Stdout, os.Stderr
		return c
	}

	readers := make([]*exec.Cmd, mp2Readers)
	for i := range readers {
		readers[i] = spawn("reader")
		if err := readers[i].Start(); err != nil {
			t.Fatalf("start reader %d: %v", i, err)
		}
	}
	writer := spawn("writer")
	if err := writer.Start(); err != nil {
		t.Fatalf("start writer: %v", err)
	}

	if err := writer.Wait(); err != nil {
		t.Fatalf("writer process failed: %v", err)
	}
	for i, r := range readers {
		if err := r.Wait(); err != nil {
			t.Fatalf("reader process %d failed: %v", i, err)
		}
	}
	t.Logf("OK: 1 pure-Go writer + %d pure-Go reader processes, %d writes, no torn reads", mp2Readers, mp2Writes)
}

// mp2Writer (child): hammer writes from a dedicated process.
func mp2Writer() {
	dir, id := os.Getenv("MMKV_MP2_DIR"), os.Getenv("MMKV_MP2_ID")
	w, err := MMKVWithID(dir, id, WithMultiProcess())
	if err != nil {
		fmt.Fprintf(os.Stderr, "writer open: %v\n", err)
		os.Exit(1)
	}
	for k := 1; k <= mp2Writes; k++ {
		pl := make([]byte, 8+(k%200))
		for i := range pl {
			pl[i] = byte(k) // identical bytes -> a torn read is detectable
		}
		if err := w.SetBytes("payload", pl); err != nil {
			fmt.Fprintf(os.Stderr, "writer set payload: %v\n", err)
			os.Exit(1)
		}
		if err := w.SetInt32("seq", int32(k)); err != nil {
			fmt.Fprintf(os.Stderr, "writer set seq: %v\n", err)
			os.Exit(1)
		}
		if err := w.Sync(); err != nil {
			fmt.Fprintf(os.Stderr, "writer sync: %v\n", err)
			os.Exit(1)
		}
	}
	os.Exit(0)
}

// mp2Reader (child): exits 0 once it observes the writer's final seq with zero
// torn reads, 1 on any inconsistency or timeout.
func mp2Reader() {
	dir, id := os.Getenv("MMKV_MP2_DIR"), os.Getenv("MMKV_MP2_ID")
	deadline := time.Now().Add(40 * time.Second)

	var r *MMKV
	for {
		var err error
		if r, err = MMKVWithID(dir, id, WithMultiProcess()); err == nil {
			break
		}
		if time.Now().After(deadline) {
			fmt.Fprintf(os.Stderr, "reader: open timed out\n")
			os.Exit(1)
		}
		time.Sleep(10 * time.Millisecond)
	}

	var maxSeq int32
	for time.Now().Before(deadline) {
		if pl, ok := r.GetBytesCopy("payload"); ok { // copied under the shared flock
			for i := 1; i < len(pl); i++ {
				if pl[i] != pl[0] {
					fmt.Fprintf(os.Stderr, "reader TORN READ: payload[%d]=%d != [0]=%d len=%d\n", i, pl[i], pl[0], len(pl))
					os.Exit(1)
				}
			}
		}
		if e := r.Err(); e != nil {
			fmt.Fprintf(os.Stderr, "reader reload error: %v\n", e)
			os.Exit(1)
		}
		if s, ok := r.GetInt32("seq"); ok {
			if s > maxSeq {
				maxSeq = s
			}
			if s >= int32(mp2Writes) {
				os.Exit(0)
			}
		}
	}
	fmt.Fprintf(os.Stderr, "reader timeout: maxSeq=%d want %d\n", maxSeq, mp2Writes)
	os.Exit(1)
}
