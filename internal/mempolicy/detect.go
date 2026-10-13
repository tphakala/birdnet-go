package mempolicy

import (
	"io/fs"
	"math"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/shirou/gopsutil/v3/mem"

	"github.com/tphakala/birdnet-go/internal/errors"
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

	// cgroupV1UseHierarchyFile says whether a v1 memory cgroup's limit also
	// covers its descendants.
	cgroupV1UseHierarchyFile = "memory.use_hierarchy"
	// Values of memory.use_hierarchy: "1" means the cgroup's limit covers its
	// descendants, "0" means it covers only its own tasks.
	cgroupV1HierarchyOn  = "1"
	cgroupV1HierarchyOff = "0"

	procSelfCgroup = "proc/self/cgroup"
)

// cgroupV1UnlimitedThreshold guards against the cgroup v1 "unlimited" sentinel,
// which is a near-MaxInt64 value (commonly 0x7FFFFFFFFFFFF000). Any value at or
// above this is treated as no limit.
const cgroupV1UnlimitedThreshold int64 = 1 << 62

// DetectTotalMemory returns the effective memory ceiling for this process:
// the smaller of host RAM and any cgroup memory limit. The cgroup check matters
// inside containers (e.g. Docker --memory=512m), where host RAM reporting would
// otherwise mask the real limit.
//
// exact is true when every cgroup file consulted was readable and valid; bytes
// is then the effective ceiling, or 0 if neither host RAM nor a cap is known.
// When exact is false a cgroup limit exists or may exist but could not be read,
// and bytes is only an upper bound: host RAM clamped by any cgroup level that
// could be read.
func DetectTotalMemory() (bytes int64, exact bool) {
	return DetectTotalMemoryAt("/")
}

