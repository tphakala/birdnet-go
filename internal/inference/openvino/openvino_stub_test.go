//go:build !openvino

package openvino

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStub_ReturnsUnavailable(t *testing.T) {
	t.Parallel()
	require.ErrorIs(t, InitOV(""), ErrOpenVINOUnavailable)

	c, err := NewClassifier("/nonexistent.onnx", Options{PrecisionHint: "f16"})
	assert.Nil(t, c)
	require.ErrorIs(t, err, ErrOpenVINOUnavailable)

	require.ErrorIs(t, DestroyOV(), ErrOpenVINOUnavailable)
}
