//go:build !windows

package main

import "syscall"

func freeDiskSpace(dir string) (uint64, error) {
	var s syscall.Statfs_t
	if err := syscall.Statfs(dir, &s); err != nil {
		return 0, err
	}
	return s.Bavail * uint64(s.Bsize), nil
}
