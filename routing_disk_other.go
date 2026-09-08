//go:build !linux

package main

import "syscall"

func routingFilesystemSpace(path string) (uint64, uint64, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, 0, err
	}
	// Darwin's Statfs_t has no Frsize; Bsize is its block-counter unit.
	return routingFilesystemBytes(stat.Blocks, stat.Bavail, int64(stat.Bsize), 0)
}
