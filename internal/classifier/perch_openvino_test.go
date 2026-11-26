package classifier

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// TestIsPerchNoDFT pins the filename-based detection of the OpenVINO-compatible
// Perch no_dft model variant. Stock perch_v2.onnx (and any other file) must not
// match, since only no_dft compiles on OpenVINO.
func TestIsPerchNoDFT(t *testing.T) {
	t.Parallel()
	cases := []struct {
		path string
		want bool
	}{
		{"/models/perch_v2_no_dft.onnx", true},
		{"/models/perch_v2_no-dft.onnx", true},
		{"perch_v2_NO_DFT.onnx", true}, // case-insensitive
		{"/data/Perch-No-DFT.onnx", true},
		{"/models/perch_v2.onnx", false},
		{"/models/birdnet-v24.onnx", false},
		{"", false},
	}
	for _, tc := range cases {
		assert.Equalf(t, tc.want, isPerchNoDFT(tc.path), "isPerchNoDFT(%q)", tc.path)
	}
}

// TestTryPerchOpenVINO_StockModelFallsBack verifies that a stock (non-no_dft)
// Perch model never attempts OpenVINO and falls back to ORT, regardless of build
// tag or host. The isPerchNoDFT gate short-circuits before any library load, so
// this is deterministic everywhere (no libopenvino_c required).
func TestTryPerchOpenVINO_StockModelFallsBack(t *testing.T) {
	t.Parallel()
	c, device, precisionHint, ok := tryPerchOpenVINO(&PerchConfig{
		ModelPath:      "/models/perch_v2.onnx",
		Backend:        conf.BackendPrefOpenVINO,
		OpenVINODevice: conf.OVDeviceAuto,
	}, []string{"a", "b"})
	assert.False(t, ok, "stock perch_v2 must not use OpenVINO")
	assert.Nil(t, c)
	assert.Empty(t, device, "fallback path must not report an OpenVINO device")
	assert.Empty(t, precisionHint, "fallback path must not report an OpenVINO precision hint")
}

// TestPerchOpenVINOPlanPrecedence pins the reason order for Perch: backend onnx,
// then a binary without OpenVINO, then the stock model file. A stock file on a
// build without OpenVINO reports not built, not the file.
func TestPerchOpenVINOPlanPrecedence(t *testing.T) {
	t.Parallel()
	const stock, noDFT = "/models/perch_v2.onnx", "/models/perch_v2_no_dft.onnx"
	tests := []struct {
		name      string
		path      string
		backend   string
		available bool
		want      OpenVINOReason
	}{
		{"stock, unavailable, openvino", stock, conf.BackendPrefOpenVINO, false, ovReasonNotBuilt},
		{"stock, unavailable, auto", stock, conf.BackendPrefAuto, false, ovReasonNotBuilt},
		{"stock, onnx, available", stock, conf.BackendPrefONNX, true, ovReasonBackendONNX},
		{"stock, onnx, unavailable", stock, conf.BackendPrefONNX, false, ovReasonBackendONNX},
		{"stock, available, openvino", stock, conf.BackendPrefOpenVINO, true, ovReasonNotPerchNoDFT},
		{"no_dft, unavailable", noDFT, conf.BackendPrefOpenVINO, false, ovReasonNotBuilt},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, ok, reason := perchOpenVINOPlan(&PerchConfig{ModelPath: tt.path, Backend: tt.backend}, tt.available)
			assert.False(t, ok)
			assert.Equal(t, tt.want, reason)
		})
	}
}