// DetectTotalMemoryAt is DetectTotalMemory with the filesystem root
// parameterized so a caller can probe a fixture tree instead of the live
// system. Host RAM still comes from the running kernel; only the cgroup limit
// is read from under root. The bytes and exact results have the same meaning as
// for DetectTotalMemory.
func DetectTotalMemoryAt(root string) (bytes int64, exact bool) {
	limit, exact := detectCgroupLimit(root)
	return effectiveTotal(hostTotalMemory(), limit), exact
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
// v2 (memory.max) and falling back to v1 (memory.limit_in_bytes). The limit is 0
// when no limit was found (root is parameterized for testability).
//
// exact is false when a cgroup file exists but could not be read or parsed, or
// when /proc/self/cgroup could not be read. The limit is then the tightest
// readable upper bound, or 0 when nothing readable capped the process.
//
// It resolves the process's own cgroup subtree from /proc/self/cgroup and reads
// the limit there as well as at the cgroup mount root. The subtree path
// matters when the container does not have a private cgroup namespace (e.g.
// Docker --cgroupns=host, or cgroup v1), where the mount root holds the host's
// unlimited value and the real per-container cap lives in a subtree. When
// /proc/self/cgroup is unreadable the mount root proves nothing, so the result
// is inexact even if no cgroup file was found.
func detectCgroupLimit(root string) (limit int64, exact bool) {
	v2Sub, v1Sub, subExact := cgroupSubPaths(root)
	if limit, found, v2Exact := cgroupV2Limit(root, v2Sub); found {
		return limit, v2Exact && subExact
	}
	limit, v1Exact := cgroupV1Limit(root, v1Sub)
	return limit, v1Exact && subExact
}

// readCgroupFile reads a cgroup control file. present is false when the file
// does not exist: ENOENT, or ENOTDIR when a path component is a regular file
// (a controller that is not mounted looks like either). Any other error means
// the file is there but unusable (EACCES, EIO, EISDIR) and is returned with
// present true, so the caller can tell "no limit" from "limit unknown".
func readCgroupFile(p string) (data string, present bool, err error) {
	b, err := os.ReadFile(p)
	switch {
	case err == nil:
		return string(b), true, nil
	case errors.Is(err, fs.ErrNotExist), errors.Is(err, syscall.ENOTDIR):
		return "", false, nil
	default:
		return "", true, err
	}
}

// cgroupV2Limit returns the effective cgroup v2 memory limit by taking the
// minimum memory.max from the process's cgroup up through its ancestors to the
// unified mount root: a cgroup's effective limit is bounded by every ancestor, so
// a "max" leaf can still be capped by a parent (e.g. a Kubernetes pod cgroup).
// found is true when any memory.max file exists (v2 is the active hierarchy); a
// returned limit of 0 then means no readable level set a limit. exact is false
// when any level was unreadable or held unparseable content; the walk continues
// past such levels so the limit stays the tightest readable upper bound.
func cgroupV2Limit(root, sub string) (limit int64, found, exact bool) {
	// The ancestor walk terminates on sub == "/", so a relative path would never
	// reach it: path.Dir("docker/abc") descends to "." and then stays there,
	// spinning on a ReadFile forever. The kernel always writes an absolute path
	// on the "0::" line, but the root is caller-supplied, so anything that is
	// not absolute is normalised to the mount root rather than trusted.
	if sub == "" || !strings.HasPrefix(sub, "/") {
		sub = "/"
	}
	exact = true
	for {
		data, present, err := readCgroupFile(filepath.Join(root, cgroupV2Base, sub, cgroupV2File))
		if present {
			found = true
			// "max" means unlimited at this level; a number caps the subtree.
			v, ok := parseCgroupV2Max(data)
			switch {
			case err != nil || !ok:
				exact = false
			case v > 0 && (limit == 0 || v < limit):
				limit = v
			}
		}
		if sub == "/" {
			break
		}
		sub = path.Dir(sub) // cgroup paths are always slash-separated
	}
	return limit, found, exact
}

// cgroupV1Limit returns the effective cgroup v1 memory limit by walking the
// process's memory controller cgroup up through its ancestors to the controller
// mount root and taking the smallest positive limit, as cgroupV2Limit does. An
// ancestor above the deepest present level caps the process only when its
// memory.use_hierarchy is "1" or absent; "0" means its limit does not cover
// descendants. The limit is 0 when none is found (also the path for non-v1
// systems, where the files are absent). exact is false when a limit file was
// unreadable or held unparseable content, or when an ancestor's use_hierarchy
// could not be read, in which case that ancestor's limit is left out so the
// result stays an upper bound.
func cgroupV1Limit(root, sub string) (limit int64, exact bool) {
	// Same normalisation as cgroupV2Limit: the walk ends on "/", so a relative
	// path would never reach it.
	if sub == "" || !strings.HasPrefix(sub, "/") {
		sub = "/"
	}
	exact = true
	seen := false // a limit file was present at a deeper level
	for {
		data, present, err := readCgroupFile(filepath.Join(root, cgroupV1MemBase, sub, cgroupV1File))
		if present {
			applies, hierExact := true, true
			if seen {
				applies, hierExact = cgroupV1Hierarchical(root, sub)
			}
			seen = true
			v, ok := parseCgroupV1Limit(data)
			switch {
			case !hierExact:
				exact = false
			case err != nil || !ok:
				exact = false
			case applies && v > 0 && (limit == 0 || v < limit):
				limit = v
			}
		}
		if sub == "/" {
			break
		}
		sub = path.Dir(sub) // cgroup paths are always slash-separated
	}
	return limit, exact
}

// cgroupV1Hierarchical reports whether a v1 memory cgroup's limit covers its
// descendants, from its memory.use_hierarchy file: absent or "1" means it does,
// "0" means it does not. exact is false when the file exists but cannot be
// read or holds anything else; applies is then false too, so the caller leaves
// that limit out and keeps an upper bound.
func cgroupV1Hierarchical(root, sub string) (applies, exact bool) {
	data, present, err := readCgroupFile(filepath.Join(root, cgroupV1MemBase, sub, cgroupV1UseHierarchyFile))
	switch {
	case !present:
		return true, true
	case err != nil:
		return false, false
	}
	switch strings.TrimSpace(data) {
	case cgroupV1HierarchyOn:
		return true, true
	case cgroupV1HierarchyOff:
		return false, true
	default:
		return false, false
	}
}

// cgroupSubPaths parses /proc/self/cgroup for the process's cgroup path under the
// v2 unified hierarchy and the v1 memory controller. Either is "" when absent (a
// namespaced container reports "/", which resolves back to the mount root).
// exact is false when the file exists but could not be read.
func cgroupSubPaths(root string) (v2Sub, v1Sub string, exact bool) {
	data, present, err := readCgroupFile(filepath.Join(root, procSelfCgroup))
	if !present {
		return "", "", true
	}
	if err != nil {
		return "", "", false
	}
	for line := range strings.SplitSeq(strings.TrimSpace(data), "\n") {
		// Format: hierarchy-ID:controller-list:cgroup-path
		parts := strings.SplitN(line, ":", 3)
		if len(parts) != 3 {
			continue
		}
		controllers, cgPath := parts[1], parts[2]
		if parts[0] == "0" && controllers == "" {
			v2Sub = cgPath // unified v2 line: "0::<path>"
			continue
		}
		for c := range strings.SplitSeq(controllers, ",") {
			if c == "memory" {
				v1Sub = cgPath
			}
		}
	}
	return v2Sub, v1Sub, true
}

// parseCgroupV2Max parses memory.max: a byte count, or "max" for unlimited. ok
// is true for a valid value: "max" gives (0, true) and a positive integer gives
// (v, true). Empty, non-numeric, zero and negative content is invalid and gives
// (0, false), so garbage is not mistaken for "unlimited".
func parseCgroupV2Max(s string) (limit int64, ok bool) {
	s = strings.TrimSpace(s)
	if s == "max" {
		return 0, true
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil || v <= 0 {
		return 0, false
	}
	return v, true
}

// parseCgroupV1Limit parses memory.limit_in_bytes. ok is true for a valid
// value: the near-MaxInt64 "no limit" sentinel gives (0, true) and a positive
// integer below it gives (v, true). Empty, non-numeric, zero and negative
// content is invalid and gives (0, false).
func parseCgroupV1Limit(s string) (limit int64, ok bool) {
	s = strings.TrimSpace(s)
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil || v <= 0 {
		return 0, false
	}
	if v >= cgroupV1UnlimitedThreshold {
		return 0, true
	}
	return v, true
}
