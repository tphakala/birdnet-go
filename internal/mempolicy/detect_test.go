package mempolicy

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseCgroupV2Max(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in     string
		want   int64
		wantOK bool
	}{
		{"536870912\n", 536870912, true},
		{"max\n", 0, true},
		{"max", 0, true},
		{"", 0, false},
		{"  268435456 ", 268435456, true},
		{"garbage", 0, false},
		{"0", 0, false},
		{"-5", 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			t.Parallel()
			got, ok := parseCgroupV2Max(tt.in)
			assert.Equal(t, tt.wantOK, ok)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestParseCgroupV1Limit(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		in     string
		want   int64
		wantOK bool
	}{
		{"real limit", "536870912\n", 536870912, true},
		{"v1 unlimited sentinel", "9223372036854771712\n", 0, true},
		{"max int64 sentinel", "9223372036854775807\n", 0, true},
		{"empty", "", 0, false},
		{"garbage", "nope", 0, false},
		{"zero", "0", 0, false},
		{"negative", "-1", 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, ok := parseCgroupV1Limit(tt.in)
			assert.Equal(t, tt.wantOK, ok)
			assert.Equal(t, tt.want, got)
		})
	}
}

// detectCgroupLimit reads cgroup files under a fake root, so we can exercise
// the v2-first, v1-fallback logic with t.TempDir(). An unreadable file is
// modelled as a directory at the file path (EISDIR on read, not "not exist").
func TestDetectCgroupLimit(t *testing.T) {
	t.Parallel()

	const (
		limit512 = int64(536870912)
		limit256 = int64(268435456)
		limit128 = int64(134217728)
		v1Sent   = "9223372036854771712\n"
	)
	v2Docker := filepath.Join(cgroupV2Base, "docker", "abc123", cgroupV2File)
	v2DockerParent := filepath.Join(cgroupV2Base, "docker", cgroupV2File)
	v1Docker := filepath.Join(cgroupV1MemBase, "docker", "abc123", cgroupV1File)
	v1PodUseHierarchy := filepath.Join(cgroupV1MemBase, "pod", cgroupV1UseHierarchyFile)

	tests := []struct {
		name      string
		files     map[string]string // relative path to content
		dirs      []string          // relative paths created as directories (unreadable files)
		wantLimit int64
		wantExact bool
	}{
		{name: "cgroup v2 limit", files: map[string]string{cgroupV2MaxPath: "536870912\n"}, wantLimit: limit512, wantExact: true},
		{name: "cgroup v2 unlimited", files: map[string]string{cgroupV2MaxPath: "max\n"}, wantExact: true},
		{name: "cgroup v1 fallback", files: map[string]string{cgroupV1MaxPath: "268435456\n"}, wantLimit: limit256, wantExact: true},
		{name: "no cgroup files", wantExact: true},
		{
			name:      "cgroup v2 takes precedence over v1",
			files:     map[string]string{cgroupV2MaxPath: "536870912\n", cgroupV1MaxPath: "268435456\n"},
			wantLimit: limit512, wantExact: true,
		},
		{
			name:      "cgroup v2 max short-circuits, v1 ignored",
			files:     map[string]string{cgroupV2MaxPath: "max\n", cgroupV1MaxPath: "268435456\n"},
			wantExact: true,
		},
		{
			name:      "cgroup v2 reads process subtree (host cgroupns)",
			files:     map[string]string{procSelfCgroup: "0::/docker/abc123\n", v2Docker: "536870912\n"},
			wantLimit: limit512, wantExact: true,
		},
		{
			name:      "cgroup v2 falls back to mount root when subtree file absent",
			files:     map[string]string{procSelfCgroup: "0::/docker/abc123\n", cgroupV2MaxPath: "268435456\n"},
			wantLimit: limit256, wantExact: true,
		},
		{
			name:      "cgroup v1 reads memory controller subtree",
			files:     map[string]string{procSelfCgroup: "7:memory:/docker/abc123\n3:cpu,cpuacct:/docker/abc123\n", v1Docker: "134217728\n"},
			wantLimit: limit128, wantExact: true,
		},
		{
			name: "cgroup v2 takes ancestor limit when leaf is max",
			files: map[string]string{
				procSelfCgroup: "0::/pod/container\n",
				filepath.Join(cgroupV2Base, "pod", "container", cgroupV2File): "max\n",
				filepath.Join(cgroupV2Base, "pod", cgroupV2File):              "268435456\n",
			},
			wantLimit: limit256, wantExact: true,
		},
		{
			name: "v2 numeric leaf under a max ancestor keeps the leaf limit",
			files: map[string]string{
				procSelfCgroup: "0::/pod/container\n",
				filepath.Join(cgroupV2Base, "pod", "container", cgroupV2File): "268435456\n",
				filepath.Join(cgroupV2Base, "pod", cgroupV2File):              "max\n",
			},
			wantLimit: limit256, wantExact: true,
		},
		{
			name: "v2 smaller leaf under a larger ancestor",
			files: map[string]string{
				procSelfCgroup: "0::/pod/container\n",
				filepath.Join(cgroupV2Base, "pod", "container", cgroupV2File): "134217728\n",
				filepath.Join(cgroupV2Base, "pod", cgroupV2File):              "268435456\n",
			},
			wantLimit: limit128, wantExact: true,
		},
		{
			name: "v2 larger leaf under a smaller ancestor",
			files: map[string]string{
				procSelfCgroup: "0::/pod/container\n",
				filepath.Join(cgroupV2Base, "pod", "container", cgroupV2File): "268435456\n",
				filepath.Join(cgroupV2Base, "pod", cgroupV2File):              "134217728\n",
			},
			wantLimit: limit128, wantExact: true,
		},
		{
			name:      "v2 unreadable leaf under a numeric ancestor keeps walking",
			files:     map[string]string{procSelfCgroup: "0::/docker/abc123\n", v2DockerParent: "268435456\n"},
			dirs:      []string{v2Docker},
			wantLimit: limit256, wantExact: false,
		},
		{
			name:      "v1 numeric subtree under a sentinel root keeps the subtree limit",
			files:     map[string]string{procSelfCgroup: "7:memory:/docker/abc123\n", v1Docker: "134217728\n", cgroupV1MaxPath: v1Sent},
			wantLimit: limit128, wantExact: true,
		},
		{
			name:      "v2 unreadable leaf",
			files:     map[string]string{procSelfCgroup: "0::/docker/abc123\n"},
			dirs:      []string{v2Docker},
			wantLimit: 0, wantExact: false,
		},
		{
			name:      "v2 unreadable ancestor with numeric leaf",
			files:     map[string]string{procSelfCgroup: "0::/docker/abc123\n", v2Docker: "536870912\n"},
			dirs:      []string{v2DockerParent},
			wantLimit: limit512, wantExact: false,
		},
		{
			name:      "v2 unreadable does not fall through to v1",
			files:     map[string]string{cgroupV1MaxPath: "268435456\n"},
			dirs:      []string{cgroupV2MaxPath},
			wantLimit: 0, wantExact: false,
		},
		{name: "v2 garbage content", files: map[string]string{cgroupV2MaxPath: "garbage\n"}, wantExact: false},
		{name: "v2 empty content", files: map[string]string{cgroupV2MaxPath: ""}, wantExact: false},
		{
			name:      "v2 garbage ancestor with numeric leaf",
			files:     map[string]string{procSelfCgroup: "0::/docker/abc123\n", v2Docker: "536870912\n", v2DockerParent: "garbage"},
			wantLimit: limit512, wantExact: false,
		},
		{name: "v1 sentinel", files: map[string]string{cgroupV1MaxPath: v1Sent}, wantExact: true},
		{name: "v1 unreadable", dirs: []string{cgroupV1MaxPath}, wantExact: false},
		{
			name:      "v1 unreadable subtree, readable root limit",
			files:     map[string]string{procSelfCgroup: "7:memory:/docker/abc123\n", cgroupV1MaxPath: "268435456\n"},
			dirs:      []string{v1Docker},
			wantLimit: limit256, wantExact: false,
		},
		{
			name:      "v1 readable subtree cap, unreadable root",
			files:     map[string]string{procSelfCgroup: "7:memory:/docker/abc123\n", v1Docker: "134217728\n"},
			dirs:      []string{cgroupV1MaxPath},
			wantLimit: limit128, wantExact: false,
		},
		{
			name:      "v1 root cap below the subtree cap wins",
			files:     map[string]string{procSelfCgroup: "7:memory:/docker/abc123\n", v1Docker: "268435456\n", cgroupV1MaxPath: "134217728\n"},
			wantLimit: limit128, wantExact: true,
		},
		{
			name:      "v1 subtree cap below the root cap wins",
			files:     map[string]string{procSelfCgroup: "7:memory:/docker/abc123\n", v1Docker: "134217728\n", cgroupV1MaxPath: "268435456\n"},
			wantLimit: limit128, wantExact: true,
		},
		{
			name: "v1 intermediate ancestor limit is honoured",
			files: map[string]string{
				procSelfCgroup: "7:memory:/pod/container\n",
				filepath.Join(cgroupV1MemBase, "pod", "container", cgroupV1File): v1Sent,
				filepath.Join(cgroupV1MemBase, "pod", cgroupV1File):              "268435456\n",
				cgroupV1MaxPath: v1Sent,
			},
			wantLimit: limit256, wantExact: true,
		},
		{
			name: "v1 ancestor with use_hierarchy 0 does not cap the subtree",
			files: map[string]string{
				procSelfCgroup: "7:memory:/pod/container\n",
				filepath.Join(cgroupV1MemBase, "pod", "container", cgroupV1File): "268435456\n",
				filepath.Join(cgroupV1MemBase, "pod", cgroupV1File):              "134217728\n",
				v1PodUseHierarchy: "0\n",
			},
			wantLimit: limit256, wantExact: true,
		},
		{
			name: "v1 own cgroup with use_hierarchy 0 keeps its limit",
			files: map[string]string{
				procSelfCgroup: "7:memory:/pod/container\n",
				filepath.Join(cgroupV1MemBase, "pod", "container", cgroupV1File):             "134217728\n",
				filepath.Join(cgroupV1MemBase, "pod", "container", cgroupV1UseHierarchyFile): "0\n",
				filepath.Join(cgroupV1MemBase, "pod", cgroupV1File):                          v1Sent,
			},
			wantLimit: limit128, wantExact: true,
		},
		{
			name: "v1 mount root reached through absent levels keeps its limit under use_hierarchy 0",
			files: map[string]string{
				procSelfCgroup:  "7:memory:/docker/abc123\n",
				cgroupV1MaxPath: "134217728\n",
				filepath.Join(cgroupV1MemBase, cgroupV1UseHierarchyFile): "0\n",
			},
			wantLimit: limit128, wantExact: true,
		},
		{
			name: "v1 ancestor with use_hierarchy 1 caps the subtree",
			files: map[string]string{
				procSelfCgroup: "7:memory:/pod/container\n",
				filepath.Join(cgroupV1MemBase, "pod", "container", cgroupV1File): "268435456\n",
				filepath.Join(cgroupV1MemBase, "pod", cgroupV1File):              "134217728\n",
				v1PodUseHierarchy: "1\n",
			},
			wantLimit: limit128, wantExact: true,
		},
		{
			name: "v1 ancestor with invalid use_hierarchy is skipped and inexact",
			files: map[string]string{
				procSelfCgroup: "7:memory:/pod/container\n",
				filepath.Join(cgroupV1MemBase, "pod", "container", cgroupV1File): "268435456\n",
				filepath.Join(cgroupV1MemBase, "pod", cgroupV1File):              "134217728\n",
				v1PodUseHierarchy: "garbage\n",
			},
			wantLimit: limit256, wantExact: false,
		},
		{
			name: "v1 ancestor with unreadable use_hierarchy is skipped and inexact",
			files: map[string]string{
				procSelfCgroup: "7:memory:/pod/container\n",
				filepath.Join(cgroupV1MemBase, "pod", "container", cgroupV1File): "268435456\n",
				filepath.Join(cgroupV1MemBase, "pod", cgroupV1File):              "134217728\n",
			},
			dirs:      []string{v1PodUseHierarchy},
			wantLimit: limit256, wantExact: false,
		},
		{name: "v1 garbage", files: map[string]string{cgroupV1MaxPath: "garbage"}, wantExact: false},
		{name: "proc self cgroup unreadable", dirs: []string{procSelfCgroup}, wantExact: false},
		{
			name:      "proc self cgroup unreadable with readable root limit",
			files:     map[string]string{cgroupV2MaxPath: "268435456\n"},
			dirs:      []string{procSelfCgroup},
			wantLimit: limit256, wantExact: false,
		},
		{
			name: "controller absent: v2 mount without memory files",
			files: map[string]string{
				procSelfCgroup: "0::/system.slice/x\n",
				filepath.Join(cgroupV2Base, "cgroup.controllers"): "cpu io\n",
			},
			wantExact: true,
		},
		{
			name: "ENOTDIR is absent",
			files: map[string]string{
				procSelfCgroup:                        "0::/docker/abc123\n",
				filepath.Join(cgroupV2Base, "docker"): "not a directory",
			},
			wantExact: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			for rel, content := range tt.files {
				writeFile(t, filepath.Join(root, rel), content)
			}
			for _, rel := range tt.dirs {
				require.NoError(t, os.MkdirAll(filepath.Join(root, rel), 0o755))
			}
			got, exact := detectCgroupLimit(root)
			assert.Equal(t, tt.wantLimit, got)
			assert.Equal(t, tt.wantExact, exact)
		})
	}
}

