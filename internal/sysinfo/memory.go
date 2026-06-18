// Package sysinfo: process memory helpers.
package sysinfo

import (
	"os"

	"github.com/shirou/gopsutil/v3/process"
)

// CurrentProcessRSS returns the resident set size (host RAM) of the current
// process in bytes. It is cross-platform via gopsutil. Callers treat any error
// as "RSS unavailable on this platform" and degrade gracefully rather than
// reporting a misleading zero.
func CurrentProcessRSS() (uint64, error) {
	p, err := process.NewProcess(int32(os.Getpid()))
	if err != nil {
		return 0, err
	}
	mi, err := p.MemoryInfo()
	if err != nil {
		return 0, err
	}
	return mi.RSS, nil
}
