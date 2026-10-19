//go:build windows

// disk_windows.go -- free space, and which drives to ask about.
//
// The standard library has no portable call for this, so each half of the
// recovery gets its own twenty lines. No cgo: kernel32 is reached through
// syscall, the same way the rest of this project talks to Windows.
package main

import (
	"syscall"
	"unsafe"
)

var (
	kernel32         = syscall.NewLazyDLL("kernel32.dll")
	procGetDiskFree  = kernel32.NewProc("GetDiskFreeSpaceExW")
	procGetLogical   = kernel32.NewProc("GetLogicalDrives")
	procGetDriveType = kernel32.NewProc("GetDriveTypeW")
)

func freeSpace(path string) (uint64, error) {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	var freeToCaller, total, free uint64
	r, _, e := procGetDiskFree.Call(uintptr(unsafe.Pointer(p)),
		uintptr(unsafe.Pointer(&freeToCaller)),
		uintptr(unsafe.Pointer(&total)),
		uintptr(unsafe.Pointer(&free)))
	if r == 0 {
		return 0, e
	}
	return freeToCaller, nil
}

// volumes lists the fixed drives. A restore lands on one of them, and the
// RAM disk a PE boots from (X:, usually) is deliberately included: it is
// where a careless restore target ends up, and watching it fill is how you
// find that out before it stops the machine.
func volumes() []string {
	mask, _, _ := procGetLogical.Call()
	var out []string
	for i := uint(0); i < 26; i++ {
		if mask&(1<<i) == 0 {
			continue
		}
		root := string(rune('A'+i)) + ":" + string(rune(92))
		p, err := syscall.UTF16PtrFromString(root)
		if err != nil {
			continue
		}
		// 3 is DRIVE_FIXED, 6 is DRIVE_RAMDISK.
		t, _, _ := procGetDriveType.Call(uintptr(unsafe.Pointer(p)))
		if t == 3 || t == 6 {
			out = append(out, root)
		}
	}
	return out
}
