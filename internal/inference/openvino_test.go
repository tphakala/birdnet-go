package inference

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	ov "github.com/tphakala/birdnet-go/internal/inference/openvino"
)

func TestNewOpenVINOClassifier_UnavailableWithoutTag(t *testing.T) {
	t.Parallel()
	// In the default (no openvino tag) build, construction must fail with the
	// sentinel so the classifier dispatch falls back to ORT.
	c, err := NewOpenVINOClassifier("/x.onnx", OpenVINOClassifierOptions{Labels: []string{"a"}})
	assert.Nil(t, c)
	assert.ErrorIs(t, err, ov.ErrOpenVINOUnavailable)
}

func TestNewOpenVINOClassifier_RequiresLabels(t *testing.T) {
	t.Parallel()
	c, err := NewOpenVINOClassifier("/x.onnx", OpenVINOClassifierOptions{})
	require.Error(t, err)
	require.ErrorContains(t, err, "requires labels")
	assert.Nil(t, c)
}

// TestInitOpenVINO_GuardWithoutTag verifies that without the openvino build tag,
// InitOpenVINO returns an error via the stub and does NOT mark the runtime as
// initialized, leaving DestroyOpenVINO a safe no-op.
func TestInitOpenVINO_GuardWithoutTag(t *testing.T) {
	// Do not call t.Parallel: this test touches package-global init state.
	require.Error(t, InitOpenVINO(""))
	assert.False(t, IsOpenVINOInitialized())
	assert.NoError(t, DestroyOpenVINO()) // no-op when never initialized
}
