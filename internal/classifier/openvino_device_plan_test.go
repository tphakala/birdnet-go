package classifier

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/inference"
)

// fakeOVHost builds injected host facts and a counter of GPU probe calls. A
// present GPU is the lone bare "GPU" a single-GPU host enumerates.
func fakeOVHost(goarch string, nativeF16, ortAvailable, gpuPresent bool) (host *ovHostFacts, gpuProbes *int) {
	var gpus []inference.OpenVINODevice
	if gpuPresent {
		gpus = []inference.OpenVINODevice{{Name: inference.OVDeviceGPU}}
	}
	return fakeOVHostWithGPUs(goarch, nativeF16, ortAvailable, gpus)
}

// fakeOVHostWithGPUs is fakeOVHost with an explicit GPU list.
func fakeOVHostWithGPUs(goarch string, nativeF16, ortAvailable bool, gpus []inference.OpenVINODevice) (host *ovHostFacts, gpuProbes *int) {
	probes := 0
	return &ovHostFacts{
		goarch:       goarch,
		nativeF16:    nativeF16,
		ortAvailable: ortAvailable,
		gpus: func() []inference.OpenVINODevice {
			probes++
			return gpus
		},
	}, &probes
}

func TestPlanOpenVINODevice(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		goarch       string
		nativeF16    bool
		ortAvailable bool
		gpuPresent   bool
		backendPref  string
		devicePref   string
		wantDevice   string
		wantOK       bool
		wantReason   OpenVINOReason
		wantProbed   bool
	}{
		{"arm64 A76 auto/auto ORT ok declines before the GPU probe", goarchARM64, true, true, false, conf.BackendPrefAuto, conf.OVDeviceAuto, "", false, ovReasonORTPreferredARM64, false},
		{"arm64 A76 empty prefs ORT ok declines like auto", goarchARM64, true, true, false, "", "", "", false, ovReasonORTPreferredARM64, false},
		{"arm64 A76 auto/auto ORT missing keeps the CPU", goarchARM64, true, false, false, conf.BackendPrefAuto, conf.OVDeviceAuto, inference.OVDeviceCPU, true, "", true},
		{"arm64 A76 explicit openvino backend keeps the CPU", goarchARM64, true, true, false, conf.BackendPrefOpenVINO, conf.OVDeviceAuto, inference.OVDeviceCPU, true, "", true},
		{"arm64 A76 explicit cpu device keeps the CPU", goarchARM64, true, true, false, conf.BackendPrefAuto, conf.OVDeviceCPU, inference.OVDeviceCPU, true, "", false},
		{"arm64 A76 explicit gpu device still probes", goarchARM64, true, true, false, conf.BackendPrefAuto, conf.OVDeviceGPU, "", false, ovReasonGPUUnavailable, true},
		{"arm64 A72 auto/auto keeps the no-device reason", goarchARM64, false, true, false, conf.BackendPrefAuto, conf.OVDeviceAuto, "", false, ovReasonNoDevice, false},
		{"arm64 A72 auto/auto ORT missing never reaches the GPU probe", goarchARM64, false, false, false, conf.BackendPrefAuto, conf.OVDeviceAuto, "", false, ovReasonNoDevice, false},
		{"arm64 A72 explicit cpu is rejected by the f16 gate", goarchARM64, false, true, false, conf.BackendPrefOpenVINO, conf.OVDeviceCPU, "", false, ovReasonCPUUnsupported, false},
		{"amd64 auto/auto with a GPU picks the GPU", goarchAMD64, false, true, true, conf.BackendPrefAuto, conf.OVDeviceAuto, inference.OVDeviceGPU, true, "", true},
		{"amd64 auto/auto without a GPU never plans the CPU", goarchAMD64, false, true, false, conf.BackendPrefAuto, conf.OVDeviceAuto, "", false, ovReasonNoDevice, true},
		{"amd64 explicit cpu is allowed", goarchAMD64, false, true, false, conf.BackendPrefAuto, conf.OVDeviceCPU, inference.OVDeviceCPU, true, "", false},
		{"arm64 onnx backend declines without probing", goarchARM64, true, true, true, conf.BackendPrefONNX, conf.OVDeviceAuto, "", false, ovReasonBackendONNX, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			host, probes := fakeOVHost(tt.goarch, tt.nativeF16, tt.ortAvailable, tt.gpuPresent)
			device, _, ok, reason := planOpenVINODevice(tt.backendPref, tt.devicePref, host)
			assert.Equal(t, tt.wantDevice, device)
			assert.Equal(t, tt.wantOK, ok)
			assert.Equal(t, tt.wantReason, reason)
			assert.Equal(t, tt.wantProbed, *probes > 0, "GPU probe calls: %d", *probes)
		})
	}

	t.Run("settings transitions are re-read on every plan", func(t *testing.T) {
		t.Parallel()
		host, _ := fakeOVHost(goarchARM64, true, true, false)
		_, _, ok, _ := planOpenVINODevice(conf.BackendPrefAuto, conf.OVDeviceAuto, host)
		assert.False(t, ok, "auto declines")
		device, _, ok, _ := planOpenVINODevice(conf.BackendPrefOpenVINO, conf.OVDeviceAuto, host)
		assert.True(t, ok, "openvino opts in")
		assert.Equal(t, inference.OVDeviceCPU, device)
		_, _, ok, _ = planOpenVINODevice(conf.BackendPrefAuto, conf.OVDeviceAuto, host)
		assert.False(t, ok, "auto declines again")
	})
}

