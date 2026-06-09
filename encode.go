package mmkv

import (
	"encoding/binary"
	"math"
)

// codedOutput encodes MMKV's protobuf-style wire format — the inverse of
// codedInput (wire.go). Multi-byte integers are LEB128 varints; fixed32/64 are
// little-endian; length-delimited fields are varint(len)+bytes. It mirrors
// MMKV's CodedOutputData (Core/CodedOutputData.cpp) byte for byte so files we
// write are read back identically by the official C++ library.
type codedOutput struct{ buf []byte }

func (o *codedOutput) bytes() []byte { return o.buf }

func (o *codedOutput) writeRawByte(b byte) { o.buf = append(o.buf, b) }

func (o *codedOutput) writeRawData(b []byte) { o.buf = append(o.buf, b...) }

// writeRawVarint32 writes a uint32 as LEB128 (1–5 bytes).
func (o *codedOutput) writeRawVarint32(v uint32) {
	for v >= 0x80 {
		o.buf = append(o.buf, byte(v)|0x80)
		v >>= 7
	}
	o.buf = append(o.buf, byte(v))
}

// writeRawVarint64 writes a uint64 as LEB128 (1–10 bytes).
func (o *codedOutput) writeRawVarint64(v uint64) {
	for v >= 0x80 {
		o.buf = append(o.buf, byte(v)|0x80)
		v >>= 7
	}
	o.buf = append(o.buf, byte(v))
}

func (o *codedOutput) writeRawLittleEndian32(v uint32) {
	o.buf = binary.LittleEndian.AppendUint32(o.buf, v)
}

func (o *codedOutput) writeRawLittleEndian64(v uint64) {
	o.buf = binary.LittleEndian.AppendUint64(o.buf, v)
}

// writeData writes a length-delimited field: varint(len) + raw bytes
// (== CodedOutputData::writeData / writeString — no UTF-8 transform).
func (o *codedOutput) writeData(b []byte) {
	o.writeRawVarint32(uint32(len(b)))
	o.writeRawData(b)
}

func (o *codedOutput) writeBool(v bool) {
	if v {
		o.writeRawByte(1)
	} else {
		o.writeRawByte(0)
	}
}

// writeInt32 matches MMKV: a non-negative value is a varint32, a negative value
// is sign-extended to 64 bits and written as a 10-byte varint
// (CodedOutputData::writeInt32). Getting this wrong is a classic interop bug.
func (o *codedOutput) writeInt32(v int32) {
	if v >= 0 {
		o.writeRawVarint32(uint32(v))
	} else {
		o.writeRawVarint64(uint64(int64(v)))
	}
}

func (o *codedOutput) writeUInt32(v uint32) { o.writeRawVarint32(v) }
func (o *codedOutput) writeInt64(v int64)   { o.writeRawVarint64(uint64(v)) }
func (o *codedOutput) writeUInt64(v uint64) { o.writeRawVarint64(v) }

func (o *codedOutput) writeFloat32(v float32) { o.writeRawLittleEndian32(math.Float32bits(v)) }
func (o *codedOutput) writeFloat64(v float64) { o.writeRawLittleEndian64(math.Float64bits(v)) }

// Value blobs: the bytes stored as a KV value, before the pair's outer length
// prefix. Scalars are single-wrapped (the outer writeData in the pair adds the
// only length prefix). string/[]byte are double-wrapped — the blob itself is
// varint(len)+raw, so the pair's outer writeData yields an outer+inner length
// (this is MMKV's isDataHolder=true path; see MMKV_IO.cpp doAppendDataWithKey).
// These are the exact inverse of reader.go's snapshot.value / bytesValue.

func boolBlob(v bool) []byte       { o := &codedOutput{}; o.writeBool(v); return o.buf }
func int32Blob(v int32) []byte     { o := &codedOutput{}; o.writeInt32(v); return o.buf }
func int64Blob(v int64) []byte     { o := &codedOutput{}; o.writeInt64(v); return o.buf }
func uint32Blob(v uint32) []byte   { o := &codedOutput{}; o.writeUInt32(v); return o.buf }
func uint64Blob(v uint64) []byte   { o := &codedOutput{}; o.writeUInt64(v); return o.buf }
func float32Blob(v float32) []byte { o := &codedOutput{}; o.writeFloat32(v); return o.buf }
func float64Blob(v float64) []byte { o := &codedOutput{}; o.writeFloat64(v); return o.buf }
func bytesBlob(b []byte) []byte    { o := &codedOutput{}; o.writeData(b); return o.buf }
func stringBlob(s string) []byte   { return bytesBlob([]byte(s)) }

// itemSizeHolder is a uint32 that encodes to a 4-byte varint (0x200000 → 80 80
// 80 01). MMKV writes a randomized placeholder of fixed byte-length
// (ItemSizeHolderSize=4) at the start of the KV region and discards it on read
// (MiniPBCoder::decodeOneMap reads one varint and ignores it). A fixed value is
// fine for plaintext; encryption (later) wants it randomized.
const itemSizeHolder = 0x200000

// stringSliceBlob encodes a []string as MMKV's vector<string> value: a
// length-delimited buffer of repeated writeString entries. MMKV's decodeOneVector
// reads a leading aggregate-size varint then loops readString to the end; that
// aggregate size equals the byte length of the concatenated entries, so the blob
// is exactly writeData(concat(writeData(s))) — structurally a length-delimited
// payload (MiniPBCoder vector encode/decode).
func stringSliceBlob(v []string) []byte {
	items := &codedOutput{}
	for _, s := range v {
		items.writeData([]byte(s))
	}
	return bytesBlob(items.bytes())
}

// decodeStringSlice parses the inner items buffer (concatenated writeString
// entries) back into a []string. Best-effort: stops at the first malformed entry.
func decodeStringSlice(items []byte) []string {
	ci := newCodedInput(items)
	out := []string{}
	for !ci.atEnd() {
		s, err := ci.readBytes()
		if err != nil {
			break
		}
		out = append(out, string(s))
	}
	return out
}

// encodeRegion builds the KV-log region: the leading 4-byte placeholder followed
// by each pair as writeData(key) + writeData(valueBlob), in order. This is the
// inverse of reader.go's parseDict.
func encodeRegion(order []string, blob map[string][]byte) []byte {
	o := &codedOutput{}
	o.writeUInt32(itemSizeHolder)
	for _, k := range order {
		o.writeData([]byte(k))
		o.writeData(blob[k])
	}
	return o.buf
}
