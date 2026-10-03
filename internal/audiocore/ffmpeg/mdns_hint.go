// mdns_hint.go - troubleshooting hints for .local (mDNS) hostname resolution failures.
package ffmpeg

import (
	"fmt"
	"net"
	"os"
	"strings"

	"github.com/tphakala/birdnet-go/internal/sysinfo"
)

// avahiSocketPath is the host Avahi daemon's Unix socket. In a container it is
// only present when the host's /run/avahi-daemon directory is bind-mounted.
const avahiSocketPath = "/run/avahi-daemon/socket"

// mdnsDocsHint points at the deployment-agnostic documentation.
const mdnsDocsHint = "See the 'Using .local (mDNS) hostnames in containers' section of the RTSP troubleshooting wiki page"

// mdnsEnv holds the environment facts that decide which .local hint applies.
type mdnsEnv struct {
	inContainer        bool
	avahiSocketPresent bool
}

// detectEnv probes the environment for the mDNS hint. It is a variable so tests
// can substitute a fixed environment.
var detectEnv = detectMDNSEnv

func detectMDNSEnv() mdnsEnv {
	env := mdnsEnv{inContainer: sysinfo.IsContainer()}
	if fi, err := os.Stat(avahiSocketPath); err == nil && fi.Mode()&os.ModeSocket != 0 {
		env.avahiSocketPresent = true
	}
	return env
}

// isMDNSHost reports whether host is a .local (mDNS) name. The suffix match is
// case-insensitive and ignores one trailing dot. IP literals and empty input
// return false.
func isMDNSHost(host string) bool {
	h := strings.TrimSuffix(strings.TrimSpace(host), ".")
	if h == "" || net.ParseIP(h) != nil {
		return false
	}
	return strings.HasSuffix(strings.ToLower(h), ".local")
}

// mdnsTroubleshooting returns troubleshooting steps for a failed lookup of the
// .local name host. The caller must have checked isMDNSHost.
func mdnsTroubleshooting(host string, env mdnsEnv) []string {
	switch {
	case env.inContainer && !env.avahiSocketPresent:
		return []string{
			fmt.Sprintf("'%s' is a .local (mDNS) name and the container cannot reach the host's Avahi daemon", host),
			"Mount the host's Avahi directory read-only: -v /run/avahi-daemon:/run/avahi-daemon:ro (never :z or :Z)",
			"If the mount is already configured, restart the container: the host directory may have been recreated",
			"Without avahi-daemon on the host, use the device's IP address or a router DNS name instead",
			mdnsDocsHint,
		}
	case env.inContainer:
		return []string{
			fmt.Sprintf("The host's Avahi daemon is reachable but did not find '%s'", host),
			"Check that the device is powered on and on the same network segment as the host",
			fmt.Sprintf("On the host, run: avahi-resolve -n %s", host),
		}
	default:
		return []string{
			fmt.Sprintf("'%s' is a .local (mDNS) name: check that avahi-daemon is running and libnss-mdns is installed on this system", host),
			fmt.Sprintf("Test resolution: getent hosts %s", host),
		}
	}
}
