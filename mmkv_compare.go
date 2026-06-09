//go:build unix

package mmkv

// EnableCompareBeforeSet makes a Set a no-op when the stored value already equals
// the new one — avoiding write amplification for unchanged values. It is an
// in-memory, per-instance setting (not persisted) and is mutually exclusive with
// key expiration: it has no effect while expiration is enabled, and enabling
// expiration turns it off (matching MMKV).
func (m *MMKV) EnableCompareBeforeSet() {
	m.lockExclusive()
	defer m.unlockExclusive()
	if m.enableExpire {
		return
	}
	m.compareBeforeSet = true
}

// DisableCompareBeforeSet turns off compare-before-set.
func (m *MMKV) DisableCompareBeforeSet() {
	m.lockExclusive()
	defer m.unlockExclusive()
	m.compareBeforeSet = false
}

// IsCompareBeforeSetEnabled reports whether compare-before-set is on.
func (m *MMKV) IsCompareBeforeSetEnabled() bool {
	m.lockShared()
	defer m.unlockShared()
	return m.compareBeforeSet
}
