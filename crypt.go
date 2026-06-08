package mmkv

import (
	"crypto/aes"
	"crypto/cipher"
	"errors"
)

// Decryptor turns the on-disk (encrypted) data region into plaintext wire bytes.
// iv is the per-file IV from the meta file (.crc m_vector, 16 bytes) — it can
// change when the writer does a full write-back, so it is passed on every reload.
// Inject a custom one via WithDecryptor, or use WithEncryption for standard MMKV
// AES (AES-CFB-128; AES-128 or AES-256 chosen by key length).
type Decryptor interface {
	Decrypt(ciphertext, iv []byte) (plaintext []byte, err error)
}

// WithDecryptor supplies a custom decryptor for encrypted files.
func WithDecryptor(d Decryptor) Option { return func(r *Reader) { r.dec = d } }

// WithEncryption decrypts files written with an MMKV cryptKey. The AES width
// follows MMKV: a key longer than 16 bytes selects AES-256, otherwise AES-128;
// the key is truncated or zero-padded to 16/32 bytes. Pass the SAME key the
// writer used.
func WithEncryption(key []byte) Option {
	return func(r *Reader) { r.dec = newAESCFB(key) }
}

var errShortIV = errors.New("mmkv: IV shorter than 16 bytes")

// aesCFB implements MMKV's AES-CFB-128 (Core/aes/AESCrypt.cpp → OpenSSL CFB128).
type aesCFB struct{ block cipher.Block }

func newAESCFB(userKey []byte) *aesCFB {
	n := 16
	if len(userKey) > 16 {
		n = 32 // MMKV: keyLength > AES_KEY_LEN(16) selects AES-256
	}
	k := make([]byte, n)
	copy(k, userKey)         // truncate or zero-pad, matching AESCrypt's key setup
	b, _ := aes.NewCipher(k) // a 16/32-byte key never errors
	return &aesCFB{block: b}
}

// Decrypt runs CFB-128 over the whole region from iv (offset 0), matching MMKV's
// single contiguous stream. CFB-128 decrypt: out[i] = keystream[i%16] ^ ct[i],
// then the ciphertext byte is fed back into the register; the register is
// re-encrypted every 16 bytes.
func (a *aesCFB) Decrypt(ct, iv []byte) ([]byte, error) {
	if len(iv) < 16 {
		return nil, errShortIV
	}
	out := make([]byte, len(ct))
	var reg, ks [16]byte
	copy(reg[:], iv[:16])
	n := 0
	for i := 0; i < len(ct); i++ {
		if n == 0 {
			a.block.Encrypt(ks[:], reg[:])
		}
		c := ct[i]
		out[i] = c ^ ks[n]
		reg[n] = c
		n = (n + 1) & 15
	}
	return out, nil
}
