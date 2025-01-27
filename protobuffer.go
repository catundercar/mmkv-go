package mmkvgo

// extracted from: https://github.com/golang/protobuf/blob/v1.5.4/proto/buffer.go

import "google.golang.org/protobuf/encoding/protowire"

// ProtoBuffer is a buffer for encoding and decoding the protobuf wire format.
// It may be reused between invocations to reduce memory usage.
type ProtoBuffer struct {
	buf           []byte
	idx           int
	deterministic bool
}

// NewProtoBuffer allocates a new ProtoBuffer initialized with buf,
// where the contents of buf are considered the unread portion of the buffer.
func NewProtoBuffer(buf []byte) *ProtoBuffer {
	return &ProtoBuffer{buf: buf}
}

// DecodeStringBytes consumes a length-prefixed data bytes from the buffer.
// It does not validate whether the data bytes contain valid UTF-8.
func (b *ProtoBuffer) DecodeStringBytes() (string, error) {
	v, n := protowire.ConsumeString(b.buf[b.idx:])
	if n < 0 {
		return "", protowire.ParseError(n)
	}
	b.idx += n
	return v, nil
}

// DecodeRawBytes consumes a length-prefixed data bytes from the buffer.
// If alloc is specified, it returns a copy the data bytes
// rather than a sub-slice of the buffer.
func (b *ProtoBuffer) DecodeRawBytes(alloc bool) ([]byte, error) {
	v, n := protowire.ConsumeBytes(b.buf[b.idx:])
	if n < 0 {
		return nil, protowire.ParseError(n)
	}
	b.idx += n
	if alloc {
		v = append([]byte(nil), v...)
	}
	return v, nil
}

// Unread returns the unread portion of the buffer.
func (b *ProtoBuffer) Unread() []byte {
	return b.buf[b.idx:]
}

// Position returns the position of the buffer.
func (b *ProtoBuffer) Position() int {
	return b.idx
}
