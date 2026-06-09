//go:build unix

package mmkv

import (
	"errors"
	"os"
	"syscall"
)

type lockType int

const (
	sharedLock lockType = iota
	exclusiveLock
)

// fileLock wraps flock(2) on one fd with shared/exclusive reference counting and
// lock upgrade/downgrade — a faithful port of MMKV's FileLock
// (Core/InterProcessLock.cpp) so a Go process interlocks with a C++ MMKV process
// over the same .crc file.
//
// flock is associated with the open file description, not the process: within a
// single process it does NOT serialize goroutines, so a fileLock must be used
// under the instance's thread lock (exactly as MMKV uses it under m_lock).
// Cross-process exclusion is the kernel flock's job. Hence no internal mutex —
// the counters are only ever touched by the one goroutine holding the thread
// lock, matching the C++ FileLock which also has no lock of its own.
type fileLock struct {
	f         *os.File
	shared    int
	exclusive int
}

func newFileLock(f *os.File) *fileLock { return &fileLock{f: f} }

func (l *fileLock) fd() int { return int(l.f.Fd()) }

// lock blocks until a lock of type t is held. A shared request is satisfied for
// free if any lock is already held; an exclusive request upgrades from a held
// shared lock. Counts are bumped without a syscall when the kernel state already
// covers the request (matches FileLock::doLock).
func (l *fileLock) lock(t lockType) error {
	if t == sharedLock {
		if l.shared > 0 || l.exclusive > 0 {
			l.shared++
			return nil
		}
		if err := l.platformLock(syscall.LOCK_SH, true, false); err != nil {
			return err
		}
		l.shared++
		return nil
	}
	if l.exclusive > 0 {
		l.exclusive++
		return nil
	}
	unlockFirst := l.shared > 0 // upgrade path
	if err := l.platformLock(syscall.LOCK_EX, true, unlockFirst); err != nil {
		return err
	}
	l.exclusive++
	return nil
}

// tryLock attempts a non-blocking acquire; ok=false means the lock is contended.
func (l *fileLock) tryLock(t lockType) (ok bool, err error) {
	if t == sharedLock {
		if l.shared > 0 || l.exclusive > 0 {
			l.shared++
			return true, nil
		}
		ok, err = l.platformTryLock(syscall.LOCK_SH, false)
		if ok {
			l.shared++
		}
		return ok, err
	}
	if l.exclusive > 0 {
		l.exclusive++
		return true, nil
	}
	unlockFirst := l.shared > 0
	ok, err = l.platformTryLock(syscall.LOCK_EX, unlockFirst)
	if ok {
		l.exclusive++
	}
	return ok, err
}

// unlock releases one reference. The syscall fires only when the last reference
// of its kind is dropped; releasing the last exclusive while a shared reference
// remains downgrades to LOCK_SH instead of unlocking (matches FileLock::unlock).
func (l *fileLock) unlock(t lockType) error {
	if t == sharedLock {
		if l.shared == 0 {
			return errors.New("mmkv: unlock shared without holding it")
		}
		if l.shared > 1 || l.exclusive > 0 {
			l.shared--
			return nil
		}
		if err := l.platformUnlock(false); err != nil {
			return err
		}
		l.shared--
		return nil
	}
	if l.exclusive == 0 {
		return errors.New("mmkv: unlock exclusive without holding it")
	}
	if l.exclusive > 1 {
		l.exclusive--
		return nil
	}
	downgrade := l.shared > 0
	if err := l.platformUnlock(downgrade); err != nil {
		return err
	}
	l.exclusive--
	return nil
}

// platformLock issues a blocking flock. When unlockFirst (a shared→exclusive
// upgrade), it first tries a non-blocking upgrade and, failing that, drops the
// held shared lock before blocking — the deadlock-avoidance dance MMKV uses
// (InterProcessLock.cpp platformLock).
func (l *fileLock) platformLock(how int, wait, unlockFirst bool) error {
	if unlockFirst {
		if err := syscall.Flock(l.fd(), how|syscall.LOCK_NB); err == nil {
			return nil
		}
		if err := syscall.Flock(l.fd(), syscall.LOCK_UN); err != nil {
			return err
		}
	}
	cmd := how
	if !wait {
		cmd |= syscall.LOCK_NB
	}
	if err := syscall.Flock(l.fd(), cmd); err != nil {
		if unlockFirst {
			_ = syscall.Flock(l.fd(), syscall.LOCK_SH) // best-effort recover the dropped shared lock
		}
		return err
	}
	return nil
}

func (l *fileLock) platformTryLock(how int, unlockFirst bool) (bool, error) {
	err := l.platformLock(how, false, unlockFirst)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
		return false, nil
	}
	return false, err
}

func (l *fileLock) platformUnlock(downgradeToShared bool) error {
	cmd := syscall.LOCK_UN
	if downgradeToShared {
		cmd = syscall.LOCK_SH
	}
	return syscall.Flock(l.fd(), cmd)
}
