package mmkv

import (
	"crypto/md5"
	"encoding/hex"
	"path/filepath"
	"strings"
)

// MMKV file naming (Core/MMKV.cpp encodeFilePath): a normal mmapID maps to a
// file named exactly mmapID; an mmapID containing any of these special
// characters maps to "specialCharacter/<md5(mmapID)>" instead. The same rule
// applies under the default root and under a namespace (custom) root — a
// namespace is just a different rootDir.
const (
	specialCharDir = "specialCharacter"
	specialChars   = `\/:*?"<>|`
	crcSuffix      = ".crc"
)

func encodeFilePath(mmapID string) string {
	if strings.ContainsAny(mmapID, specialChars) {
		sum := md5.Sum([]byte(mmapID))
		return filepath.Join(specialCharDir, hex.EncodeToString(sum[:]))
	}
	return mmapID
}

func dataPathFor(rootDir, mmapID string) string {
	return filepath.Join(rootDir, encodeFilePath(mmapID))
}

func (r *Reader) dataPath() string { return dataPathFor(r.rootDir, r.mmapID) }
func (r *Reader) metaPath() string { return r.dataPath() + crcSuffix }
