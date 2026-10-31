//go:build !windows && !linux && !darwin && !freebsd

package xfile

import "fmt"

func DiskUsage(string) (float64, float64, error) {
	return 0, 0, fmt.Errorf("disk capacity unavailable on this platform")
}
