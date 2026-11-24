//go:build linux

package main

import "syscall"

// filesystemSize is the size of the file system holding path and the space
// on it free to this user.
func filesystemSize(path string) (total, free uint64, err error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, 0, err
	}
	return stat.Blocks * uint64(stat.Bsize), stat.Bavail * uint64(stat.Bsize), nil
}
