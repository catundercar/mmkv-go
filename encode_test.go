package mmkv

import (
	"bytes"
	"math"
	"testing"
)

func TestWriteRawVarint32(t *testing.T) {
	cases := []struct {
		v    uint32
		want []byte
	}{
		{0, []byte{0x00}},
		{1, []byte{0x01}},
		{127, []byte{0x7f}},
		{128, []byte{0x80, 0x01}},
		{300, []byte{0xac, 0x02}},
		{itemSizeHolder, []byte{0x80, 0x80, 0x80, 0x01}}, // must encode to exactly 4 bytes
		{math.MaxUint32, []byte{0xff, 0xff, 0xff, 0xff, 0x0f}},
	}
	for _, c := range cases {
		o := &codedOutput{}
		o.writeRawVarint32(c.v)
		if !bytes.Equal(o.bytes(), c.want) {
			t.Errorf("varint32(%d) = % x, want % x", c.v, o.bytes(), c.want)
		}
	}
}

// TestNegativeInt32TenBytes pins the classic MMKV interop footgun: a negative
// int32 is sign-extended to 64 bits and written as a 10-byte varint.
func TestNegativeInt32TenBytes(t *testing.T) {
	for _, v := range []int32{-1, math.MinInt32, -42} {
		o := &codedOutput{}
		o.writeInt32(v)
		if len(o.bytes()) != 10 {
			t.Errorf("int32(%d) encoded to %d bytes, want 10", v, len(o.bytes()))
		}
	}
	// non-negative stays compact
	o := &codedOutput{}
	o.writeInt32(42)
	if len(o.bytes()) != 1 {
		t.Errorf("int32(42) = %d bytes, want 1", len(o.bytes()))
	}
}

func TestWriteData(t *testing.T) {
	o := &codedOutput{}
	o.writeData([]byte("hi"))
	if want := []byte{0x02, 'h', 'i'}; !bytes.Equal(o.bytes(), want) {
		t.Errorf("writeData = % x, want % x", o.bytes(), want)
	}
}

func TestFloatLittleEndian(t *testing.T) {
	o := &codedOutput{}
	o.writeFloat32(1.5) // bits 0x3FC00000
	if want := []byte{0x00, 0x00, 0xc0, 0x3f}; !bytes.Equal(o.bytes(), want) {
		t.Errorf("float32(1.5) = % x, want % x", o.bytes(), want)
	}
}

// TestBlobRoundTrip checks every value blob decodes back via codedInput (the
// reader's primitives) to the original — encode is the exact inverse of decode.
func TestBlobRoundTrip(t *testing.T) {
	t.Run("bool", func(t *testing.T) {
		for _, v := range []bool{true, false} {
			got, err := newCodedInput(boolBlob(v)).readBool()
			if err != nil || got != v {
				t.Fatalf("bool %v -> %v err=%v", v, got, err)
			}
		}
	})
	t.Run("int32", func(t *testing.T) {
		for _, v := range []int32{0, 1, -1, math.MinInt32, math.MaxInt32} {
			got, err := newCodedInput(int32Blob(v)).readInt32()
			if err != nil || got != v {
				t.Fatalf("int32 %d -> %d err=%v", v, got, err)
			}
		}
	})
	t.Run("int64", func(t *testing.T) {
		for _, v := range []int64{0, 1, -1, math.MinInt64, math.MaxInt64} {
			x, err := newCodedInput(int64Blob(v)).readVarint64()
			if err != nil || int64(x) != v {
				t.Fatalf("int64 %d -> %d err=%v", v, int64(x), err)
			}
		}
	})
	t.Run("uint64", func(t *testing.T) {
		for _, v := range []uint64{0, 1, math.MaxUint64} {
			x, err := newCodedInput(uint64Blob(v)).readVarint64()
			if err != nil || x != v {
				t.Fatalf("uint64 %d -> %d err=%v", v, x, err)
			}
		}
	})
	t.Run("float32", func(t *testing.T) {
		for _, v := range []float32{0, 1.5, -2.5, math.MaxFloat32} {
			bits, err := newCodedInput(float32Blob(v)).readFixed32()
			if err != nil || math.Float32frombits(bits) != v {
				t.Fatalf("float32 %v -> %v err=%v", v, math.Float32frombits(bits), err)
			}
		}
	})
	t.Run("float64", func(t *testing.T) {
		for _, v := range []float64{0, 1.5, -2.5, math.MaxFloat64} {
			bits, err := newCodedInput(float64Blob(v)).readFixed64()
			if err != nil || math.Float64frombits(bits) != v {
				t.Fatalf("float64 %v -> %v err=%v", v, math.Float64frombits(bits), err)
			}
		}
	})
	t.Run("bytes_double_wrap", func(t *testing.T) {
		// the blob is varint(len)+raw; readBytes strips that inner layer, like
		// reader.go bytesValue does after parseDict strips the outer one.
		raw := []byte("payload")
		got, err := newCodedInput(bytesBlob(raw)).readBytes()
		if err != nil || !bytes.Equal(got, raw) {
			t.Fatalf("bytes %q -> %q err=%v", raw, got, err)
		}
	})
}

// TestEncodeRegionParseDict checks the region builder is the inverse of the
// reader's parseDict (the whole-file decode), per type.
func TestEncodeRegionParseDict(t *testing.T) {
	order := []string{"i", "s", "b"}
	blob := map[string][]byte{
		"i": int32Blob(-7),
		"s": stringBlob("héllo"),
		"b": bytesBlob([]byte{1, 2, 3}),
	}
	m, err := parseDict(encodeRegion(order, blob))
	if err != nil {
		t.Fatalf("parseDict: %v", err)
	}
	if len(m) != 3 {
		t.Fatalf("got %d keys, want 3", len(m))
	}
	if got, _ := newCodedInput(m["i"]).readInt32(); got != -7 {
		t.Errorf("i = %d, want -7", got)
	}
	if got, _ := newCodedInput(m["s"]).readBytes(); string(got) != "héllo" {
		t.Errorf("s = %q, want héllo", got)
	}
	if got, _ := newCodedInput(m["b"]).readBytes(); !bytes.Equal(got, []byte{1, 2, 3}) {
		t.Errorf("b = % x, want 01 02 03", got)
	}
}
