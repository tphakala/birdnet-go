package ffmpeg

import (
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/sysinfo"
)

func TestIsMDNSHost(t *testing.T) {
	t.Parallel()
	tests := []struct {
		host string
		want bool
	}{
		{"cam.local", true},
		{"CAM.LOCAL.", true},
		{"cam.local.example.com", false},
		{"cam.lan", false},
		{"local", false},
		{"192.168.1.5", false},
		{"", false},
	}
	for _, tt := range tests {
		t.Run(tt.host, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, isMDNSHost(tt.host))
		})
	}
}

func TestMDNSTroubleshooting_ByEnvironment(t *testing.T) {
	t.Parallel()
	const mount = "-v /run/avahi-daemon:/run/avahi-daemon:ro"
	tests := []struct {
		name string
		env  mdnsEnv
		want string
	}{
		{"container without socket", mdnsEnv{inContainer: true}, mount},
		{"container with socket", mdnsEnv{inContainer: true, avahiSocketPresent: true}, "avahi-resolve -n cam.local"},
		// The socket file existing does not prove the daemon is usable (SELinux can deny the connect).
		{"container with socket names denied access", mdnsEnv{inContainer: true, avahiSocketPresent: true}, "access to the socket was denied"},
		{"container with socket points to the docs", mdnsEnv{inContainer: true, avahiSocketPresent: true}, "RTSP troubleshooting wiki"},
		{"native", mdnsEnv{}, "getent hosts cam.local"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if tt.name == "native" && runtime.GOOS != "linux" {
				t.Skip("native avahi and getent steps are Linux only")
			}
			joined := strings.Join(mdnsTroubleshooting("cam.local", tt.env), "\n")
			assert.Contains(t, joined, tt.want)
			assert.Equal(t, tt.name == "container without socket", strings.Contains(joined, mount), "mount text only without socket")
		})
	}
}

func TestMDNSContainerEnv(t *testing.T) {
	t.Parallel()
	tests := []struct {
		envType string
		want    bool
	}{
		{sysinfo.EnvDocker, true},
		{sysinfo.EnvPodman, true},
		{sysinfo.EnvLXC, false},
		{sysinfo.EnvNspawn, false},
		{sysinfo.EnvContainerGen, false},
		{"", false},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, mdnsContainerEnv(tt.envType), "envType %q", tt.envType)
	}
}

func TestIsUnixSocket(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	sockPath := filepath.Join(dir, "s")
	ln, err := net.Listen("unix", sockPath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	assert.True(t, isUnixSocket(sockPath), "listening Unix socket")

	regular := filepath.Join(dir, "regular")
	require.NoError(t, os.WriteFile(regular, nil, 0o600))
	assert.False(t, isUnixSocket(regular), "regular file")
	assert.False(t, isUnixSocket(dir), "directory")
	assert.False(t, isUnixSocket(filepath.Join(dir, "missing")), "missing path")
}

func TestNativeMDNSSteps(t *testing.T) {
	t.Parallel()
	linux := strings.Join(nativeMDNSSteps("cam.local", "linux"), "\n")
	assert.Contains(t, linux, "libnss-mdns")
	assert.Contains(t, linux, "getent hosts cam.local")

	for _, goos := range []string{"windows", "darwin"} {
		other := strings.Join(nativeMDNSSteps("cam.local", goos), "\n")
		assert.Contains(t, other, "same network", goos)
		assert.NotContains(t, other, "avahi-daemon", goos)
		assert.NotContains(t, other, "getent", goos)
	}
}
