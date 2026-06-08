package mmkv

import (
	"bytes"
	"encoding/hex"
	"testing"
)

func mustHex(s string) []byte {
	b, err := hex.DecodeString(s)
	if err != nil {
		panic(err)
	}
	return b
}

// TestAESCFBKnownVectors checks the AES-CFB-128 decrypt against the NIST
// SP800-38A known-answer vectors (4 blocks, so multi-block feedback is
// exercised), for both AES-128 and AES-256. Pure Go, no cgo, version-independent.
func TestAESCFBKnownVectors(t *testing.T) {
	iv := mustHex("000102030405060708090a0b0c0d0e0f")
	plaintext := mustHex(
		"6bc1bee22e409f96e93d7e117393172a" +
			"ae2d8a571e03ac9c9eb76fac45af8e51" +
			"30c81c46a35ce411e5fbc1191a0a52ef" +
			"f69f2445df4f9b17ad2b417be66c3710")

	cases := []struct{ name, key, ciphertext string }{
		{
			"AES-128 (NIST SP800-38A F.3.13)",
			"2b7e151628aed2a6abf7158809cf4f3c",
			"3b3fd92eb72dad20333449f8e83cfb4a" +
				"c8a64537a0b3a93fcde3cdad9f1ce58b" +
				"26751f67a3cbb140b1808cf187a4f4df" +
				"c04b05357c5d1c0eeac4c66f9ff7f2e6",
		},
		{
			"AES-256 (NIST SP800-38A F.3.17)",
			"603deb1015ca71be2b73aef0857d77811f352c073b6108d72d9810a30914dff4",
			"dc7e84bfda79164b7ecd8486985d3860" +
				"39ffed143b28b1c832113c6331e5407b" +
				"df10132415e54b92a13ed0a8267ae2f9" +
				"75a385741ab9cef82031623d55b1e471",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := newAESCFB(mustHex(c.key)).Decrypt(mustHex(c.ciphertext), iv)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, plaintext) {
				t.Fatalf("decrypt mismatch:\n got %x\nwant %x", got, plaintext)
			}
		})
	}

	if _, err := newAESCFB(mustHex(cases[0].key)).Decrypt([]byte{1, 2, 3}, []byte{0, 0}); err == nil {
		t.Fatal("expected error on short IV")
	}
}
