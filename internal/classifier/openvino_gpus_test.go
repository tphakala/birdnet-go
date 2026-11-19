package classifier

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/inference"
)

// fakeOVCore replaces the core init, the probe and the loaded-path lookup that
// openVINOGPUs reads, and returns the library paths the probe was asked about.
// The tests using it must not run in parallel: the seams are package variables.
func fakeOVCore(t *testing.T, initErr error, devices []inference.OpenVINODevice, probeErr error) (probedPaths *[]string) {
	t.Helper()
	origInit, origProbe, origPath := ovInitCore, ovProbeDeviceInfo, ovLoadedLibraryPath
	t.Cleanup(func() { ovInitCore, ovProbeDeviceInfo, ovLoadedLibraryPath = origInit, origProbe, origPath })

	var paths []string
	ovInitCore = func(string) error { return initErr }
	ovLoadedLibraryPath = func() string { return "/loaded/libopenvino_c.so" }
	ovProbeDeviceInfo = func(path string) ([]inference.OpenVINODevice, error) {
		paths = append(paths, path)
		return devices, probeErr
	}
	return &paths
}

// multiGPUHost is the enumeration measured on a host with an iGPU and a
// discrete Arc card: OpenVINO lists only the indexed GPU names.
func multiGPUHost() []inference.OpenVINODevice {
	return []inference.OpenVINODevice{
		{Name: "CPU", Type: "integrated", FullName: "Test CPU"},
		{Name: "GPU.0", Type: "integrated", FullName: "Test iGPU"},
		{Name: "GPU.1", Type: "discrete", FullName: "Test Arc"},
	}
}

// TestOpenVINOGPUsRecognizesIndexedGPUs fails when the planner's GPU gate goes
// back to looking for the literal "GPU": the multi-GPU enumeration has none.
func TestOpenVINOGPUsRecognizesIndexedGPUs(t *testing.T) {
	paths := fakeOVCore(t, nil, multiGPUHost(), nil)

	gpus := openVINOGPUs("configured.so")

	assert.Equal(t, []inference.OpenVINODevice{multiGPUHost()[1], multiGPUHost()[2]}, gpus,
		"both indexed GPUs, and no CPU entry")
	assert.Equal(t, []string{"/loaded/libopenvino_c.so"}, *paths,
		"the probe describes the library the core loaded, not the configured path")
}

// TestPlannerGateAcceptsMultiGPUEnumeration runs the real gate function through
// the planner on [CPU, GPU.0, GPU.1], for the keywords and for each index.
func TestPlannerGateAcceptsMultiGPUEnumeration(t *testing.T) {
	fakeOVCore(t, nil, multiGPUHost(), nil)
	host := &ovHostFacts{
		goarch: goarchAMD64, ortAvailable: true,
		gpus: func() []inference.OpenVINODevice { return openVINOGPUs("") },
	}

	for _, tt := range []struct {
		devicePref, wantDevice, wantName string
	}{
		{conf.OVDeviceAuto, inference.OVDeviceGPU, "Test iGPU"},
		{conf.OVDeviceGPU, inference.OVDeviceGPU, "Test iGPU"},
		{"GPU.0", "GPU.0", "Test iGPU"},
		{"GPU.1", "GPU.1", "Test Arc"},
	} {
		device, name, ok, reason := planOpenVINODevice(conf.BackendPrefAuto, tt.devicePref, host)
		assert.Truef(t, ok, "device %q declined: %s", tt.devicePref, reason)
		assert.Equal(t, tt.wantDevice, device, tt.devicePref)
		assert.Equal(t, tt.wantName, name, tt.devicePref)
	}
}

func TestOpenVINOGPUsUnavailable(t *testing.T) {
	t.Run("core fails to load", func(t *testing.T) {
		paths := fakeOVCore(t, errors.NewStd("load failed"), multiGPUHost(), nil)
		assert.Empty(t, openVINOGPUs(""))
		assert.Empty(t, *paths, "no probe without a loaded core")
	})
	t.Run("probe fails", func(t *testing.T) {
		fakeOVCore(t, nil, multiGPUHost(), errors.NewStd("child crashed"))
		assert.Empty(t, openVINOGPUs(""))
	})
	t.Run("host without a GPU", func(t *testing.T) {
		fakeOVCore(t, nil, []inference.OpenVINODevice{{Name: "CPU"}}, nil)
		require.Empty(t, openVINOGPUs(""))
	})
}
