package onnx

import (
	"testing"

	ort "github.com/microsoft/onnxruntime/go/onnxruntime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestValidateOutputShapes_RejectsMissingAndMiscountedOutputs covers the checks
// that need no native library: the number of returned outputs and a requested
// output that is absent from the result.
func TestValidateOutputShapes_RejectsMissingAndMiscountedOutputs(t *testing.T) {
	t.Parallel()
	c := &Classifier{
		config:      ModelConfig{Type: BirdNETv30, NumOutputs: 2},
		outputNames: []string{"emb", "logits"},
	}

	err := c.validateOutputShapes([]*ort.Tensor{nil}, 1)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "returned 1 outputs, expected 2")

	err = c.validateOutputShapes([]*ort.Tensor{nil, nil}, 1)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing from the inference result")
}
