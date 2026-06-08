package classifier

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Compile-time check that Perch implements ModelInstance.
var _ ModelInstance = (*Perch)(nil)

// newEmbTestPerch builds a minimal *Perch backed by the given classifier and
// labels. The classifier parameter uses the structural interface that both the
// embedding-capable and plain test fakes satisfy.
func newEmbTestPerch(c interface {
	Predict([]float32) ([]float32, error)
	NumSpecies() int
	Close()
}, labels []string,
) *Perch {
	return &Perch{
		classifier: c,
		labels:     labels,
		info:       ModelInfo{ID: "perch-test"},
	}
}

func TestPerch_PredictWithEmbeddings_Capable(t *testing.T) {
	t.Parallel()
	f := &fakeEmbExtractor{logits: []float32{0.1, 0.9, 0.5}, emb: []float32{1, 2, 3}, dim: 3}
	p := newEmbTestPerch(f, []string{"a_A", "b_B", "c_C"})

	results, emb, err := p.PredictWithEmbeddings(t.Context(), [][]float32{{0.0}})
	require.NoError(t, err)
	require.Equal(t, []float32{1, 2, 3}, emb)
	require.Len(t, results, 3)
}

func TestPerch_PredictWithEmbeddings_Incapable(t *testing.T) {
	t.Parallel()
	f := &fakePlainClassifier{logits: []float32{0.1, 0.9, 0.5}}
	p := newEmbTestPerch(f, []string{"a_A", "b_B", "c_C"})

	results, emb, err := p.PredictWithEmbeddings(t.Context(), [][]float32{{0.0}})
	require.NoError(t, err)
	assert.Nil(t, emb)
	require.Len(t, results, 3)
}

func TestPerch_EmbeddingDim(t *testing.T) {
	t.Parallel()
	assert.Equal(t, 1536, newEmbTestPerch(&fakeEmbExtractor{logits: []float32{0}, dim: 1536}, []string{"a_A"}).EmbeddingDim())
	assert.Equal(t, 0, newEmbTestPerch(&fakePlainClassifier{logits: []float32{0}}, []string{"a_A"}).EmbeddingDim())
}
