//go:build windows

package main

import (
	"fmt"
	"path/filepath"
	"syscall"
	"unsafe"
)

func freeDiskSpace(dir string) (uint64, error) {
	p, e := syscall.UTF16PtrFromString(filepath.Clean(dir))
	if e != nil {
		return 0, e
	}
	var available uint64
	kernel := syscall.NewLazyDLL("kernel32.dll")
	proc := kernel.NewProc("GetDiskFreeSpaceExW")
	ok, _, callErr := proc.Call(uintptr(unsafe.Pointer(p)), uintptr(unsafe.Pointer(&available)), 0, 0)
	if ok == 0 {
		return 0, fmt.Errorf("GetDiskFreeSpaceExW: %v", callErr)
	}
	return available, nil
}
