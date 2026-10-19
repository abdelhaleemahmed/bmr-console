//go:build !windows

// disk_unix.go -- free space, and which mount points to ask about.
//
// The Linux half of a recovery is a ReaR rescue system, where the machine
// being rebuilt is mounted under /mnt/local. That is where the bytes land, so
// it is listed first when it exists.
package main

import (
	"bufio"
	"os"
	"strings"
	"syscall"
)

func freeSpace(path string) (uint64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, err
	}
	return uint64(st.Bavail) * uint64(st.Bsize), nil
}

// volumes reads the mount table rather than guessing. Pseudo-filesystems are
// skipped: a restore does not land on proc, and a row of them would bury the
// one mount point that matters.
func volumes() []string {
	skip := map[string]bool{
		"proc": true, "sysfs": true, "devtmpfs": true, "devpts": true,
		"cgroup": true, "cgroup2": true, "securityfs": true, "pstore": true,
		"debugfs": true, "tracefs": true, "configfs": true, "fusectl": true,
		"bpf": true, "mqueue": true, "hugetlbfs": true, "autofs": true,
		"binfmt_misc": true, "efivarfs": true, "squashfs": true, "overlay": true,
	}
	f, err := os.Open("/proc/mounts")
	if err != nil {
		return []string{"/"}
	}
	defer f.Close()

	var local, rest []string
	seen := map[string]bool{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		parts := strings.Fields(sc.Text())
		if len(parts) < 3 || skip[parts[2]] {
			continue
		}
		mp := parts[1]
		if seen[mp] {
			continue
		}
		seen[mp] = true
		// /mnt/local is where ReaR mounts the system being restored.
		if mp == "/mnt/local" || strings.HasPrefix(mp, "/mnt/local/") {
			local = append(local, mp)
		} else {
			rest = append(rest, mp)
		}
	}
	return append(local, rest...)
}
