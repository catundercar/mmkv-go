package mmkv

// NameSpace reads MMKV instances stored under a custom root directory — MMKV's
// "namespace". A namespace is nothing more than a different rootDir; this type
// is sugar so callers don't repeat the path. Instances created by the cgo side
// via GetNameSpace(rootDir).MMKVWithID(id) live at <rootDir>/<encodeFilePath(id)>.
type NameSpace struct {
	rootDir string
}

// OpenNameSpace returns a NameSpace rooted at rootDir.
func OpenNameSpace(rootDir string) NameSpace { return NameSpace{rootDir: rootDir} }

// RootDir returns the namespace root.
func (ns NameSpace) RootDir() string { return ns.rootDir }

// Open opens an instance within the namespace.
func (ns NameSpace) Open(mmapID string, opts ...Option) (*Reader, error) {
	return Open(ns.rootDir, mmapID, opts...)
}

// BackupOne backs up an instance within the namespace to dstDir.
func (ns NameSpace) BackupOne(mmapID, dstDir string) error {
	return BackupOne(ns.rootDir, mmapID, dstDir)
}
