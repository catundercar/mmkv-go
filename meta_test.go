package mmkv

import (
	"encoding/binary"
	"testing"
)

func TestParseMeta(t *testing.T) {
	buf := make([]byte, metaSize)
	binary.LittleEndian.PutUint32(buf[0:], 0xDEADBEEF) // crcDigest
	binary.LittleEndian.PutUint32(buf[4:], 4)          // version (Flag)
	binary.LittleEndian.PutUint32(buf[8:], 7)          // sequence
	for i := 0; i < 16; i++ {
		buf[12+i] = byte(i + 1) // IV
	}
	binary.LittleEndian.PutUint32(buf[28:], 12345)                     // actualSize
	binary.LittleEndian.PutUint64(buf[104:], uint64(flagEnableExpire)) // flags

	m, err := parseMeta(buf)
	if err != nil {
		t.Fatal(err)
	}
	if m.crcDigest != 0xDEADBEEF {
		t.Errorf("crc: got %#x", m.crcDigest)
	}
	if m.version != 4 {
		t.Errorf("version: got %d", m.version)
	}
	if m.sequence != 7 {
		t.Errorf("sequence: got %d", m.sequence)
	}
	if m.actualSize != 12345 {
		t.Errorf("actualSize: got %d", m.actualSize)
	}
	if m.iv[0] != 1 || m.iv[15] != 16 {
		t.Errorf("iv: got %v", m.iv)
	}
	if !m.expireEnabled() {
		t.Errorf("expected expire flag set")
	}
}

func TestParseMetaNoExpire(t *testing.T) {
	buf := make([]byte, metaSize)
	binary.LittleEndian.PutUint32(buf[4:], 4)
	binary.LittleEndian.PutUint64(buf[104:], 0)
	m, err := parseMeta(buf)
	if err != nil {
		t.Fatal(err)
	}
	if m.expireEnabled() {
		t.Errorf("did not expect expire flag")
	}
}

func TestParseMetaTooShort(t *testing.T) {
	if _, err := parseMeta(make([]byte, 8)); err == nil {
		t.Fatal("expected error for short meta")
	}
}
