package mmkv

import (
	"math"
	"testing"
)

func TestReadVarint(t *testing.T) {
	cases := []struct {
		name string
		in   []byte
		want uint64
	}{
		{"zero", []byte{0x00}, 0},
		{"one", []byte{0x01}, 1},
		{"150", []byte{0x96, 0x01}, 150}, // canonical protobuf example
		{"max32", []byte{0xff, 0xff, 0xff, 0xff, 0x0f}, 0xffffffff},
		{"neg1_int32", []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x01}, math.MaxUint64},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ci := newCodedInput(c.in)
			got, err := ci.readVarint64()
			if err != nil {
				t.Fatalf("err: %v", err)
			}
			if got != c.want {
				t.Fatalf("got %d want %d", got, c.want)
			}
			if !ci.atEnd() {
				t.Fatalf("not at end, pos=%d len=%d", ci.pos, len(c.in))
			}
		})
	}
}

func TestReadVarintTruncated(t *testing.T) {
	ci := newCodedInput([]byte{0x96}) // continuation bit set, no next byte
	if _, err := ci.readVarint64(); err == nil {
		t.Fatal("expected error on truncated varint")
	}
}

func TestNegativeInt32(t *testing.T) {
	// -1 as int32 is encoded as a 10-byte sign-extended varint
	ci := newCodedInput([]byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x01})
	v, err := ci.readInt32()
	if err != nil {
		t.Fatal(err)
	}
	if v != -1 {
		t.Fatalf("got %d want -1", v)
	}
}

func TestReadLengthDelimited(t *testing.T) {
	ci := newCodedInput([]byte{0x03, 'a', 'b', 'c', 0x00})
	b, err := ci.readBytes()
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "abc" {
		t.Fatalf("got %q want abc", b)
	}
	// the trailing 0x00 is a zero-length field
	b2, err := ci.readBytes()
	if err != nil {
		t.Fatal(err)
	}
	if len(b2) != 0 {
		t.Fatalf("got %q want empty", b2)
	}
	if !ci.atEnd() {
		t.Fatal("not at end")
	}
}

func TestReadFixed(t *testing.T) {
	// 1.0f little-endian = 00 00 80 3f
	ci := newCodedInput([]byte{0x00, 0x00, 0x80, 0x3f})
	f, err := ci.readFixed32()
	if err != nil {
		t.Fatal(err)
	}
	if math.Float32frombits(f) != 1.0 {
		t.Fatalf("got %v want 1.0", math.Float32frombits(f))
	}

	// 1.0 little-endian = 00 00 00 00 00 00 f0 3f
	ci = newCodedInput([]byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0xf0, 0x3f})
	d, err := ci.readFixed64()
	if err != nil {
		t.Fatal(err)
	}
	if math.Float64frombits(d) != 1.0 {
		t.Fatalf("got %v want 1.0", math.Float64frombits(d))
	}
}

func TestReadBytesOverflow(t *testing.T) {
	// length says 5 but only 2 bytes available
	ci := newCodedInput([]byte{0x05, 'a', 'b'})
	if _, err := ci.readBytes(); err == nil {
		t.Fatal("expected error on truncated length-delimited")
	}
}
