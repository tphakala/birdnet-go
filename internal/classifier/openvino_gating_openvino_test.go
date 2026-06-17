//go:build openvino

package classifier

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/cpuspec"
)

// TestShouldTryOpenVINO_Tagged_OptOut verifies Backend="onnx" opts out even when the
// openvino backend is compiled in.
func TestShouldTryOpenVINO_Tagged_OptOut(t *testing.T) {
	t.Parallel()
	bn := &BirdNET{Settings: &conf.Settings{}}
	bn.Settings.BirdNET.Backend = conf.BackendPrefONNX
	bn.ModelInfo = ModelInfo{ID: DefaultModelVersion, Backend: BackendONNX}
	assert.False(t, bn.shouldTryOpenVINO(), "Backend=onnx must opt out even with the openvino tag")
}

// TestShouldTryOpenVINO_Tagged_WrongModel verifies a non-v2.4 model never uses OpenVINO.
func TestShouldTryOpenVINO_Tagged_WrongModel(t *testing.T) {
	t.Parallel()
	bn := &BirdNET{Settings: &conf.Settings{}}
	bn.Settings.BirdNET.Backend = conf.BackendPrefAuto
	bn.ModelInfo = ModelInfo{ID: "BirdNET_V2.3", Backend: BackendONNX}
	assert.False(t, bn.shouldTryOpenVINO(), "a non-v2.4 model must not use OpenVINO in the PoC")
}

// TestShouldTryOpenVINO_Tagged_HardwareGateDecides verifies that with the tag, a
// supported model, and no opt-out, the native-f16 (ASIMDHP) capability is the final
// deciding factor: true on ARMv8.2 (rpi5), false elsewhere (amd64). This makes the
// test robust on any host.
func TestShouldTryOpenVINO_Tagged_HardwareGateDecides(t *testing.T) {
	t.Parallel()
	bn := &BirdNET{Settings: &conf.Settings{}}
	bn.Settings.BirdNET.Backend = conf.BackendPrefOpenVINO
	bn.ModelInfo = ModelInfo{ID: DefaultModelVersion, Backend: BackendONNX}
	assert.Equal(t, cpuspec.HasNativeF16(), bn.shouldTryOpenVINO(),
		"with tag, supported model, and opt-in, the result must equal native-f16 capability")
}
