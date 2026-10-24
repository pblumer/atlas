//go:build windows

package diskspace

import (
	"fmt"
	"strings"

	"golang.org/x/sys/windows"
)

// Of reads the volume holding dir. GetDiskFreeSpaceExW answers for the calling
// account: with disk quotas on, Total and Available are what the quota allows, so
// they are the numbers that run out for this server.
func Of(dir string) (Usage, error) {
	p, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return Usage{}, fmt.Errorf("diskspace: %s: %w", dir, err)
	}
	var available, total, free uint64
	if err := windows.GetDiskFreeSpaceEx(p, &available, &total, &free); err != nil {
		return Usage{}, fmt.Errorf("diskspace: GetDiskFreeSpaceEx %s: %w", dir, err)
	}
	volume, err := volumeOf(p)
	if err != nil {
		return Usage{}, fmt.Errorf("diskspace: GetVolumePathName %s: %w", dir, err)
	}
	return clamp(Usage{Total: total, Free: free, Available: available, Volume: volume}), nil
}

// volumeOf names the volume a path lives on by its mount point — "C:\" for a drive, a
// folder for a volume mounted into one — which is what tells two directories on one
// disk from two on different ones.
func volumeOf(p *uint16) (string, error) {
	buf := make([]uint16, windows.MAX_LONG_PATH)
	if err := windows.GetVolumePathName(p, &buf[0], uint32(len(buf))); err != nil {
		return "", err
	}
	return strings.ToLower(windows.UTF16ToString(buf)), nil
}
