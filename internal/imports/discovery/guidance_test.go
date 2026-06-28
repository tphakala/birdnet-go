package discovery

import (
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/sysinfo"
)

func TestBuildGuidance_NativeLinuxHasMountSteps(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != osLinux {
		t.Skip("native guidance is only built on Linux; macOS and Windows are reserved")
	}
	g := BuildGuidance("Bare Metal", "birdnet")
	require.NotNil(t, g)
	joined := strings.Join(g.Steps, "\n")
	assert.Contains(t, joined, "lsblk")
	assert.Contains(t, joined, "mount")
	// The configured run-as user is surfaced in the hint.
	assert.Contains(t, joined, "birdnet")
}

func TestBuildGuidance_NativeLinuxDefaultsUserWhenUnknown(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != osLinux {
		t.Skip("native guidance is only built on Linux; macOS and Windows are reserved")
	}
	g := BuildGuidance("Bare Metal", "")
	require.NotNil(t, g)
	assert.Contains(t, strings.Join(g.Steps, "\n"), "birdnet")
}

func TestBuildGuidance_DockerHasVolumeSteps(t *testing.T) {
	t.Parallel()
	g := BuildGuidance(sysinfo.EnvDocker, "")
	require.NotNil(t, g)
	assert.Equal(t, sysinfo.EnvDocker, g.Environment)
	assert.Contains(t, strings.Join(g.Steps, "\n"), "rslave")
}

func TestBuildGuidance_PodmanHasVolumeSteps(t *testing.T) {
	t.Parallel()
	g := BuildGuidance(sysinfo.EnvPodman, "")
	require.NotNil(t, g)
	assert.Equal(t, sysinfo.EnvPodman, g.Environment)
	assert.Contains(t, strings.Join(g.Steps, "\n"), "podman")
}
