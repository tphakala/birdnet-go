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
	c, ok := tryPerchOpenVINO(&PerchConfig{
		ModelPath:      "/models/perch_v2.onnx",
		Backend:        conf.BackendPrefOpenVINO,
		OpenVINODevice: conf.OVDeviceAuto,
	}, []string{"a", "b"})
	assert.False(t, ok, "stock perch_v2 must not use OpenVINO")
	assert.Nil(t, c)
}
