package mempolicy

import (
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/shirou/gopsutil/v3/mem"
)

// cgroup memory-limit file locations, relative to the filesystem root.
const (
	cgroupV2MaxPath = "sys/fs/cgroup/memory.max"
	cgroupV1MaxPath = "sys/fs/cgroup/memory/memory.limit_in_bytes"
)

// cgroupV1UnlimitedThreshold guards against the cgroup v1 "unlimited" sentinel,
// which is a near-MaxInt64 value (commonly 0x7FFFFFFFFFFFF000). Any value at or
// above this is treated as no limit.
const cgroupV1UnlimitedThreshold int64 = 1 << 62

// DetectTotalMemory returns the effective memory ceiling for this process:
// the smaller of host RAM and any cgroup memory limit. Returns 0 if unknown.
// The cgroup check matters inside containers (e.g. Docker --memory=512m), where
// host RAM reporting would otherwise mask the real limit.
func DetectTotalMemory() int64 {
	return effectiveTotal(hostTotalMemory(), detectCgroupLimit("/"))
}

// hostTotalMemory returns total physical RAM in bytes via gopsutil, or 0 on error.
func hostTotalMemory() int64 {
	vm, err := mem.VirtualMemory()
	if err != nil || vm == nil {
		return 0
	}
	if vm.Total > uint64(math.MaxInt64) {
		return math.MaxInt64
	}
	return int64(vm.Total)
}

// effectiveTotal picks the binding limit between host RAM and a cgroup cap.
// A non-positive value means "unknown/unlimited" for that source.
func effectiveTotal(host, cgroup int64) int64 {
	switch {
	case cgroup <= 0:
		return host
	case host <= 0:
		return cgroup
	case cgroup < host:
		return cgroup
	default:
		return host
	}
}

// detectCgroupLimit reads the cgroup memory limit under root, preferring cgroup
// v2 (memory.max) and falling back to v1 (memory.limit_in_bytes). Returns 0 when
// there is no limit or no cgroup files (root is parameterized for testability).
func detectCgroupLimit(root string) int64 {
	if data, err := os.ReadFile(filepath.Join(root, cgroupV2MaxPath)); err == nil {
		// v2 present: "max" means unlimited, a number is the cap.
		if v, ok := parseCgroupV2Max(string(data)); ok {
			return v
		}
		return 0
	}
	if data, err := os.ReadFile(filepath.Join(root, cgroupV1MaxPath)); err == nil {
		if v, ok := parseCgroupV1Limit(string(data)); ok {
			return v
		}
	}
	return 0
}

// parseCgroupV2Max parses memory.max: a byte count, or "max" for unlimited.
func parseCgroupV2Max(s string) (int64, bool) {
	s = strings.TrimSpace(s)
	if s == "" || s == "max" {
		return 0, false
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil || v <= 0 {
		return 0, false
	}
	return v, true
}

// parseCgroupV1Limit parses memory.limit_in_bytes, treating the near-MaxInt64
// sentinel as "no limit".
func parseCgroupV1Limit(s string) (int64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil || v <= 0 || v >= cgroupV1UnlimitedThreshold {
		return 0, false
	}
	return v, true
}
