//go:build !windows

package lifecycle

import (
	"errors"
	"os"
	"syscall"
)

func diskFreeBytes(path string) (int64, error) {
	if path == "" {
		path = "."
	}
	if err := os.MkdirAll(path, 0o755); err != nil {
		return 0, err
	}
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, err
	}
	return int64(stat.Bavail) * int64(stat.Bsize), nil
}

func filesystemDeviceID(path string) (uint64, error) {
	stat, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	sys, ok := stat.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, errors.New("unsupported stat type")
	}
	return uint64(sys.Dev), nil
}
