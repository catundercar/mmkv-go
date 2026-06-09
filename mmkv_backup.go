//go:build unix

package mmkv

import (
	"fmt"
	"os"
	"path/filepath"
)

// RestoreOneFromDirectory restores one instance (data + ".crc") from srcDir into
// rootDir, overwriting in place. It holds an exclusive flock on the destination
// ".crc" for the whole copy (interlocking with multi-process instances and other
// processes) and writes with O_TRUNC, which preserves the inode so a live mmap
// stays valid — then it flags any cached instance to remap+reload on its next
// call. The pure-Go counterpart to BackupOne.
//
// Coordination: a SINGLE-process instance takes no flock, so don't restore over
// one that is being concurrently accessed; restore over multi-process instances
// or while the file is idle (matches MMKV's restore contract).
func RestoreOneFromDirectory(rootDir, mmapID, srcDir string) error {
	src := dataPathFor(srcDir, mmapID)
	dst := dataPathFor(rootDir, mmapID)
	if _, err := os.Stat(src); err != nil {
		return fmt.Errorf("mmkv: backup source missing: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}

	lf, err := os.OpenFile(dst+crcSuffix, os.O_RDWR|os.O_CREATE, 0o666)
	if err != nil {
		return fmt.Errorf("mmkv: open dst .crc: %w", err)
	}
	defer lf.Close()
	if err := flockExclusive(lf); err != nil {
		return err
	}
	defer flockUnlock(lf)

	// data first, then .crc (consistent order; both in place via O_TRUNC).
	if err := copyFile(src, dst); err != nil {
		return err
	}
	if err := copyFile(src+crcSuffix, dst+crcSuffix); err != nil {
		return err
	}

	// a cached instance must remap+reload (the needLoad branch of checkLoadData
	// remaps, which is required if the restore shrank the file).
	key := registryKey(rootDir, mmapID)
	gInstanceMu.Lock()
	if m := gInstances[key]; m != nil {
		m.mu.Lock()
		m.needLoad = true
		m.mu.Unlock()
	}
	gInstanceMu.Unlock()
	return nil
}

// RestoreOne restores an instance within the namespace from srcDir.
func (ns NameSpace) RestoreOne(mmapID, srcDir string) error {
	return RestoreOneFromDirectory(ns.rootDir, mmapID, srcDir)
}
