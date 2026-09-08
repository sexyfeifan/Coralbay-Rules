//go:build linux

package main

import "syscall"

func routingFilesystemSpace(path string) (uint64, uint64, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, 0, err
	}
	return routingFilesystemBytes(stat.Blocks, stat.Bavail, stat.Bsize, stat.Frsize)
}
