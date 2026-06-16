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

	// Bases for resolving the process's own cgroup subtree (see detectCgroupLimit).
	cgroupV2Base    = "sys/fs/cgroup"        // unified (v2) mount
	cgroupV1MemBase = "sys/fs/cgroup/memory" // v1 memory controller mount
	cgroupV2File    = "memory.max"
	cgroupV1File    = "memory.limit_in_bytes"
	procSelfCgroup  = "proc/self/cgroup"
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
//
// It resolves the process's own cgroup subtree from /proc/self/cgroup and reads
// the limit there before falling back to the cgroup mount root. The subtree path
// matters when the container does not have a private cgroup namespace (e.g.
// Docker --cgroupns=host, or cgroup v1), where the mount root holds the host's
// unlimited value and the real per-container cap lives in a subtree.
func detectCgroupLimit(root string) int64 {
	v2Sub, v1Sub := cgroupSubPaths(root)

	// cgroup v2 (unified): process subtree first, then the cgroup mount root.
	for _, rel := range distinctPaths(
		filepath.Join(cgroupV2Base, v2Sub, cgroupV2File),
		cgroupV2MaxPath,
	) {
		if data, err := os.ReadFile(filepath.Join(root, rel)); err == nil {
			// v2 present: "max" means unlimited, a number is the cap.
			if v, ok := parseCgroupV2Max(string(data)); ok {
				return v
			}
			return 0
		}
	}

	// cgroup v1: the memory controller subtree first, then the mount root.
	for _, rel := range distinctPaths(
		filepath.Join(cgroupV1MemBase, v1Sub, cgroupV1File),
		cgroupV1MaxPath,
	) {
		if data, err := os.ReadFile(filepath.Join(root, rel)); err == nil {
			if v, ok := parseCgroupV1Limit(string(data)); ok {
				return v
			}
		}
	}
	return 0
}

// cgroupSubPaths parses /proc/self/cgroup for the process's cgroup path under the
// v2 unified hierarchy and the v1 memory controller. Either is "" when absent (a
// namespaced container reports "/", which resolves back to the mount root).
func cgroupSubPaths(root string) (v2Sub, v1Sub string) {
	data, err := os.ReadFile(filepath.Join(root, procSelfCgroup))
	if err != nil {
		return "", ""
	}
	for line := range strings.SplitSeq(strings.TrimSpace(string(data)), "\n") {
		// Format: hierarchy-ID:controller-list:cgroup-path
		parts := strings.SplitN(line, ":", 3)
		if len(parts) != 3 {
			continue
		}
		controllers, path := parts[1], parts[2]
		if parts[0] == "0" && controllers == "" {
			v2Sub = path // unified v2 line: "0::<path>"
			continue
		}
		for c := range strings.SplitSeq(controllers, ",") {
			if c == "memory" {
				v1Sub = path
			}
		}
	}
	return v2Sub, v1Sub
}

// distinctPaths returns the inputs with duplicates removed, preserving order. A
// subtree path of "/" or "" cleans to the same path as the mount root, so this
// avoids reading the same file twice.
func distinctPaths(paths ...string) []string {
	out := make([]string, 0, len(paths))
	seen := make(map[string]bool, len(paths))
	for _, p := range paths {
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	return out
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