func TestDetectTotalMemoryAtEmptyRootIsExactHostRAM(t *testing.T) {
	t.Parallel()
	got, exact := DetectTotalMemoryAt(t.TempDir())
	assert.True(t, exact)
	assert.Equal(t, hostTotalMemory(), got)
}

func TestDetectTotalMemoryAtUnreadableIsUpperBound(t *testing.T) {
	t.Parallel()

	t.Run("unreadable leaf reports host RAM", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		writeFile(t, filepath.Join(root, procSelfCgroup), "0::/docker/abc123\n")
		require.NoError(t, os.MkdirAll(filepath.Join(root, cgroupV2Base, "docker", "abc123", cgroupV2File), 0o755))
		got, exact := DetectTotalMemoryAt(root)
		assert.False(t, exact)
		assert.Equal(t, hostTotalMemory(), got)
	})

	t.Run("readable leaf under unreadable ancestor clamps the bound", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		const leafLimit = 64 * mib
		writeFile(t, filepath.Join(root, procSelfCgroup), "0::/docker/abc123\n")
		writeFile(t, filepath.Join(root, cgroupV2Base, "docker", "abc123", cgroupV2File), "67108864\n")
		require.NoError(t, os.MkdirAll(filepath.Join(root, cgroupV2Base, "docker", cgroupV2File), 0o755))
		got, exact := DetectTotalMemoryAt(root)
		assert.False(t, exact)
		assert.Equal(t, min(hostTotalMemory(), int64(leafLimit)), got)
	})
}

func TestEffectiveTotal(t *testing.T) {
	t.Parallel()
	// cgroup limit lower than host -> cgroup wins (the container case).
	assert.Equal(t, int64(512*mib), effectiveTotal(8*gib, 512*mib))
	// cgroup unlimited (0) -> host wins.
	assert.Equal(t, int64(8*gib), effectiveTotal(8*gib, 0))
	// cgroup higher than host (unusual) -> host wins.
	assert.Equal(t, int64(2*gib), effectiveTotal(2*gib, 8*gib))
	// host unknown -> cgroup wins if present.
	assert.Equal(t, int64(512*mib), effectiveTotal(0, 512*mib))
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}
