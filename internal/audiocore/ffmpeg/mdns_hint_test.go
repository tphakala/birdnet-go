package ffmpeg

import (
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
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
