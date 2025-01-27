package mmkvgo

import (
	"google.golang.org/protobuf/encoding/protowire"
	"math"
)

type buffer struct {
	data   []byte
	offset int
	err    error
}

func (buf buffer) ToBytes() (v []byte, err error) {
	return buf.consumeBytes()
}

func (buf buffer) ToString() (string, error) {
	byt, err := buf.consumeBytes()
	return string(byt), err
}

func (buf buffer) ToInt32() (int32, error) {
	v, err := buf.consumeVarint()
	return int32(v), err
}

func (buf buffer) ToUInt32() (uint32, error) {
	v, err := buf.consumeVarint()
	return uint32(v), err
}

func (buf buffer) ToInt64() (int64, error) {
	v, err := buf.consumeVarint()
	return int64(v), err
}

func (buf buffer) ToUInt64() (uint64, error) {
	v, err := buf.consumeVarint()
	return v, err
}

func (buf buffer) ToFloat32() (float32, error) {
	raw, err := buf.decodeRaw()
	if err != nil {
		return 0, err
	}
	fixed32, n := protowire.ConsumeFixed32(raw)
	if n < 0 {
		return 0, protowire.ParseError(n)
	}
	return math.Float32frombits(fixed32), err
}

func (buf buffer) ToFloat64() (float64, error) {
	raw, err := buf.decodeRaw()
	if err != nil {
		return 0, err
	}
	fixed64, n := protowire.ConsumeFixed64(raw)
	if n < 0 {
		return 0, protowire.ParseError(n)
	}
	return math.Float64frombits(fixed64), err
}

func (buf buffer) consumeBytes() ([]byte, error) {
	raw, err := buf.decodeRaw()
	if err != nil {
		return nil, err
	}
	v, n := protowire.ConsumeBytes(raw)
	if n < 0 {
		return v, protowire.ParseError(n)
	}
	return v, nil
}

func (buf buffer) consumeVarint() (uint64, error) {
	raw, err := buf.decodeRaw()
	if err != nil {
		return 0, err
	}
	v, n := protowire.ConsumeVarint(raw)
	if n < 0 {
		return 0, protowire.ParseError(n)
	}
	return v, nil
}

func (buf buffer) decodeRaw() ([]byte, error) {
	if buf.err != nil {
		return nil, buf.err
	}
	raw, err := NewProtoBuffer(buf.data[buf.offset:]).DecodeRawBytes(false)
	return raw, err
}