// TestPlanOpenVINODeviceGPUList covers the GPU choice on hosts that enumerate
// several GPUs: "gpu" and "auto" keep meaning OpenVINO's GPU alias, a concrete
// "GPU.<n>" runs only on a device enumerated under exactly that name, and a
// missing index declines instead of substituting another adapter.
func TestPlanOpenVINODeviceGPUList(t *testing.T) {
	t.Parallel()

	igpu := inference.OpenVINODevice{Name: "GPU.0", Type: "integrated", FullName: "Test iGPU"}
	arc := inference.OpenVINODevice{Name: "GPU.1", Type: "discrete", FullName: "Test Arc"}
	lone := inference.OpenVINODevice{Name: "GPU", Type: "integrated", FullName: "Lone GPU"}
	both := []inference.OpenVINODevice{igpu, arc}

	tests := []struct {
		name        string
		gpus        []inference.OpenVINODevice
		backendPref string
		devicePref  string
		wantDevice  string
		wantName    string
		wantOK      bool
		wantReason  OpenVINOReason
		wantProbed  bool
	}{
		{"gpu on two GPUs uses the alias and names GPU.0", both, conf.BackendPrefAuto, conf.OVDeviceGPU, inference.OVDeviceGPU, "Test iGPU", true, "", true},
		{"auto on two GPUs uses the alias and names GPU.0", both, conf.BackendPrefAuto, conf.OVDeviceAuto, inference.OVDeviceGPU, "Test iGPU", true, "", true},
		{"gpu on a lone GPU names it", []inference.OpenVINODevice{lone}, conf.BackendPrefAuto, conf.OVDeviceGPU, inference.OVDeviceGPU, "Lone GPU", true, "", true},
		{"GPU.1 on two GPUs selects the second", both, conf.BackendPrefAuto, "GPU.1", "GPU.1", "Test Arc", true, "", true},
		{"GPU.0 on two GPUs selects the first", both, conf.BackendPrefAuto, "GPU.0", "GPU.0", "Test iGPU", true, "", true},
		{"GPU.1 with openvino backend selects the second", both, conf.BackendPrefOpenVINO, "GPU.1", "GPU.1", "Test Arc", true, "", true},
		{"GPU.1 on a lone GPU declines", []inference.OpenVINODevice{lone}, conf.BackendPrefAuto, "GPU.1", "", "", false, ovReasonGPUDeviceMissing, true},
		{"GPU.0 on a lone GPU declines, no alias matching", []inference.OpenVINODevice{lone}, conf.BackendPrefAuto, "GPU.0", "", "", false, ovReasonGPUDeviceMissing, true},
		{"GPU.5 on two GPUs declines", both, conf.BackendPrefAuto, "GPU.5", "", "", false, ovReasonGPUDeviceMissing, true},
		{"GPU.1 without any GPU declines", nil, conf.BackendPrefAuto, "GPU.1", "", "", false, ovReasonGPUDeviceMissing, true},
		{"GPU.1 with backend onnx declines without probing", both, conf.BackendPrefONNX, "GPU.1", "", "", false, ovReasonBackendONNX, false},
		{"a GPU name without a name from the child still plans", []inference.OpenVINODevice{{Name: "GPU.0"}, {Name: "GPU.1"}}, conf.BackendPrefAuto, "GPU.1", "GPU.1", "", true, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			host, probes := fakeOVHostWithGPUs(goarchAMD64, false, true, tt.gpus)
			device, name, ok, reason := planOpenVINODevice(tt.backendPref, tt.devicePref, host)
			assert.Equal(t, tt.wantDevice, device)
			assert.Equal(t, tt.wantName, name)
			assert.Equal(t, tt.wantOK, ok)
			assert.Equal(t, tt.wantReason, reason)
			assert.Equal(t, tt.wantProbed, *probes > 0, "GPU probe calls: %d", *probes)
		})
	}

	t.Run("a fixed GPU.1 preference follows the hardware across hot reloads", func(t *testing.T) {
		t.Parallel()
		gpus := both
		host := &ovHostFacts{
			goarch: goarchAMD64, ortAvailable: true,
			gpus: func() []inference.OpenVINODevice { return gpus },
		}
		plan := func() (string, bool) {
			device, _, ok, _ := planOpenVINODevice(conf.BackendPrefAuto, "GPU.1", host)
			return device, ok
		}

		device, ok := plan()
		assert.True(t, ok)
		assert.Equal(t, "GPU.1", device)

		gpus = []inference.OpenVINODevice{lone} // the iGPU was disabled, the Arc is now plain GPU
		_, ok = plan()
		assert.False(t, ok, "the stored index is gone, so OpenVINO is declined")

		gpus = both
		device, ok = plan()
		assert.True(t, ok)
		assert.Equal(t, "GPU.1", device)
	})
}

