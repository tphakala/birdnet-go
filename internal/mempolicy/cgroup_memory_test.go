package mempolicy

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDetectCgroupMemoryAt(t *testing.T) {
	t.Parallel()

	const (
		limit512 = int64(536870912)
		usage100 = int64(104857600)
		usage300 = int64(314572800)
	)
	v2Leaf := filepath.Join(cgroupV2Base, "docker", "abc123")
	v1Leaf := filepath.Join(cgroupV1MemBase, "docker", "abc123")

	tests := []struct {
		name  string
		files map[string]string // relative path to content
		want  CgroupMemory
	}{
		{
			name: "v2 leaf with limit and usage",
			files: map[string]string{
				procSelfCgroup:                                 "0::/docker/abc123\n",
				filepath.Join(v2Leaf, cgroupV2File):            "536870912\n",
				filepath.Join(v2Leaf, cgroupV2UsageFile):       "104857600\n",
				filepath.Join(cgroupV2Base, cgroupV2UsageFile): "999999999\n", // host-wide root file must not be read
			},
			want: CgroupMemory{Version: CgroupVersionV2, LimitBytes: limit512, LimitExact: true, UsageBytes: usage100},
		},
		{
			name: "v1 leaf with limit and usage",
			files: map[string]string{
				procSelfCgroup:                           "7:memory:/docker/abc123\n",
				filepath.Join(v1Leaf, cgroupV1File):      "536870912\n",
				filepath.Join(v1Leaf, cgroupV1UsageFile): "104857600\n",
			},
			want: CgroupMemory{Version: CgroupVersionV1, LimitBytes: limit512, LimitExact: true, UsageBytes: usage100},
		},
		{
			name: "no cgroup files",
			want: CgroupMemory{LimitExact: true},
		},
		{
			name: "v2 leaf missing memory.current falls back to the mount root",
			files: map[string]string{
				procSelfCgroup:                                 "0::/docker/abc123\n",
				filepath.Join(v2Leaf, cgroupV2File):            "max\n",
				filepath.Join(cgroupV2Base, cgroupV2UsageFile): "314572800\n",
			},
			want: CgroupMemory{Version: CgroupVersionV2, LimitExact: true, UsageBytes: usage300},
		},
		{
			name:  "v2 limit file without a usage file",
			files: map[string]string{cgroupV2MaxPath: "536870912\n"},
			want:  CgroupMemory{Version: CgroupVersionV2, LimitBytes: limit512, LimitExact: true},
		},
		{
			name:  "v1 limit file without a usage file",
			files: map[string]string{cgroupV1MaxPath: "536870912\n"},
			want:  CgroupMemory{Version: CgroupVersionV1, LimitBytes: limit512, LimitExact: true},
		},
		{
			name: "invalid usage content gives zero",
			files: map[string]string{
				cgroupV2MaxPath: "max\n",
				filepath.Join(cgroupV2Base, cgroupV2UsageFile): "garbage\n",
			},
			want: CgroupMemory{Version: CgroupVersionV2, LimitExact: true},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			for rel, content := range tt.files {
				p := filepath.Join(root, rel)
				require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o750))
				require.NoError(t, os.WriteFile(p, []byte(content), 0o600))
			}
			assert.Equal(t, tt.want, DetectCgroupMemoryAt(root))
		})
	}
}
