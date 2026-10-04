// mdns_hint.go - troubleshooting hints for .local (mDNS) hostname resolution failures.
package ffmpeg

import (
	"fmt"
	"net"
	"os"
	"runtime"
	"strings"

	"github.com/tphakala/birdnet-go/internal/sysinfo"
)

// avahiSocketPath is the host Avahi daemon's Unix socket. In a container it is
// only present when the host's /run/avahi-daemon directory is bind-mounted.
const avahiSocketPath = "/run/avahi-daemon/socket"

// mdnsDocsHint points at the deployment-agnostic documentation.
const mdnsDocsHint = "See the 'Using .local (mDNS) hostnames in containers' section of the RTSP troubleshooting wiki page"

// goosLinux is the runtime.GOOS value for Linux, the only system with the
// avahi-daemon, libnss-mdns and getent steps.
const goosLinux = "linux"

// mdnsEnv holds the environment facts that decide which .local hint applies.
type mdnsEnv struct {
	inContainer        bool
	avahiSocketPresent bool
	goos               string // runtime.GOOS of the running process
}

// detectEnv probes the environment for the mDNS hint. It is a variable so tests
// can substitute a fixed environment.
var detectEnv = detectMDNSEnv

// detectMDNSEnv probes the running system for the mDNS hint.
func detectMDNSEnv() mdnsEnv {
	envType, _ := sysinfo.GetEnvironment()
	return newMDNSEnv(envType, runtime.GOOS, avahiSocketPath)
}

// newMDNSEnv builds an mdnsEnv from probe results: the sysinfo environment type,
// the operating system name and the path of the Avahi socket to look for. It
// takes them as arguments so tests can cover every combination.
func newMDNSEnv(envType, goos, avahiSocket string) mdnsEnv {
	return mdnsEnv{
		inContainer:        mdnsContainerEnv(envType),
		avahiSocketPresent: isUnixSocket(avahiSocket),
		goos:               goos,
	}
}

// mdnsContainerEnv reports whether envType is a Docker or Podman container, where
// the host's Avahi daemon is reached through a bind mount. LXC and nspawn installs
// run BirdNET-Go natively and resolve .local names like any other Linux host.
func mdnsContainerEnv(envType string) bool {
	return envType == sysinfo.EnvDocker || envType == sysinfo.EnvPodman
}

// isUnixSocket reports whether path exists and is a Unix socket.
func isUnixSocket(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.Mode()&os.ModeSocket != 0
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
			"Mount the host's Avahi directory read-only, unless the deployment already does (the Podman quadlets do): -v /run/avahi-daemon:/run/avahi-daemon:ro (never :z or :Z)",
			"If the mount is already configured, restart the container: the host directory may have been recreated. Under a Podman quadlet, restart its systemd unit instead (for the unit podman-install.sh installs: systemctl --user restart birdnet-go), because a container restart does not rerun the check that links the Avahi directory",
			"Without avahi-daemon on the host, use the device's IP address or a router DNS name instead",
			mdnsDocsHint,
		}
	case env.inContainer:
		return []string{
			fmt.Sprintf("The host's Avahi socket is mounted but the lookup of '%s' failed: the device was not found, or access to the socket was denied (for example by SELinux)", host),
			"Check that the device is powered on and on the same network segment as the host",
			fmt.Sprintf("On the host, run: avahi-resolve -n %s", host),
			mdnsDocsHint,
		}
	default:
		return nativeMDNSSteps(host, env.goos)
	}
}

// nativeMDNSSteps returns the .local steps for a native install. The avahi-daemon,
// libnss-mdns and getent steps apply only to Linux; other systems resolve .local
// names themselves.
func nativeMDNSSteps(host, goos string) []string {
	if goos != goosLinux {
		return []string{
			fmt.Sprintf("'%s' is a .local (mDNS) name: check that the device is powered on and on the same network as this computer", host),
		}
	}
	return []string{
		fmt.Sprintf("'%s' is a .local (mDNS) name: check that avahi-daemon is running and libnss-mdns is installed on this system", host),
		fmt.Sprintf("Test resolution: getent hosts %s", host),
	}
}
