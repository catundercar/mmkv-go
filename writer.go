//go:build unix

package mmkv

import (
	"fmt"
	"hash/crc32"
	"os"
	"path/filepath"
)

// Writer builds a plaintext MMKV instance from scratch and flushes it in a
// single full write-back — the same operation MMKV performs on its first write
// (empty dict → expandAndWriteBack → doFullWriteBack). It produces a file that
// the official C++ library and the read-only Reader both load: version=4
// (Flag), sequence=1, no encryption, no expiration.
//
// This is the Phase-A write seam. The incremental, lock-coordinated live MMKV
// type (append, transparent reload, multi-process) builds on the same
// encode/meta/memfile/flock foundation. A Writer is not safe for concurrent use
// and does not coordinate with other processes; use it to author or replace a
// file, not to co-write a live one. POSIX-only.
type Writer struct {
	rootDir string
	mmapID  string
	order   []string
	blob    map[string][]byte
}

// NewWriter starts a writer for <rootDir>/<encodeFilePath(mmapID)> (+ ".crc").
func NewWriter(rootDir, mmapID string) *Writer {
	return &Writer{rootDir: rootDir, mmapID: mmapID, blob: map[string][]byte{}}
}

func (w *Writer) set(key string, b []byte) *Writer {
	if _, ok := w.blob[key]; !ok {
		w.order = append(w.order, key)
	}
	w.blob[key] = b
	return w
}

// Typed setters return the Writer for chaining. The last value set for a key
// wins. An empty []byte/"" stores an empty value, which MMKV reads as present
// but zero-length (it is NOT a deletion — that is a separate operation).
func (w *Writer) SetBool(key string, v bool) *Writer       { return w.set(key, boolBlob(v)) }
func (w *Writer) SetInt32(key string, v int32) *Writer     { return w.set(key, int32Blob(v)) }
func (w *Writer) SetInt64(key string, v int64) *Writer     { return w.set(key, int64Blob(v)) }
func (w *Writer) SetUInt32(key string, v uint32) *Writer   { return w.set(key, uint32Blob(v)) }
func (w *Writer) SetUInt64(key string, v uint64) *Writer   { return w.set(key, uint64Blob(v)) }
func (w *Writer) SetFloat32(key string, v float32) *Writer { return w.set(key, float32Blob(v)) }
func (w *Writer) SetFloat64(key string, v float64) *Writer { return w.set(key, float64Blob(v)) }
func (w *Writer) SetString(key, v string) *Writer          { return w.set(key, stringBlob(v)) }
func (w *Writer) SetBytes(key string, v []byte) *Writer    { return w.set(key, bytesBlob(v)) }
func (w *Writer) SetStringSlice(key string, v []string) *Writer {
	return w.set(key, stringSliceBlob(v))
}

// Flush writes the data file and its ".crc" meta to disk via a full write-back,
// then msyncs both (data first, then meta — the order MMKV relies on for crash
// consistency).
func (w *Writer) Flush() error {
	region := encodeRegion(w.order, w.blob)
	actual := uint32(len(region))
	crc := crc32.ChecksumIEEE(region)

	dataPath := dataPathFor(w.rootDir, w.mmapID)
	if err := os.MkdirAll(filepath.Dir(dataPath), 0o777); err != nil {
		return fmt.Errorf("mmkv: mkdir: %w", err)
	}

	// Data file: [0,4) legacy header (left zero for version>=3, like MMKV) +
	// the KV region at [4, 4+actual). The page-rounded tail is free space.
	df, err := openMemoryFile(dataPath, int(actual)+4)
	if err != nil {
		return err
	}
	defer df.close()
	mem := df.memory()
	clear(mem[:4])
	copy(mem[4:4+actual], region)
	if err := df.msync(true); err != nil {
		return err
	}

	// Meta: version=Flag(4), sequence=1, plaintext (zero IV), no flags. The
	// last-confirmed point equals the just-written snapshot.
	m := &metaInfo{
		crcDigest:      crc,
		version:        versionFlag,
		sequence:       1,
		actualSize:     actual,
		lastActualSize: actual,
		lastCRCDigest:  crc,
	}
	mf, err := openMemoryFile(dataPath+crcSuffix, metaSize)
	if err != nil {
		return err
	}
	defer mf.close()
	copy(mf.memory(), m.marshal())
	return mf.msync(true)
}
