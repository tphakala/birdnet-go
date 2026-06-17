package inference

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNewOpenVINOClassifier_RequiresLabels verifies the labels precondition,
// which holds in every build (the check runs before any OpenVINO call).
func TestNewOpenVINOClassifier_RequiresLabels(t *testing.T) {
	t.Parallel()
	c, err := NewOpenVINOClassifier("/x.onnx", OpenVINOClassifierOptions{})
	require.Error(t, err)
	require.ErrorContains(t, err, "requires labels")
	assert.Nil(t, c)
}
