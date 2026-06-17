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
	assert.Nil(t, c)
	require.Error(t, err)
	assert.ErrorContains(t, err, "requires labels")
}
