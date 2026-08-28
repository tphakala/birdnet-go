package conf

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

// TestValidateModelRegion verifies that the modes, empty, and well-formed slugs
// (including ones unknown to the embedded tables) are accepted, while a malformed
// value warns and normalizes to auto without invalidating the config.
func TestValidateModelRegion(t *testing.T) {
	t.Parallel()
	hasRegionWarning := func(r ValidationResult) bool {
		for _, w := range r.Warnings {
			if strings.Contains(w, "modelregion") {
				return true
			}
		}
		return false
	}

	// Empty, both modes, and well-formed slugs (a known one and a not-yet-known
	// one) are all accepted syntactically without a warning.
	normalizedRegion := func(t *testing.T, res ValidationResult) string {
		t.Helper()
		require.NotNil(t, res.Normalized)
		norm, ok := res.Normalized.(*BirdNETConfig)
		require.True(t, ok, "Normalized must be a *BirdNETConfig")
		return norm.ModelRegion
	}

	for _, region := range []string{"", ModelRegionAuto, ModelRegionGlobal, "iberia", "north-america-east", "future-family-slug"} {
		cfg := &BirdNETConfig{ModelRegion: region}
		res := ValidateBirdNETSettings(cfg)
		assert.Falsef(t, hasRegionWarning(res), "region %q must be accepted without a warning", region)
		assert.Equalf(t, region, normalizedRegion(t, res), "region %q must be left unchanged", region)
	}

	// A malformed value warns, stays valid, and normalizes to auto.
	for _, bad := range []string{"Iberia!", "north america", "UPPER", "-leading", "trailing-"} {
		cfg := &BirdNETConfig{ModelRegion: bad}
		res := ValidateBirdNETSettings(cfg)
		assert.Truef(t, hasRegionWarning(res), "malformed region %q must warn", bad)
		assert.Truef(t, res.Valid, "malformed region %q must not invalidate the config", bad)
		assert.Equalf(t, ModelRegionAuto, normalizedRegion(t, res), "malformed region %q must normalize to auto", bad)
	}
}
