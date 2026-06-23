// Package sysinfo detects the runtime environment (container, VM, bare metal)
// and provides CPU architecture and model information.
package sysinfo

// DefaultExternalMountPath is the container path where the host external-media
// directory is expected to be bind-mounted (see install.sh / compose / systemd).
const DefaultExternalMountPath = "/external"

// MountProbeResult holds the result of probing a directory for mount status.
type MountProbeResult struct {
	// Exists reports whether the path exists (os.Stat succeeded).
	Exists bool
	// IsMountpoint reports whether the path is a distinct mount from its parent.
	// Determined by comparing device IDs; only meaningful when Exists is true.
	IsMountpoint bool
	// Readable reports whether the process can read directory entries.
	Readable bool
}

// EnvGetter returns (envType, detail) for the current runtime environment.
// The default implementation is sysinfo.GetEnvironment.
type EnvGetter func() (envType, detail string)

// MountProber probes a filesystem path and returns a MountProbeResult.
// The default implementation uses the real filesystem.
type MountProber func(path string) MountProbeResult

// ProbeExternalMount checks whether the external-media directory at path is
// present and mounted. prober is injectable for testing; pass nil to use the
// production default.
func ProbeExternalMount(path string, prober MountProber) MountProbeResult {
	if prober == nil {
		prober = realMountProber
	}
	return prober(path)
}

// IsContainerEnv reports whether the given envType string indicates a container
// runtime. It accepts the envType value returned by GetEnvironment or
// DetectEnvironment and mirrors the logic in IsContainer without requiring a
// live call to the cached singleton.
func IsContainerEnv(envType string) bool {
	switch envType {
	case envDocker, envPodman, envLXC, envNspawn, envContainerGen:
		return true
	default:
		return false
	}
}
