//go:build unix

package mmkv

// Lock / Unlock / TryLock expose MMKV's public cross-process exclusive lock for
// atomic read-modify-write across processes (matching MMKV::lock/unlock/try_lock).
//
// Only the cross-process flock is held across the user's critical section; the
// thread mutex is taken just to manipulate the lock counters and released
// immediately, so ordinary Get/Set calls between Lock and Unlock do NOT deadlock
// (they re-enter the already-held exclusive flock by reference count). In
// single-process mode (no WithMultiProcess) these are no-ops, exactly like MMKV.
//
// Usage:
//
//	m.Lock()
//	defer m.Unlock()
//	v, _ := m.GetInt32("counter")
//	m.SetInt32("counter", v+1)
func (m *MMKV) Lock() {
	m.mu.Lock()
	if m.multiProcess {
		_ = m.fl.lock(exclusiveLock)
	}
	m.mu.Unlock()
}

func (m *MMKV) Unlock() {
	m.mu.Lock()
	if m.multiProcess {
		_ = m.fl.unlock(exclusiveLock)
	}
	m.mu.Unlock()
}

// TryLock attempts the exclusive cross-process lock without blocking; it returns
// true if acquired (always true in single-process mode).
func (m *MMKV) TryLock() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.multiProcess {
		return true
	}
	ok, _ := m.fl.tryLock(exclusiveLock)
	return ok
}
