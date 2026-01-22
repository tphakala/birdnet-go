package monitor

import (
	"fmt"
	"strings"
	"testing"

	"github.com/shirou/gopsutil/v3/disk"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockPartitions returns a mock partition list for deterministic unit tests
func mockPartitions() []disk.PartitionStat {
	return []disk.PartitionStat{
		{Device: "/dev/sda1", Mountpoint: "/", Fstype: "ext4"},
		{Device: "/dev/sda2", Mountpoint: "/home", Fstype: "ext4"},
		{Device: "/dev/sdb1", Mountpoint: "/mnt/data", Fstype: "ext4"},
	}
}

// resolveMountPointFromPartitions is a test helper that skips path existence check
// Used for deterministic unit tests with mock partitions
func resolveMountPointFromPartitions(path string, partitions []disk.PartitionStat) (string, error) {
	var bestMatch string
	for _, p := range partitions {
		mountpoint := p.Mountpoint
		if strings.HasPrefix(path, mountpoint) {
			if path == mountpoint || len(mountpoint) == 1 || strings.HasPrefix(path, mountpoint+"/") {
				if len(mountpoint) > len(bestMatch) {
					bestMatch = mountpoint
				}
			}
		}
	}

	if bestMatch == "" {
		return "", fmt.Errorf("no mount point found for path: %s", path)
	}

	return bestMatch, nil
}

// groupPathsWithPartitionsMock is a test helper that skips path existence checks
// Used for deterministic unit tests with mock partitions
func groupPathsWithPartitionsMock(paths []string, partitions []disk.PartitionStat) []MountGroup {
	groups := make(map[string]*MountGroup)

	for _, path := range paths {
		mount, err := resolveMountPointFromPartitions(path, partitions)
		if err != nil {
			continue
		}

		// Find device and fstype from partitions
		var device, fstype string
		for _, p := range partitions {
			if p.Mountpoint == mount {
				device = p.Device
				fstype = p.Fstype
				break
			}
		}

		if group, exists := groups[mount]; exists {
			group.Paths = append(group.Paths, path)
		} else {
			groups[mount] = &MountGroup{
				MountPoint: mount,
				Device:     device,
				Fstype:     fstype,
				Paths:      []string{path},
			}
		}
	}

	return sortMountGroups(groups)
}

func TestResolveMountPointFromPartitions(t *testing.T) {
	t.Parallel()

	partitions := mockPartitions()

	tests := []struct {
		name     string
		path     string
		expected string
	}{
		{"root resolves to root", "/", "/"},
		{"home resolves to home", "/home", "/home"},
		{"home subdir resolves to home", "/home/user", "/home"},
		{"var resolves to root", "/var", "/"},
		{"etc resolves to root", "/etc", "/"},
		{"mnt data resolves to mnt data", "/mnt/data", "/mnt/data"},
		{"mnt data subdir resolves to mnt data", "/mnt/data/files", "/mnt/data"},
		{"mnt without data resolves to root", "/mnt", "/"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			mount, err := resolveMountPointFromPartitions(tt.path, partitions)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, mount)
		})
	}
}

func TestResolveMountPointFromPartitionsNoMatch(t *testing.T) {
	t.Parallel()

	// Empty partitions list
	partitions := []disk.PartitionStat{}
	_, err := resolveMountPointFromPartitions("/some/path", partitions)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no mount point found")
}

func TestGroupPathsByMountPoint(t *testing.T) {
	t.Parallel()

	// Test with real filesystem - paths that exist
	paths := []string{"/", "/tmp"}
	groups, err := groupPathsByMountPoint(paths)
	require.NoError(t, err)

	// Should have at least one group
	assert.NotEmpty(t, groups)

	// Total paths in all groups should equal input paths
	totalPaths := 0
	for _, g := range groups {
		totalPaths += len(g.Paths)
	}
	assert.Equal(t, 2, totalPaths)
}

func TestGroupPathsByMountPointWithInvalidPath(t *testing.T) {
	t.Parallel()

	// Invalid paths should be skipped, not cause error
	paths := []string{"/", "/nonexistent/path/xyz/abc/123"}
	groups, err := groupPathsByMountPoint(paths)
	require.NoError(t, err)

	// Should have group for valid path
	assert.NotEmpty(t, groups)

	// Only the valid path should be in groups
	totalPaths := 0
	for _, g := range groups {
		totalPaths += len(g.Paths)
	}
	assert.Equal(t, 1, totalPaths)
}

func TestGroupPathsWithPartitionsAggregation(t *testing.T) {
	t.Parallel()

	partitions := mockPartitions()

	// All these paths should be on root filesystem
	paths := []string{"/var", "/etc", "/usr"}
	groups := groupPathsWithPartitionsMock(paths, partitions)

	// All three paths should be in one group (root)
	require.Len(t, groups, 1)
	assert.Equal(t, "/", groups[0].MountPoint)
	assert.Equal(t, "/dev/sda1", groups[0].Device)
	assert.Equal(t, "ext4", groups[0].Fstype)
	assert.Len(t, groups[0].Paths, 3)
	// Paths should be sorted
	assert.Equal(t, []string{"/etc", "/usr", "/var"}, groups[0].Paths)
}

func TestGroupPathsWithPartitionsMultipleMounts(t *testing.T) {
	t.Parallel()

	partitions := mockPartitions()

	// Paths on different mounts
	paths := []string{"/var", "/home/user", "/mnt/data/files"}
	groups := groupPathsWithPartitionsMock(paths, partitions)

	// Should have 3 groups: /, /home, /mnt/data
	require.Len(t, groups, 3)

	// Verify groups are sorted by mount point
	assert.Equal(t, "/", groups[0].MountPoint)
	assert.Equal(t, "/home", groups[1].MountPoint)
	assert.Equal(t, "/mnt/data", groups[2].MountPoint)

	// Verify paths in each group
	assert.Equal(t, []string{"/var"}, groups[0].Paths)
	assert.Equal(t, []string{"/home/user"}, groups[1].Paths)
	assert.Equal(t, []string{"/mnt/data/files"}, groups[2].Paths)
}

func TestGroupPathsWithPartitionsEmptyInput(t *testing.T) {
	t.Parallel()

	partitions := mockPartitions()
	groups := groupPathsWithPartitionsMock([]string{}, partitions)

	assert.Empty(t, groups)
}

func TestMountGroupFields(t *testing.T) {
	t.Parallel()

	partitions := mockPartitions()
	paths := []string{"/home/user1", "/home/user2"}
	groups := groupPathsWithPartitionsMock(paths, partitions)

	require.Len(t, groups, 1)
	group := groups[0]

	assert.Equal(t, "/home", group.MountPoint)
	assert.Equal(t, "/dev/sda2", group.Device)
	assert.Equal(t, "ext4", group.Fstype)
	assert.Equal(t, []string{"/home/user1", "/home/user2"}, group.Paths)
}
