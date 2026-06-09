//go:build unix

package mmkv

import "crypto/rand"

// randomIV returns a fresh 16-byte AES IV (rotated on every encrypted full
// write-back, like MMKV's fillRandomIV).
func randomIV() ([16]byte, error) {
	var iv [16]byte
	_, err := rand.Read(iv[:])
	return iv, err
}

// ReKey changes the encryption key, re-encrypting the whole file via a full
// write-back (with a fresh IV). It also converts between plaintext and
// encrypted: pass a non-empty key to encrypt (or change the key), or an empty
// key to decrypt to plaintext. The new key takes effect only if the rewrite
// succeeds. AES width follows the key length (>16 bytes ⇒ AES-256).
func (m *MMKV) ReKey(newKey []byte) error {
	m.lockExclusive()
	defer m.unlockExclusive()
	if m.closed {
		return ErrClosed
	}
	if m.readOnly {
		return ErrReadOnly
	}
	m.checkLoadData() // decrypts with the current key into dict (plaintext views)

	prev := m.crypt
	if len(newKey) == 0 {
		m.crypt = nil
	} else {
		m.crypt = newAESCFB(newKey)
	}
	if err := m.fullWriteback(); err != nil {
		m.crypt = prev // roll back the in-memory key on failure
		return err
	}
	return nil
}

// IsEncryptionEnabled reports whether the instance is AES-encrypted.
func (m *MMKV) IsEncryptionEnabled() bool {
	m.lockShared()
	defer m.unlockShared()
	return m.crypt != nil
}
