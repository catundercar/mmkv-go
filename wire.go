package mmkv

import (
	"encoding/binary"
	"errors"
	"io"
)

// codedInput decodes MMKV's protobuf-style wire format over a byte buffer.
// All multi-byte ints are LEB128 varints; fixed32/64 are little-endian;
// strings/data are length-delimited (varint length + raw bytes).
type codedInput struct {
	buf []byte
	pos int
}

func newCodedInput(b []byte) *codedInput { return &codedInput{buf: b} }

func (c *codedInput) atEnd() bool { return c.pos >= len(c.buf) }

var errVarintOverflow = errors.New("mmkv: varint overflows 64 bits")

// readVarint64 reads a base-128 varint (up to 10 bytes, matching protobuf /
// MMKV sign-extended encoding for negative 32-bit values).
func (c *codedInput) readVarint64() (uint64, error) {
	var x uint64
	var s uint
	for i := 0; i < 10; i++ {
		if c.pos >= len(c.buf) {
			return 0, io.ErrUnexpectedEOF
		}
		b := c.buf[c.pos]
		c.pos++
		if b < 0x80 {
			return x | uint64(b)<<s, nil
		}
		x |= uint64(b&0x7f) << s
		s += 7
	}
	return 0, errVarintOverflow
}

// readBytes reads a length-delimited field: varint length then that many bytes.
// The returned slice is a view into the underlying buffer (no copy).
func (c *codedInput) readBytes() ([]byte, error) {
	n, err := c.readVarint64()
	if err != nil {
		return nil, err
	}
	if n > uint64(len(c.buf)-c.pos) {
		return nil, io.ErrUnexpectedEOF
	}
	start := c.pos
	c.pos += int(n)
	return c.buf[start:c.pos], nil
}

func (c *codedInput) readFixed32() (uint32, error) {
	if c.pos+4 > len(c.buf) {
		return 0, io.ErrUnexpectedEOF
	}
	v := binary.LittleEndian.Uint32(c.buf[c.pos:])
	c.pos += 4
	return v, nil
}

func (c *codedInput) readFixed64() (uint64, error) {
	if c.pos+8 > len(c.buf) {
		return 0, io.ErrUnexpectedEOF
	}
	v := binary.LittleEndian.Uint64(c.buf[c.pos:])
	c.pos += 8
	return v, nil
}

func (c *codedInput) readInt32() (int32, error) {
	v, err := c.readVarint64()
	return int32(v), err
}

func (c *codedInput) readUInt32() (uint32, error) {
	v, err := c.readVarint64()
	return uint32(v), err
}

func (c *codedInput) readBool() (bool, error) {
	v, err := c.readVarint64()
	return v != 0, err
}
