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
		{"max\n", 0, false},
		{"max", 0, false},
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
		{"v1 unlimited sentinel", "9223372036854771712\n", 0, false},
		{"max int64 sentinel", "9223372036854775807\n", 0, false},
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
// the v2-first, v1-fallback logic with t.TempDir().
func TestDetectCgroupLimit(t *testing.T) {
	t.Parallel()

	t.Run("cgroup v2 limit", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		writeFile(t, filepath.Join(root, cgroupV2MaxPath), "536870912\n")
		got := detectCgroupLimit(root)
		assert.Equal(t, int64(536870912), got)
	})

	t.Run("cgroup v2 unlimited", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		writeFile(t, filepath.Join(root, cgroupV2MaxPath), "max\n")
		got := detectCgroupLimit(root)
		assert.Zero(t, got)
	})

	t.Run("cgroup v1 fallback", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		writeFile(t, filepath.Join(root, cgroupV1MaxPath), "268435456\n")
		got := detectCgroupLimit(root)
		assert.Equal(t, int64(268435456), got)
	})

	t.Run("no cgroup files", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		got := detectCgroupLimit(root)
		assert.Zero(t, got)
	})

	t.Run("cgroup v2 takes precedence over v1", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		writeFile(t, filepath.Join(root, cgroupV2MaxPath), "536870912\n")
		writeFile(t, filepath.Join(root, cgroupV1MaxPath), "268435456\n")
		got := detectCgroupLimit(root)
		assert.Equal(t, int64(536870912), got)
	})

	t.Run("cgroup v2 max short-circuits, v1 ignored", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		writeFile(t, filepath.Join(root, cgroupV2MaxPath), "max\n")
		writeFile(t, filepath.Join(root, cgroupV1MaxPath), "268435456\n")
		got := detectCgroupLimit(root)
		assert.Zero(t, got)
	})

	t.Run("cgroup v2 reads process subtree (host cgroupns)", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		writeFile(t, filepath.Join(root, procSelfCgroup), "0::/docker/abc123\n")
		// Only the subtree holds the cap; the mount root would be the host's.
		writeFile(t, filepath.Join(root, cgroupV2Base, "docker", "abc123", cgroupV2File), "536870912\n")
		got := detectCgroupLimit(root)
		assert.Equal(t, int64(536870912), got)
	})

	t.Run("cgroup v2 falls back to mount root when subtree file absent", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		writeFile(t, filepath.Join(root, procSelfCgroup), "0::/docker/abc123\n")
		writeFile(t, filepath.Join(root, cgroupV2MaxPath), "268435456\n")
		got := detectCgroupLimit(root)
		assert.Equal(t, int64(268435456), got)
	})

	t.Run("cgroup v1 reads memory controller subtree", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		writeFile(t, filepath.Join(root, procSelfCgroup), "7:memory:/docker/abc123\n3:cpu,cpuacct:/docker/abc123\n")
		writeFile(t, filepath.Join(root, cgroupV1MemBase, "docker", "abc123", cgroupV1File), "134217728\n")
		got := detectCgroupLimit(root)
		assert.Equal(t, int64(134217728), got)
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
