package monitor

import (
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/shirou/gopsutil/v3/disk"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/privacy"
)

const componentMonitor = "monitor"

// MountGroup represents a group of paths sharing the same mount point
type MountGroup struct {
	MountPoint string   // The actual mount point (e.g., "/")
	Device     string   // The device (e.g., "/dev/sda1")
	Fstype     string   // Filesystem type (e.g., "ext4")
	Paths      []string // All monitored paths on this mount
}

// getMountInfoFromPartitions returns mount point, device, and fstype for a path
// Uses the provided partitions list to avoid repeated syscalls
func getMountInfoFromPartitions(path string, partitions []disk.PartitionStat) (mountPoint, device, fstype string, err error) {
	// Resolve symlinks first (critical for correct mount detection)
	resolvedPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		// If symlink resolution fails, try with original path
		if _, statErr := os.Stat(path); statErr != nil {
			return "", "", "", errors.New(err).Component(componentMonitor).Category(errors.CategorySystem).Context("operation", "resolve_mount_path").Build()
		}
		resolvedPath = path
	}

	var bestMatch disk.PartitionStat
	var bestLen int

	for _, p := range partitions {
		mp := p.Mountpoint
		if strings.HasPrefix(resolvedPath, mp) {
			if resolvedPath == mp || len(mp) == 1 || strings.HasPrefix(resolvedPath, mp+"/") {
				if len(mp) > bestLen {
					bestMatch = p
					bestLen = len(mp)
				}
			}
		}
	}

	if bestLen == 0 {
		// Collect available mountpoints for diagnostic context (anonymized for privacy)
		mountpoints := make([]string, 0, len(partitions))
		for _, p := range partitions {
			mountpoints = append(mountpoints, privacy.AnonymizeStacktracePath(p.Mountpoint))
		}
		return "", "", "", errors.Newf("no mount point found for path: %s", privacy.AnonymizeStacktracePath(path)).
			Component(componentMonitor).
			Category(errors.CategorySystem).
			Context("operation", "resolve_mount_path").
			Context("partition_count", len(partitions)).
			Context("available_mountpoints", strings.Join(mountpoints, ", ")).
			Build()
	}

	return bestMatch.Mountpoint, bestMatch.Device, bestMatch.Fstype, nil
}

// groupPathsByMountPoint groups paths by their underlying mount point
// Calls disk.Partitions() once and reuses for all path resolutions
func groupPathsByMountPoint(paths []string) ([]MountGroup, error) {
	// Get partitions once for all paths (performance optimization)
	partitions, err := disk.Partitions(false)
	if err != nil {
		return nil, errors.New(err).Component(componentMonitor).Category(errors.CategorySystem).Context("operation", "get_partitions").Build()
	}

	return groupPathsWithPartitions(paths, partitions), nil
}

// groupPathsWithPartitions groups paths using provided partition list
// This validates path existence and resolves symlinks
func groupPathsWithPartitions(paths []string, partitions []disk.PartitionStat) []MountGroup {
	groups := make(map[string]*MountGroup)

	for _, path := range paths {
		mountPoint, device, fstype, err := getMountInfoFromPartitions(path, partitions)
		if err != nil {
			// Mount point detection failed — common in containers with overlay filesystems.
			// If the path is accessible, use it directly as its own mount group so that
			// disk.Usage() can still report usage stats.
			if _, statErr := os.Stat(path); statErr == nil {
				GetLogger().Debug("Mount point detection failed, monitoring path directly",
					logger.String("path", path),
					logger.Error(err),
				)
				mountPoint = path
				device = ""
				fstype = ""
			} else {
				GetLogger().Debug("Skipping inaccessible path for mount grouping",
					logger.String("path", path),
					logger.Error(err),
					logger.String("stat_error", statErr.Error()),
				)
				continue
			}
		}

		if group, exists := groups[mountPoint]; exists {
			group.Paths = append(group.Paths, path)
		} else {
			groups[mountPoint] = &MountGroup{
				MountPoint: mountPoint,
				Device:     device,
				Fstype:     fstype,
				Paths:      []string{path},
			}
		}
	}

	return sortMountGroups(groups)
}

// sortMountGroups converts a map of groups to a sorted slice
func sortMountGroups(groups map[string]*MountGroup) []MountGroup {
	result := make([]MountGroup, 0, len(groups))
	for _, group := range groups {
		// Sort paths within group for consistent output
		slices.Sort(group.Paths)
		result = append(result, *group)
	}

	// Sort groups by mount point
	sort.Slice(result, func(i, j int) bool {
		return result[i].MountPoint < result[j].MountPoint
	})

	return result
}
