package main

import (
	"syscall"
	"unsafe"
)

func diskFree(path string) (uint64, error) {
	k := syscall.MustLoadDLL("kernel32.dll")
	p := k.MustFindProc("GetDiskFreeSpaceExW")
	var free, total, avail uint64
	u, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	r, _, e := p.Call(uintptr(unsafe.Pointer(u)), uintptr(unsafe.Pointer(&free)), uintptr(unsafe.Pointer(&total)), uintptr(unsafe.Pointer(&avail)))
	if r == 0 {
		return 0, e
	}
	return free, nil
}
