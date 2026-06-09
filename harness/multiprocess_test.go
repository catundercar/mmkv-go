package harness

import (
	"fmt"
	"os"
	"os/exec"
	"testing"
	"time"

	mmkv "github.com/catundercar/mmkv-go"
	cgommkv "tencent.com/mmkv"
)

const (
	mpWrites  = 1500
	mpReaders = 3
)

// TestMultiProcess is the real single-writer + multi-reader scenario across
// SEPARATE OS processes: one cgo writer process (MMKV_MULTI_PROCESS) holds the
// exclusive flock on .crc while several pure-Go reader processes take the shared
// flock on reload. Unlike TestLiveReadConcurrent (goroutines in one process),
// this exercises the cross-process flock interlock for real.
//
// It re-execs this test binary with MMKV_MP_ROLE to act as writer/reader; the
// child processes report via exit code.
func TestMultiProcess(t *testing.T) {
	switch os.Getenv("MMKV_MP_ROLE") {
	case "writer":
		mpWriter()
		return
	case "reader":
		mpReader()
		return
	}

	dir := ensureInit(t)
	const id = "mp"

	// Create the initial file so reader processes can Open immediately, then
	// release our handle; the writer child reopens it.
	w := cgommkv.MMKVWithIDAndMode(id, cgommkv.MMKV_MULTI_PROCESS)
	w.ClearAll()
	w.SetInt32(0, "seq")
	w.SetBytes([]byte{0}, "payload")
	w.Sync(true)
	w.Close()

	baseEnv := append(os.Environ(), "MMKV_MP_DIR="+dir, "MMKV_MP_ID="+id)
	spawn := func(role string) *exec.Cmd {
		c := exec.Command(os.Args[0], "-test.run=^TestMultiProcess$")
		c.Env = append(baseEnv, "MMKV_MP_ROLE="+role)
		c.Stdout, c.Stderr = os.Stdout, os.Stderr
		return c
	}

	readers := make([]*exec.Cmd, mpReaders)
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
	t.Logf("OK: 1 cgo writer + %d pure-Go reader processes, %d writes, no torn reads", mpReaders, mpWrites)
}

// mpWriter (child): hammer writes from a dedicated process. os.Exit-based result.
func mpWriter() {
	dir, id := os.Getenv("MMKV_MP_DIR"), os.Getenv("MMKV_MP_ID")
	cgommkv.InitializeMMKVWithLogLevel(dir, cgommkv.MMKVLogNone)
	w := cgommkv.MMKVWithIDAndMode(id, cgommkv.MMKV_MULTI_PROCESS)
	for k := 1; k <= mpWrites; k++ {
		pl := make([]byte, 8+(k%200))
		for i := range pl {
			pl[i] = byte(k) // identical bytes -> a torn read is detectable
		}
		w.SetBytes(pl, "payload")
		w.SetInt32(int32(k), "seq")
		w.Sync(true)
	}
	os.Exit(0)
}

// mpReader (child): pure-Go reader process; exits 0 once it observes the writer's
// final value with zero torn reads / reload errors, 1 on any inconsistency or timeout.
func mpReader() {
	dir, id := os.Getenv("MMKV_MP_DIR"), os.Getenv("MMKV_MP_ID")
	deadline := time.Now().Add(30 * time.Second)

	var r *mmkv.Reader
	for {
		var err error
		if r, err = mmkv.Open(dir, id); err == nil {
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
		if pl, ok := r.GetBytes("payload"); ok {
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
			if s >= int32(mpWrites) {
				os.Exit(0) // observed the writer's final value, consistently
			}
		}
	}
	fmt.Fprintf(os.Stderr, "reader timeout: maxSeq=%d want %d\n", maxSeq, mpWrites)
	os.Exit(1)
}
