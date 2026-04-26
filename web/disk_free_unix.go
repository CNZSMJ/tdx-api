//go:build !windows

package main

import "syscall"

func diskFreeBytesForPath(path string) (int64, error) {
	target := existingPathForStat(path)
	var stat syscall.Statfs_t
	if err := syscall.Statfs(target, &stat); err != nil {
		return 0, err
	}
	return int64(stat.Bavail) * int64(stat.Bsize), nil
}
