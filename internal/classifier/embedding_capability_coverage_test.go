package classifier

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestEmbeddingCapableCoverage guards against a future ONNX-backed ModelInstance
// type being added without implementing EmbeddingCapable (the M1 regression where
// only *BirdNET was capable). When a new ONNX-backed classifier type is added,
// add it to onnxBackedTypes.
//
// TFLite-backed BirdNET and the Bat consumer are intentionally excluded:
// BirdNET-TFLite has no embedding output, and Bat is an embedding consumer, not a
// primary producer reached by the orchestrator embedding dispatch.
func TestEmbeddingCapableCoverage(t *testing.T) {
	t.Parallel()

	onnxBackedTypes := []struct {
		name     string
		instance any
	}{
		{"BirdNET", (*BirdNET)(nil)},
		{"Perch", (*Perch)(nil)},
	}

	for _, tc := range onnxBackedTypes {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, ok := tc.instance.(EmbeddingCapable)
			assert.True(t, ok, "%s must implement EmbeddingCapable so its ONNX backend can extract embeddings", tc.name)
		})
	}
}
