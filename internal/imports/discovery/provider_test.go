package discovery

import (
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/sysinfo"
)

func TestSelectProvider_ContainerReturnsMountRoot(t *testing.T) {
	t.Parallel()
	p := SelectProvider(sysinfo.EnvDocker, "/home/pi")
	roots := p.Roots()
	require.Len(t, roots, 1)
	assert.Equal(t, sysinfo.DefaultExternalMountPath, roots[0].Path)
	assert.Equal(t, KindLocal, roots[0].Kind)
}

func TestSelectProvider_NativeIncludesHomeAndRemovable(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != osLinux {
		t.Skip("native discovery roots are only populated on Linux; macOS and Windows providers are reserved stubs")
	}
	p := SelectProvider("Bare Metal", "/home/pi")
	roots := p.Roots()
	paths := make(map[string]Kind, len(roots))
	for _, r := range roots {
		paths[r.Path] = r.Kind
	}
	assert.Equal(t, KindLocal, paths["/home/pi/BirdNET-Pi"])
	assert.Equal(t, KindRemovable, paths["/media"])
	assert.Equal(t, KindRemovable, paths["/mnt"])
}
