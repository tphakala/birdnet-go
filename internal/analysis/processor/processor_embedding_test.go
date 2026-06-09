package processor

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// Detections must carry the window embedding and model id so they ride through
// the pending-detection merge to DatabaseAction.
func TestDetections_CarriesEmbeddingFields(t *testing.T) {
	t.Parallel()
	vec := []float32{1, 2, 3}
	d := Detections{
		CorrelationID: "abc",
		Embeddings:    vec,
		ModelID:       "birdnet",
	}
	assert.Equal(t, vec, d.Embeddings)
	assert.Equal(t, "birdnet", d.ModelID)
}
