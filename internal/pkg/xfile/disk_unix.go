//go:build linux || darwin || freebsd

package xfile

import "golang.org/x/sys/unix"

func DiskUsage(path string) (float64, float64, error) {
	var stat unix.Statfs_t
	err := unix.Statfs(path, &stat)
	return float64(stat.Blocks) * float64(stat.Bsize), float64(stat.Bavail) * float64(stat.Bsize), err
}
