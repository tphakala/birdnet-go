//go:build !openvino

package inference

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	ov "github.com/tphakala/birdnet-go/internal/inference/openvino"
)

// These tests assert the no-tag stub behaviour and are scoped to the !openvino
// build. In the openvino build with a real libopenvino_c present, InitOpenVINO("")
// would actually load the process-global core and NewOpenVINOClassifier would
// reach the native model read, so these no-tag invariants do not hold there; the
// tagged build is exercised by the lib-gated functional tests instead.

// TestNewOpenVINOClassifier_UnavailableWithoutTag verifies that without the
// openvino build tag, construction fails with the sentinel so the classifier
// dispatch falls back to ORT.
func TestNewOpenVINOClassifier_UnavailableWithoutTag(t *testing.T) {
	t.Parallel()
	c, err := NewOpenVINOClassifier("/x.onnx", OpenVINOClassifierOptions{Labels: []string{"a"}})
	assert.Nil(t, c)
	assert.ErrorIs(t, err, ov.ErrOpenVINOUnavailable)
}

// TestNewOpenVINOEmbeddingExtractor_UnavailableWithoutTag verifies that without the
// openvino build tag, the embedding extractor construction fails with the sentinel so
// the bat pipeline falls back to its ORT embedding extractor.
func TestNewOpenVINOEmbeddingExtractor_UnavailableWithoutTag(t *testing.T) {
	t.Parallel()
	e, err := NewOpenVINOEmbeddingExtractor("/x.onnx", OpenVINOEmbeddingExtractorOptions{
		OutputIndex: 1,
		ExpectedDim: 1024,
	})
	assert.Nil(t, e)
	assert.ErrorIs(t, err, ov.ErrOpenVINOUnavailable)
}

// TestNewOpenVINOEmbeddingExtractor_RejectsInvalidExpectedDim verifies the guard that
// rejects a non-positive ExpectedDim before any native call. A missing expected
// dimension is a caller bug, not a runtime fallback, so it returns a validation error
// rather than the unavailable sentinel.
func TestNewOpenVINOEmbeddingExtractor_RejectsInvalidExpectedDim(t *testing.T) {
	t.Parallel()
	e, err := NewOpenVINOEmbeddingExtractor("/x.onnx", OpenVINOEmbeddingExtractorOptions{OutputIndex: 1})
	assert.Nil(t, e)
	require.Error(t, err)
	assert.NotErrorIs(t, err, ov.ErrOpenVINOUnavailable,
		"a bad ExpectedDim is a validation error, not the unavailable sentinel")
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
