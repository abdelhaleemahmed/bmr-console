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

// spaceOf returns free and total bytes. Total is what lets the caller drop a
// filesystem that has no size at all -- a kernel bookkeeping mount rather than
// somewhere a restore could land.
func spaceOf(path string) (free, total uint64, err error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, 0, err
	}
	return uint64(st.Bavail) * uint64(st.Bsize), uint64(st.Blocks) * uint64(st.Bsize), nil
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
		"selinuxfs": true, "ramfs": true, "nsfs": true, "rpc_pipefs": true,
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
		// systemd gives each service its own credential mount under
		// /run/credentials. There can be dozens, they are all zero bytes, and
		// on the one screen somebody reads during a restore they would bury
		// the mount point that matters.
		if strings.HasPrefix(mp, "/run/credentials/") || strings.HasPrefix(mp, "/sys/") {
			continue
		}
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
