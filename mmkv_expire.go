//go:build unix

package mmkv

import "encoding/binary"

// ExpireNever, passed to EnableAutoKeyExpire, enables expiration without a common
// duration: keys never expire unless given a per-set duration (matches MMKV).
const ExpireNever uint32 = 0

// appendExpire returns blob with a trailing 4-byte little-endian expire
// timestamp: 0 (never) when seconds == 0, else now+seconds.
func appendExpire(blob []byte, seconds uint32) []byte {
	var ts uint32
	if seconds != 0 {
		ts = nowUnix() + seconds
	}
	out := make([]byte, len(blob)+4)
	copy(out, blob)
	binary.LittleEndian.PutUint32(out[len(blob):], ts)
	return out
}

// EnableAutoKeyExpire turns on key expiration with a default per-set duration in
// seconds (0 = ExpireNever, i.e. keys expire only with a per-set duration). It
// persists the flag in the meta and, on first enable, appends a never-expire
// timestamp to every existing value via a full write-back — so the on-disk
// format matches MMKV's enableAutoKeyExpire.
func (m *MMKV) EnableAutoKeyExpire(seconds uint32) error {
	m.lockExclusive()
	defer m.unlockExclusive()
	if m.closed {
		return ErrClosed
	}
	m.checkLoadData()

	wasEnabled := m.enableExpire
	m.expiredSeconds = seconds
	m.enableExpire = true
	m.compareBeforeSet = false // mutually exclusive (matches MMKV)
	m.info.flags |= flagEnableExpire

	if !wasEnabled {
		// existing values gain a trailing 0 (never) timestamp
		for k, v := range m.dict {
			nv := make([]byte, len(v)+4) // last 4 bytes zero = never
			copy(nv, v)
			m.dict[k] = nv
		}
	}
	return m.fullWriteback()
}

// DisableAutoKeyExpire turns off expiration: it drops already-expired keys,
// strips the trailing timestamp from the rest, clears the meta flag, and rewrites.
func (m *MMKV) DisableAutoKeyExpire() error {
	m.lockExclusive()
	defer m.unlockExclusive()
	if m.closed {
		return ErrClosed
	}
	m.checkLoadData()
	if !m.enableExpire {
		return nil
	}

	stripped := make(map[string][]byte, len(m.dict))
	for k := range m.dict {
		v, ok := m.value(k) // strips the timestamp and filters expired
		if !ok {
			continue
		}
		stripped[k] = append([]byte(nil), v...)
	}
	m.dict = stripped
	m.enableExpire = false
	m.expiredSeconds = 0
	m.info.flags &^= flagEnableExpire
	return m.fullWriteback()
}

// IsExpirationEnabled reports whether auto key expiration is on.
func (m *MMKV) IsExpirationEnabled() bool {
	m.lockShared()
	defer m.unlockShared()
	return m.enableExpire
}
