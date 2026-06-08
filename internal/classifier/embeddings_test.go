package classifier

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/errors"
)

// fakeEmbExtractor implements inference.EmbeddingExtractor (and thus inference.Classifier).
type fakeEmbExtractor struct {
	logits []float32
	emb    []float32
	dim    int
}

func (f *fakeEmbExtractor) Predict(_ []float32) ([]float32, error) { return f.logits, nil }
func (f *fakeEmbExtractor) NumSpecies() int                        { return len(f.logits) }
func (f *fakeEmbExtractor) Close()                                 {}
func (f *fakeEmbExtractor) PredictWithEmbeddings(_ []float32) (logits, embeddings []float32, err error) {
	return f.logits, f.emb, nil
}
func (f *fakeEmbExtractor) EmbeddingDim() int { return f.dim }

// fakeErrEmbExtractor implements inference.EmbeddingExtractor but returns an
// error from PredictWithEmbeddings, with a positive EmbeddingDim so it takes
// the capable branch of BirdNET.PredictWithEmbeddings.
type fakeErrEmbExtractor struct {
	logits []float32
	dim    int
	err    error
}

func (f *fakeErrEmbExtractor) Predict(_ []float32) ([]float32, error) { return f.logits, nil }
func (f *fakeErrEmbExtractor) NumSpecies() int                        { return len(f.logits) }
func (f *fakeErrEmbExtractor) Close()                                 {}
func (f *fakeErrEmbExtractor) PredictWithEmbeddings(_ []float32) (logits, embeddings []float32, err error) {
	return nil, nil, f.err
}
func (f *fakeErrEmbExtractor) EmbeddingDim() int { return f.dim }

// fakePlainClassifier implements only inference.Classifier (no embedding capability).
type fakePlainClassifier struct{ logits []float32 }

func (f *fakePlainClassifier) Predict(_ []float32) ([]float32, error) { return f.logits, nil }
func (f *fakePlainClassifier) NumSpecies() int                        { return len(f.logits) }
func (f *fakePlainClassifier) Close()                                 {}

// newEmbTestBirdNET builds a minimal *BirdNET backed by the given classifier,
// with pre-allocated buffers and settings matching the provided labels.
// The classifier parameter uses the structural interface that both fakes satisfy.
func newEmbTestBirdNET(c interface {
	Predict([]float32) ([]float32, error)
	NumSpecies() int
	Close()
}, labels []string,
) *BirdNET {
	n := len(labels)
	bn := &BirdNET{
		classifier:       c,
		confidenceBuffer: make([]float32, n),
		resultsBuffer:    make([]datastore.Results, n),
		ModelInfo:        ModelInfo{ID: "test-model"},
		speciesCache:     make(map[string]*speciesCacheEntry),
	}
	s := &conf.Settings{}
	s.BirdNET.Labels = labels
	s.BirdNET.Sensitivity = 1.0
	bn.settingsAtomic.Store(s)
	return bn
}

func TestBirdNET_PredictWithEmbeddings_Capable(t *testing.T) {
	t.Parallel()

	f := &fakeEmbExtractor{
		logits: []float32{0.1, 0.9, 0.5},
		emb:    []float32{1, 2, 3, 4},
		dim:    4,
	}
	bn := newEmbTestBirdNET(f, []string{"a_A", "b_B", "c_C"})

	results, emb, err := bn.PredictWithEmbeddings(t.Context(), [][]float32{{0.0}})
	require.NoError(t, err)
	require.Len(t, emb, 4)
	assert.Equal(t, []float32{1, 2, 3, 4}, emb)
	require.Len(t, results, 3)
}

func TestBirdNET_PredictWithEmbeddings_Incapable(t *testing.T) {
	t.Parallel()

	f := &fakePlainClassifier{logits: []float32{0.1, 0.9, 0.5}}
	bn := newEmbTestBirdNET(f, []string{"a_A", "b_B", "c_C"})

	results, emb, err := bn.PredictWithEmbeddings(t.Context(), [][]float32{{0.0}})
	require.NoError(t, err)
	assert.Nil(t, emb)
	require.Len(t, results, 3)
}

func TestBirdNET_EmbeddingDim(t *testing.T) {
	t.Parallel()

	bn := newEmbTestBirdNET(&fakeEmbExtractor{logits: []float32{0}, dim: 1024}, []string{"a_A"})
	assert.Equal(t, 1024, bn.EmbeddingDim())

	bn2 := newEmbTestBirdNET(&fakePlainClassifier{logits: []float32{0}}, []string{"a_A"})
	assert.Equal(t, 0, bn2.EmbeddingDim())
}

func TestBirdNET_PredictWithEmbeddings_ExtractorError(t *testing.T) {
	t.Parallel()

	sentinel := errors.Newf("extractor inference failed").
		Category(errors.CategoryAudio).
		Build()
	f := &fakeErrEmbExtractor{
		logits: []float32{0.1, 0.2, 0.3},
		dim:    4,
		err:    sentinel,
	}
	bn := newEmbTestBirdNET(f, []string{"a_A", "b_B", "c_C"})

	results, emb, err := bn.PredictWithEmbeddings(t.Context(), [][]float32{{0.0}})
	require.Error(t, err)
	assert.Nil(t, emb)
	assert.Nil(t, results)
}

func TestBirdNET_PredictWithEmbeddings_EmptySample(t *testing.T) {
	t.Parallel()

	f := &fakeEmbExtractor{logits: []float32{0.5}, emb: []float32{1.0}, dim: 1}
	bn := newEmbTestBirdNET(f, []string{"a_A"})

	_, _, err := bn.PredictWithEmbeddings(t.Context(), [][]float32{})
	require.Error(t, err)

	_, _, err = bn.PredictWithEmbeddings(t.Context(), [][]float32{{}})
	require.Error(t, err)
}
