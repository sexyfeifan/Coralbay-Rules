package main

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// Keep the inode in place: both the sync script and rollback lock this file.
// The kernel releases the lock even after SIGKILL or a container restart.
func (s *server) lockRelease() (*os.File, error) {
	f, err := os.OpenFile(filepath.Join(s.dataDir, ".publish.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}
