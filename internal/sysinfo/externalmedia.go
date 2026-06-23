// Package sysinfo detects the runtime environment (container, VM, bare metal)
// and provides CPU architecture and model information.
package sysinfo

import (
	"os"
	"syscall"
)

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
// present and mounted. envGetter and prober are injectable for testing; pass
// nil to use the production defaults.
func ProbeExternalMount(path string, envGetter EnvGetter, prober MountProber) MountProbeResult {
	if envGetter == nil {
		envGetter = GetEnvironment
	}
	if prober == nil {
		prober = realMountProber
	}
	return prober(path)
}

// realMountProber is the production implementation of MountProber.
func realMountProber(path string) MountProbeResult {
	result := MountProbeResult{}

	info, err := os.Stat(path)
	if err != nil {
		return result
	}
	result.Exists = true

	if !info.IsDir() {
		return result
	}

	// Check if path is a mountpoint by comparing its device ID to its parent.
	// A bind-mount or separate filesystem will have a different Dev value.
	var statPath, statParent syscall.Stat_t
	if err := syscall.Stat(path, &statPath); err != nil {
		return result
	}

	parent := path + "/.."
	if err := syscall.Stat(parent, &statParent); err != nil {
		return result
	}

	result.IsMountpoint = statPath.Dev != statParent.Dev

	// Check readability by opening the directory.
	f, err := os.Open(path) //nolint:gosec // path is a fixed constant, not user input
	if err != nil {
		return result
	}
	defer func() { _ = f.Close() }()

	_, err = f.Readdirnames(1)
	// io.EOF means the directory is empty but readable; nil means we got entries.
	result.Readable = err == nil || isEOF(err)

	return result
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

// isEOF reports whether err is io.EOF (directory is empty but readable).
func isEOF(err error) bool {
	if err == nil {
		return false
	}
	return err.Error() == "EOF"
}