// TestPlanConcreteGPUWarnsOncePerHardware pins the once-per-(preference, GPUs
// found) rule of the missing-GPU warning: a repeat on unchanged hardware stays
// silent, a different set of GPUs or a different preference warns again, and the
// line names both the configured device and the GPUs found. It swaps the global
// logger, so it must not run in parallel.
func TestPlanConcreteGPUWarnsOncePerHardware(t *testing.T) {
	ovGPUMissingWarned.Clear()
	t.Cleanup(ovGPUMissingWarned.Clear)
	buf := newCaptureLogger(t)
	const warnLine = "configured OpenVINO GPU is not present"
	lone := []inference.OpenVINODevice{{Name: "GPU"}}
	both := []inference.OpenVINODevice{{Name: "GPU.0"}, {Name: "GPU.1"}}

	_, _, ok, reason := planConcreteGPU("GPU.1", lone)
	assert.False(t, ok)
	assert.Equal(t, ovReasonGPUDeviceMissing, reason)
	assert.Equal(t, 1, strings.Count(buf.String(), warnLine))
	assert.Contains(t, buf.String(), "GPU.1", "the warning names the configured device")
	assert.Contains(t, buf.String(), "gpus_found=GPU", "the warning names the GPUs found")

	planConcreteGPU("GPU.1", lone)
	assert.Equal(t, 1, strings.Count(buf.String(), warnLine), "same preference on the same hardware warns once")

	planConcreteGPU("GPU.5", both)
	assert.Equal(t, 2, strings.Count(buf.String(), warnLine), "another preference or other hardware warns again")

	planConcreteGPU("GPU.5", lone)
	assert.Equal(t, 3, strings.Count(buf.String(), warnLine), "changed hardware warns again")
}

