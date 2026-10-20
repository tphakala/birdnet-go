package mempolicy

import (
	"path/filepath"
	"strconv"
	"strings"
)

// Cgroup hierarchy versions reported in CgroupMemory.Version.
const (
	// CgroupVersionV2 is the unified cgroup hierarchy.
	CgroupVersionV2 = "v2"
	// CgroupVersionV1 is the legacy per-controller hierarchy.
	CgroupVersionV1 = "v1"
)

// CgroupMemory describes the memory cgroup of the current process.
type CgroupMemory struct {
	// Version is CgroupVersionV2, CgroupVersionV1, or "" when no memory
	// controller was found.
	Version string
	// LimitBytes is the effective cgroup memory limit; 0 means no limit.
	LimitBytes int64
	// LimitExact is false when a limit file existed but could not be read, in
	// which case LimitBytes is only an upper bound (see DetectTotalMemory).
	LimitExact bool
	// UsageBytes is the current cgroup memory usage; 0 means it could not be read.
	UsageBytes int64
}

// DetectCgroupMemory reports the cgroup memory limit and current usage of this
// process. See DetectCgroupMemoryAt for how the files are located.
func DetectCgroupMemory() CgroupMemory {
	return DetectCgroupMemoryAt("/")
}

// DetectCgroupMemoryAt is DetectCgroupMemory with the filesystem root
// parameterized so a caller can probe a fixture tree. Usage is read from the
// process's own cgroup (memory.current on v2, memory.usage_in_bytes on v1). When
// that file is absent it falls back to the file at the controller mount root,
// which is the right one in a container with a private cgroup namespace, where
// the process's sub path is "/". Content that does not parse gives 0.
func DetectCgroupMemoryAt(root string) CgroupMemory {
	v2Sub, v1Sub, _ := cgroupSubPaths(root)
	limit, exact := detectCgroupLimit(root)
	out := CgroupMemory{LimitBytes: limit, LimitExact: exact}

	if usage, present := readCgroupUsage(root, cgroupV2Base, v2Sub, cgroupV2UsageFile); present {
		out.Version = CgroupVersionV2
		out.UsageBytes = usage
		return out
	}
	if _, found, _ := cgroupV2Limit(root, v2Sub); found {
		out.Version = CgroupVersionV2
		return out
	}
	if usage, present := readCgroupUsage(root, cgroupV1MemBase, v1Sub, cgroupV1UsageFile); present {
		out.Version = CgroupVersionV1
		out.UsageBytes = usage
		return out
	}
	if _, present, _ := readCgroupFile(filepath.Join(root, cgroupV1MemBase, v1Sub, cgroupV1File)); present {
		out.Version = CgroupVersionV1
	}
	return out
}

// readCgroupUsage reads a usage file from the process's own cgroup under base,
// falling back to the file at base itself. present is true when either file
// exists; the value is 0 when it is unreadable or not a non-negative integer.
func readCgroupUsage(root, base, sub, name string) (usage int64, present bool) {
	for _, p := range []string{
		filepath.Join(root, base, sub, name),
		filepath.Join(root, base, name),
	} {
		data, ok, err := readCgroupFile(p)
		if !ok {
			continue
		}
		if err != nil {
			return 0, true
		}
		v, perr := strconv.ParseInt(strings.TrimSpace(data), 10, 64)
		if perr != nil || v < 0 {
			return 0, true
		}
		return v, true
	}
	return 0, false
}
