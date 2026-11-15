package classifier

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf/conftest"
	"github.com/tphakala/birdnet-go/internal/errors"
)

func TestBat_PredictWithEmbedding_TopScoreIgnoresThreshold(t *testing.T) {
	// Not parallel: Bat.Predict reads the global settings for the threshold.
	const threshold = 0.5
	settings := conftest.GetTestSettings()
	settings.Bat.Threshold = threshold
	conftest.SetTestSettings(settings)
	t.Cleanup(func() { conftest.SetTestSettings(nil) })

	tests := []struct {
		name        string
		scores      []float32
		wantResults int
		wantTop     float32
	}{
		{"scores above the threshold", []float32{0.2, 0.9, 0.7}, 2, 0.9},
		{"every score below the threshold", []float32{0.2, 0.35, 0.1}, 0, 0.35},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vector := []float32{0.1, 0.2, 0.3}
			b := &Bat{
				embeddingExtractor: &batEmbeddingExtractor{embeddings: vector},
				batClassifier:      &batCustomClassifier{labels: []string{"a", "b", "c"}, scores: tt.scores},
				info:               ModelRegistry[RegistryIDBat],
				embeddingSpace:     "test-space",
			}
			samples := [][]float32{make([]float32, 8)}

			results, emb, err := b.PredictWithEmbedding(t.Context(), samples)
			require.NoError(t, err)
			assert.Len(t, results, tt.wantResults)
			assert.Equal(t, vector, emb.Vector)
			assert.Equal(t, "test-space", emb.Space)
			assert.InDelta(t, tt.wantTop, emb.TopScore, 1e-6, "the top score is taken before the threshold filter")

			plain, err := b.Predict(t.Context(), samples)
			require.NoError(t, err)
			assert.Equal(t, results, plain, "Predict returns the same results")
		})
	}
}

func TestBat_PredictWithEmbedding_FailureReturnsNoEmbedding(t *testing.T) {
	// Not parallel: Bat.Predict reads the global settings for the threshold.
	conftest.SetTestSettings(conftest.GetTestSettings())
	t.Cleanup(func() { conftest.SetTestSettings(nil) })

	tests := []struct {
		name      string
		extractor *batEmbeddingExtractor
		scores    []float32
	}{
		{"extractor fails", &batEmbeddingExtractor{err: errors.NewStd("extract failed")}, []float32{0.5}},
		{"no embedding produced", &batEmbeddingExtractor{}, []float32{0.5}},
		{"non-finite head score", &batEmbeddingExtractor{embeddings: []float32{1, 2, 3}}, []float32{float32(math.NaN())}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := &Bat{
				embeddingExtractor: tt.extractor,
				batClassifier:      &batCustomClassifier{labels: []string{"a"}, scores: tt.scores},
				info:               ModelRegistry[RegistryIDBat],
				embeddingSpace:     "test-space",
			}
			results, emb, err := b.PredictWithEmbedding(t.Context(), [][]float32{make([]float32, 8)})
			require.Error(t, err)
			assert.Nil(t, results)
			assert.Equal(t, WindowEmbedding{}, emb, "a failed window must not hand on a partial embedding")
		})
	}
}
