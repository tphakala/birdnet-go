package sysinfo

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// fakeMountProber returns a MountProber that always returns the supplied result.
func fakeMountProber(result MountProbeResult) MountProber {
	return func(_ string) MountProbeResult {
		return result
	}
}

// fakeEnvGetter returns an EnvGetter that always returns the supplied values.
func fakeEnvGetter(envType, detail string) EnvGetter {
	return func() (string, string) {
		return envType, detail
	}
}

// TestProbeExternalMount_NilDefaultsDoNotPanic ensures that nil dependencies
// fall back to the real implementations without panicking.
func TestProbeExternalMount_NilDefaultsDoNotPanic(t *testing.T) {
	t.Parallel()
	// Use a path that definitely does not exist to keep the test host-agnostic.
	result := ProbeExternalMount("/nonexistent-path-birdnet-test", nil, nil)
	// The path should not exist on a test host.
	assert.False(t, result.Exists, "nonexistent path should report Exists=false")
}

// TestProbeExternalMount_NativeNotContainerized tests the native (non-container) state.
// In native mode the mount prober still runs; the endpoint handler decides how to
// interpret the result based on environment. Here we verify the probe result
// is returned faithfully.
func TestProbeExternalMount_NativeNotContainerized(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		envType string
		probe   MountProbeResult
		want    MountProbeResult
	}{
		{
			name:    "native, path absent",
			envType: "Bare Metal",
			probe:   MountProbeResult{Exists: false, IsMountpoint: false, Readable: false},
			want:    MountProbeResult{Exists: false, IsMountpoint: false, Readable: false},
		},
		{
			name:    "native, path present but not mountpoint",
			envType: "Bare Metal",
			probe:   MountProbeResult{Exists: true, IsMountpoint: false, Readable: true},
			want:    MountProbeResult{Exists: true, IsMountpoint: false, Readable: true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := ProbeExternalMount(
				DefaultExternalMountPath,
				fakeEnvGetter(tt.envType, ""),
				fakeMountProber(tt.probe),
			)
			assert.Equal(t, tt.want, result)
		})
	}
}

// TestProbeExternalMount_ContainerMountPresent tests the container + mount-present state.
func TestProbeExternalMount_ContainerMountPresent(t *testing.T) {
	t.Parallel()

	probe := MountProbeResult{Exists: true, IsMountpoint: true, Readable: true}
	result := ProbeExternalMount(
		DefaultExternalMountPath,
		fakeEnvGetter("Docker", ""),
		fakeMountProber(probe),
	)

	assert.True(t, result.Exists, "mount should exist")
	assert.True(t, result.IsMountpoint, "should be a mountpoint")
	assert.True(t, result.Readable, "should be readable")
}

// TestProbeExternalMount_ContainerMountAbsent tests the container + mount-absent state.
func TestProbeExternalMount_ContainerMountAbsent(t *testing.T) {
	t.Parallel()

	probe := MountProbeResult{Exists: false, IsMountpoint: false, Readable: false}
	result := ProbeExternalMount(
		DefaultExternalMountPath,
		fakeEnvGetter("Docker", ""),
		fakeMountProber(probe),
	)

	assert.False(t, result.Exists, "mount should not exist")
	assert.False(t, result.IsMountpoint, "should not be a mountpoint")
	assert.False(t, result.Readable, "should not be readable")
}

// TestProbeExternalMount_PodmanMountAbsent tests the Podman container + mount-absent state.
func TestProbeExternalMount_PodmanMountAbsent(t *testing.T) {
	t.Parallel()

	probe := MountProbeResult{Exists: true, IsMountpoint: false, Readable: false}
	result := ProbeExternalMount(
		DefaultExternalMountPath,
		fakeEnvGetter("Podman", ""),
		fakeMountProber(probe),
	)

	// Path exists but is not a proper mountpoint.
	assert.True(t, result.Exists, "path exists")
	assert.False(t, result.IsMountpoint, "should not be a mountpoint")
}

// TestProbeExternalMount_CustomPath tests that the path argument is forwarded.
func TestProbeExternalMount_CustomPath(t *testing.T) {
	t.Parallel()

	var capturedPath string
	prober := func(path string) MountProbeResult {
		capturedPath = path
		return MountProbeResult{Exists: true, IsMountpoint: true, Readable: true}
	}

	customPath := "/custom/media"
	ProbeExternalMount(customPath, fakeEnvGetter("Docker", ""), prober)
	assert.Equal(t, customPath, capturedPath, "prober should receive the custom path")
}