// TestOVGPUDeviceNamesAgree is the drift guard between the config package's
// pattern for a concrete GPU name and the inference package's device-class
// check, which conf cannot import.
func TestOVGPUDeviceNamesAgree(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"GPU", "GPU.0", "GPU.1", "GPU.12", "GPU.", "GPU.x", "GPU.1x", "GPU.-1", "gpu.1", "GPUX", "CPU", "NPU", ""} {
		wantIndexed := inference.OpenVINOIsGPUDevice(name) && name != inference.OVDeviceGPU
		assert.Equalf(t, wantIndexed, conf.IsOVGPUDeviceName(name), "name %q", name)
	}
}

// TestPlanOpenVINODevice_GPUFailed covers the planner while the process-wide
// OpenVINO GPU fault latch is set: the GPU is never planned and never probed,
// the CPU stays plannable where it was.
func TestPlanOpenVINODevice_GPUFailed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		goarch       string
		nativeF16    bool
		ortAvailable bool
		backendPref  string
		devicePref   string
		wantDevice   string
		wantOK       bool
		wantReason   OpenVINOReason
	}{
		{"amd64 auto/auto with a GPU present declines", goarchAMD64, false, true, conf.BackendPrefAuto, conf.OVDeviceAuto, "", false, ovReasonGPUFailed},
		{"amd64 explicit gpu declines", goarchAMD64, false, true, conf.BackendPrefAuto, conf.OVDeviceGPU, "", false, ovReasonGPUFailed},
		{"amd64 concrete GPU.1 declines", goarchAMD64, false, true, conf.BackendPrefAuto, "GPU.1", "", false, ovReasonGPUFailed},
		{"amd64 explicit cpu still plans the CPU", goarchAMD64, false, true, conf.BackendPrefAuto, conf.OVDeviceCPU, inference.OVDeviceCPU, true, ""},
		{"arm64 A76 auto with ORT missing plans the CPU", goarchARM64, true, false, conf.BackendPrefAuto, conf.OVDeviceAuto, inference.OVDeviceCPU, true, ""},
		{"onnx backend keeps its own reason", goarchAMD64, false, true, conf.BackendPrefONNX, conf.OVDeviceGPU, "", false, ovReasonBackendONNX},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			host, probes := fakeOVHost(tt.goarch, tt.nativeF16, tt.ortAvailable, true)
			host.gpuFailed = true
			device, name, ok, reason := planOpenVINODevice(tt.backendPref, tt.devicePref, host)
			assert.Equal(t, tt.wantDevice, device)
			assert.Empty(t, name)
			assert.Equal(t, tt.wantOK, ok)
			assert.Equal(t, tt.wantReason, reason)
			assert.Zero(t, *probes, "a latched GPU must not be probed")
		})
	}
}

// TestLiveOVHostFactsReflectsGPUFault pins the wiring from the OpenVINO latch to
// the planner facts. Not parallel: it replaces the ovGPUFault seam.
func TestLiveOVHostFactsReflectsGPUFault(t *testing.T) {
	orig := ovGPUFault
	t.Cleanup(func() { ovGPUFault = orig })

	ovGPUFault = func() (bool, error) { return false, nil }
	assert.False(t, liveOVHostFacts("", true).gpuFailed)

	ovGPUFault = func() (bool, error) { return true, assert.AnError }
	assert.True(t, liveOVHostFacts("", true).gpuFailed)
}
