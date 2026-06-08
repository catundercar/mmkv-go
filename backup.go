package mmkv

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// BackupOne copies one MMKV instance (data file + ".crc") from rootDir to
// dstDir, producing a consistent, independently-readable snapshot. It holds a
// shared flock on ".crc" during the copy, so it is safe to run while an MMKV
// writer (opened MMKV_MULTI_PROCESS) is active — the writer's exclusive lock and
// this shared lock interlock. POSIX-only (matches MMKV's flock).
//
// Backup is encryption-agnostic: it copies raw bytes, so encrypted instances
// back up fine without a key. Restore is a write operation and is intentionally
// out of scope — use the official cgo library's RestoreOneFromDirectory from the
// writer process.
func BackupOne(rootDir, mmapID, dstDir string) error {
	src := dataPathFor(rootDir, mmapID)
	dst := dataPathFor(dstDir, mmapID)

	lf, err := os.Open(src + crcSuffix)
	if err != nil {
		return fmt.Errorf("mmkv: open .crc: %w", err)
	}
	defer lf.Close()
	if err := flockShared(lf); err != nil {
		return err
	}
	defer flockUnlock(lf)

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	// Copy data first, then .crc — same order as MMKV; under the shared lock the
	// two are mutually consistent.
	if err := copyFile(src, dst); err != nil {
		return err
	}
	if err := copyFile(src+crcSuffix, dst+crcSuffix); err != nil {
		return err
	}
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
