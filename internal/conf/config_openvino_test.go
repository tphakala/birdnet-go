package conf

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBirdNETBackendPreferenceField(t *testing.T) {
	t.Parallel()
	var s Settings
	s.BirdNET.Backend = BackendPrefOpenVINO
	s.BirdNET.OpenVINOPath = "/usr/lib/libopenvino_c.so"
	assert.Equal(t, "openvino", s.BirdNET.Backend)
	assert.Equal(t, "/usr/lib/libopenvino_c.so", s.BirdNET.OpenVINOPath)
}

func TestBirdNETOpenVINODeviceField(t *testing.T) {
	t.Parallel()
	var s Settings
	s.BirdNET.OpenVINODevice = OVDeviceGPU
	assert.Equal(t, "gpu", s.BirdNET.OpenVINODevice)
}

// TestValidateOpenVINODevice verifies that known device preferences are accepted
// and an unknown value produces a (non-fatal) warning that falls back to auto.
func TestValidateOpenVINODevice(t *testing.T) {
	t.Parallel()
	hasDeviceWarning := func(r ValidationResult) bool {
		for _, w := range r.Warnings {
			if strings.Contains(w, "openvinodevice") {
				return true
			}
		}
		return false
	}

	for _, dev := range []string{"", OVDeviceAuto, OVDeviceCPU, OVDeviceGPU} {
		cfg := &BirdNETConfig{OpenVINODevice: dev}
		assert.Falsef(t, hasDeviceWarning(ValidateBirdNETSettings(cfg)),
			"device %q must be accepted without a warning", dev)
	}

	cfg := &BirdNETConfig{OpenVINODevice: "tpu"}
	res := ValidateBirdNETSettings(cfg)
	assert.True(t, hasDeviceWarning(res), "an unknown openvinodevice must warn")
	assert.True(t, res.Valid, "an unknown openvinodevice must not invalidate the config")
}
