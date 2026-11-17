package classifier

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/inference"
)

// fakeOVHost builds injected host facts and a counter of GPU probe calls.
func fakeOVHost(goarch string, nativeF16, ortAvailable, gpuPresent bool) (host *ovHostFacts, gpuProbes *int) {
	probes := 0
	return &ovHostFacts{
		goarch:       goarch,
		nativeF16:    nativeF16,
		ortAvailable: ortAvailable,
		gpuAvailable: func() bool {
			probes++
			return gpuPresent
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
		wantReason   string
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
			device, ok, reason := planOpenVINODevice(tt.backendPref, tt.devicePref, host)
			assert.Equal(t, tt.wantDevice, device)
			assert.Equal(t, tt.wantOK, ok)
			assert.Equal(t, tt.wantReason, reason)
			assert.Equal(t, tt.wantProbed, *probes > 0, "GPU probe calls: %d", *probes)
		})
	}

	t.Run("settings transitions are re-read on every plan", func(t *testing.T) {
		t.Parallel()
		host, _ := fakeOVHost(goarchARM64, true, true, false)
		_, ok, _ := planOpenVINODevice(conf.BackendPrefAuto, conf.OVDeviceAuto, host)
		assert.False(t, ok, "auto declines")
		device, ok, _ := planOpenVINODevice(conf.BackendPrefOpenVINO, conf.OVDeviceAuto, host)
		assert.True(t, ok, "openvino opts in")
		assert.Equal(t, inference.OVDeviceCPU, device)
		_, ok, _ = planOpenVINODevice(conf.BackendPrefAuto, conf.OVDeviceAuto, host)
		assert.False(t, ok, "auto declines again")
	})
}
