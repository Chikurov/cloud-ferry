// Package disk reports free and total space for a destination path.
package disk

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// Usage returns available and total bytes on the filesystem that holds path.
// When path does not exist yet, the nearest existing parent is used, because
// rclone creates the destination directories itself.
func Usage(path string) (free, total uint64, err error) {
	dir, err := existingDir(path)
	if err != nil {
		return 0, 0, err
	}

	var stat syscall.Statfs_t
	if err := syscall.Statfs(dir, &stat); err != nil {
		return 0, 0, fmt.Errorf("statfs %s: %w", dir, err)
	}
	return uint64(stat.Bavail) * uint64(stat.Bsize), uint64(stat.Blocks) * uint64(stat.Bsize), nil
}

// Free returns available bytes on the filesystem that holds path.
func Free(path string) (uint64, error) {
	free, _, err := Usage(path)
	return free, err
}

// TransferLimit returns the number of bytes a transfer may write into a
// filesystem with free bytes available, leaving a tenth of them as a safety
// margin. rclone uses the same value for --max-transfer, so a preflight check
// and the actual start agree on whether the transfer fits.
func TransferLimit(free uint64) uint64 {
	return free - free/10
}

// existingDir walks up from path to the nearest directory that exists.
func existingDir(path string) (string, error) {
	dir := filepath.Clean(path)
	for {
		info, err := os.Stat(dir)
		if err == nil && info.IsDir() {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no existing parent directory for %s", path)
		}
		dir = parent
	}
}
