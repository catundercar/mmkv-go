//go:build unix

package mmkv

import (
	"encoding/binary"
	"hash/crc32"
	"os"
)

// MMKVWithID opens a read+write instance within the namespace (custom root dir).
func (ns NameSpace) MMKVWithID(mmapID string, opts ...MMKVOption) (*MMKV, error) {
	return MMKVWithID(ns.rootDir, mmapID, opts...)
}

// CheckExist reports whether both the data file and its ".crc" exist.
func CheckExist(rootDir, mmapID string) bool {
	dp := dataPathFor(rootDir, mmapID)
	if _, err := os.Stat(dp); err != nil {
		return false
	}
	_, err := os.Stat(dp + crcSuffix)
	return err == nil
}

// IsFileValid reports whether the instance on disk is structurally valid: the
// meta parses to a supported version and the data region passes its CRC. Like
// MMKV's isFileValid, this is a structural check over the raw region (the CRC is
// over the ciphertext when encrypted), so it needs no key.
func IsFileValid(rootDir, mmapID string) bool {
	dp := dataPathFor(rootDir, mmapID)
	metaBuf, err := os.ReadFile(dp + crcSuffix)
	if err != nil {
		return false
	}
	mi, err := parseMeta(metaBuf)
	if err != nil || mi.version > maxSupportedVersion {
		return false
	}
	data, err := os.ReadFile(dp)
	if err != nil {
		return false
	}
	var actual uint32
	if mi.version >= versionActualSize {
		actual = mi.actualSize
	} else if len(data) >= 4 {
		actual = binary.LittleEndian.Uint32(data)
	}
	if int(actual)+4 > len(data) {
		return false
	}
	return crc32.ChecksumIEEE(data[4:4+actual]) == mi.crcDigest
}

// RemoveStorage closes any cached instance for the file and unlinks both the data
// file and its ".crc" (matching MMKV's removeStorage: close, then delete).
func RemoveStorage(rootDir, mmapID string) error {
	key := registryKey(rootDir, mmapID)
	gInstanceMu.Lock()
	m := gInstances[key]
	gInstanceMu.Unlock()
	if m != nil {
		_ = m.Close() // removes from the registry and unmaps
	}
	dp := dataPathFor(rootDir, mmapID)
	e1 := os.Remove(dp)
	e2 := os.Remove(dp + crcSuffix)
	if e1 != nil && !os.IsNotExist(e1) {
		return e1
	}
	if e2 != nil && !os.IsNotExist(e2) {
		return e2
	}
	return nil
}
