//go:build unix

package mmkv

import (
	"os"
	"path/filepath"
	"testing"
)

// open two independent fds (= two open-file-descriptions) for the same file, so
// flock mutually excludes them exactly as two processes would.
func twoLocks(t *testing.T) (*fileLock, *fileLock) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "lockfile")
	if err := os.WriteFile(p, []byte("x"), 0o666); err != nil {
		t.Fatal(err)
	}
	f1, err := os.OpenFile(p, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	f2, err := os.OpenFile(p, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f1.Close(); f2.Close() })
	return newFileLock(f1), newFileLock(f2)
}

func mustTry(t *testing.T, l *fileLock, lt lockType, want bool) {
	t.Helper()
	ok, err := l.tryLock(lt)
	if err != nil {
		t.Fatalf("tryLock: %v", err)
	}
	if ok != want {
		t.Fatalf("tryLock(%v) = %v, want %v", lt, ok, want)
	}
}

func TestFileLockExclusiveExcludes(t *testing.T) {
	a, b := twoLocks(t)
	if err := a.lock(exclusiveLock); err != nil {
		t.Fatal(err)
	}
	mustTry(t, b, exclusiveLock, false) // a holds it
	mustTry(t, b, sharedLock, false)    // exclusive blocks shared too
	if err := a.unlock(exclusiveLock); err != nil {
		t.Fatal(err)
	}
	mustTry(t, b, exclusiveLock, true) // now free
	_ = b.unlock(exclusiveLock)
}

func TestFileLockSharedCompatible(t *testing.T) {
	a, b := twoLocks(t)
	if err := a.lock(sharedLock); err != nil {
		t.Fatal(err)
	}
	mustTry(t, b, sharedLock, true)     // shared + shared ok
	mustTry(t, b, exclusiveLock, false) // but not exclusive while shared held
	_ = b.unlock(sharedLock)
	_ = a.unlock(sharedLock)
}

// TestFileLockRefCount checks nested acquires only hit the OS on the first/last
// reference (the count bookkeeping that mirrors MMKV's FileLock).
func TestFileLockRefCount(t *testing.T) {
	a, b := twoLocks(t)
	if err := a.lock(sharedLock); err != nil { // shared=1, real LOCK_SH
		t.Fatal(err)
	}
	if err := a.lock(sharedLock); err != nil { // shared=2, no syscall
		t.Fatal(err)
	}
	if a.shared != 2 {
		t.Fatalf("shared count = %d, want 2", a.shared)
	}
	_ = a.unlock(sharedLock) // shared=1, still held at OS level
	mustTry(t, b, exclusiveLock, false)
	_ = a.unlock(sharedLock) // shared=0, real LOCK_UN
	mustTry(t, b, exclusiveLock, true)
	_ = b.unlock(exclusiveLock)
}

// TestFileLockUpgradeDowngrade exercises shared→exclusive upgrade and the
// exclusive→shared downgrade on release.
func TestFileLockUpgradeDowngrade(t *testing.T) {
	a, b := twoLocks(t)
	if err := a.lock(sharedLock); err != nil {
		t.Fatal(err)
	}
	if err := a.lock(exclusiveLock); err != nil { // upgrade to LOCK_EX
		t.Fatal(err)
	}
	mustTry(t, b, sharedLock, false)                // exclusive now held
	if err := a.unlock(exclusiveLock); err != nil { // downgrade back to LOCK_SH
		t.Fatal(err)
	}
	mustTry(t, b, sharedLock, true) // shared again
	_ = b.unlock(sharedLock)
	_ = a.unlock(sharedLock)
}
