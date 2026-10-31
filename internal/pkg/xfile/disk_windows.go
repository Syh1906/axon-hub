package xfile

import "golang.org/x/sys/windows"

// DiskUsage reports filesystem capacity and bytes available to this process.
func DiskUsage(path string) (float64, float64, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, 0, err
	}
	var available, total, free uint64
	err = windows.GetDiskFreeSpaceEx(p, &available, &total, &free)
	return float64(total), float64(available), err
}
