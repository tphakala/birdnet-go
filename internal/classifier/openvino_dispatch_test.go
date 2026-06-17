//go:build !openvino

package classifier

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/tphakala/birdnet-go/internal/conf"
)

// TestShouldTryOpenVINO_FalseWithoutTag verifies that without the openvino build
// tag, shouldTryOpenVINO returns false regardless of config or CPU state.
func TestShouldTryOpenVINO_FalseWithoutTag(t *testing.T) {
	t.Parallel()
	// In the default build openvinoBackendAvailable is false, so the gate must
	// be false regardless of config/CPU.
	bn := &BirdNET{Settings: &conf.Settings{}}
	bn.Settings.BirdNET.Backend = conf.BackendPrefOpenVINO
	bn.ModelInfo = ModelInfo{ID: DefaultModelVersion, Backend: BackendONNX}
	assert.False(t, bn.shouldTryOpenVINO(),
		"without the openvino tag, shouldTryOpenVINO must be false")
}

// TestShouldTryOpenVINO_OptOut verifies that Backend="onnx" forces shouldTryOpenVINO
// to false even if everything else would allow it.
func TestShouldTryOpenVINO_OptOut(t *testing.T) {
	t.Parallel()
	// Backend="onnx" forces OFF even if everything else allows it.
	bn := &BirdNET{Settings: &conf.Settings{}}
	bn.Settings.BirdNET.Backend = conf.BackendPrefONNX
	bn.ModelInfo = ModelInfo{ID: DefaultModelVersion, Backend: BackendONNX}
	assert.False(t, bn.shouldTryOpenVINO())
}

// TestShouldTryOpenVINO_FalseForAutoWithoutTag pins the no-tag gate predicate
// for the default/auto backend: without the openvino build tag, shouldTryOpenVINO
// is false even when the model ID and CPU would otherwise qualify.
func TestShouldTryOpenVINO_FalseForAutoWithoutTag(t *testing.T) {
	t.Parallel()
	// Without the openvino tag, shouldTryOpenVINO is false, so initializeModel
	// on an ONNX-backed model must go straight to the ONNX path. We assert the
	// gate, which is the observable contract in a CI (no-tag) build.
	bn := &BirdNET{Settings: &conf.Settings{}}
	bn.ModelInfo = ModelInfo{ID: DefaultModelVersion, Backend: BackendONNX}
	assert.False(t, bn.shouldTryOpenVINO())
}
