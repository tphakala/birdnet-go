// Package classifier embedding-extraction capability (substrate M1, issue #948).
package classifier

import (
	"context"

	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/inference"
)

// EmbeddingCapable is an optional capability a ModelInstance may implement to
// return the primary embedding vector alongside detection results, produced by
// the same forward pass. EmbeddingDim reports 0 when the active model cannot
// produce embeddings.
type EmbeddingCapable interface {
	// PredictWithEmbeddings runs inference and returns both detection results
	// and the model's embedding vector for the given audio window. The embedding
	// is nil when the underlying classifier cannot produce one; callers treat
	// nil as "unavailable".
	PredictWithEmbeddings(ctx context.Context, sample [][]float32) (results []datastore.Results, embedding []float32, err error)

	// EmbeddingDim returns the embedding vector length, or 0 when the active
	// model cannot produce embeddings.
	EmbeddingDim() int
}

// Verify that *BirdNET satisfies EmbeddingCapable at compile time.
var _ EmbeddingCapable = (*BirdNET)(nil)

// EmbeddingDim returns the embedding vector length of the underlying classifier,
// or 0 when the active model cannot produce embeddings.
// The result is read under bn.mu to avoid a race with concurrent model reloads.
func (bn *BirdNET) EmbeddingDim() int {
	bn.mu.Lock()
	defer bn.mu.Unlock()
	if ee, ok := bn.classifier.(inference.EmbeddingExtractor); ok {
		return ee.EmbeddingDim()
	}
	return 0
}

// PredictWithEmbeddings runs inference and returns detection results plus the
// model's embedding vector for the given audio window. The embedding is nil
// when the underlying classifier cannot produce one; callers treat nil as
// "unavailable".
//
// The method holds bn.mu for the full duration of the native inference call,
// matching the lock discipline of BirdNET.Predict (issue #3336 use-after-free
// contract: the native ONNX/TFLite buffers must not be freed by a concurrent
// model reload or Delete while inference is running).
//
// The capability check (inference.EmbeddingExtractor type assertion and
// ee.EmbeddingDim()) is performed on the ee interface value, never via
// bn.EmbeddingDim(), to avoid a self-deadlock on bn.mu.
//
// Metric recording is intentionally absent here; it is wired at the
// orchestrator chokepoint in a later task (Task 8).
//
// Implements EmbeddingCapable.
func (bn *BirdNET) PredictWithEmbeddings(ctx context.Context, sample [][]float32) ([]datastore.Results, []float32, error) {
	span, _ := StartSpan(ctx, "birdnet.predict_embeddings", "Species prediction with embeddings")
	defer span.Finish()

	settings := bn.currentSettings()

	if len(sample) == 0 || len(sample[0]) == 0 {
		return nil, nil, errors.Newf("empty audio sample").
			Category(errors.CategoryValidation).
			ModelContext(settings.BirdNET.ModelPath, bn.ModelInfo.ID).
			Build()
	}

	bn.mu.Lock()
	defer bn.mu.Unlock()

	if bn.classifier == nil {
		return nil, nil, errors.Newf("classifier backend is not initialized").
			Category(errors.CategoryModelInit).
			ModelContext(settings.BirdNET.ModelPath, bn.ModelInfo.ID).
			Build()
	}

	ee, capable := bn.classifier.(inference.EmbeddingExtractor)
	if !capable || ee.EmbeddingDim() == 0 {
		// Model cannot extract embeddings: fall back to plain prediction.
		predictions, err := bn.classifier.Predict(sample[0])
		if err != nil {
			return nil, nil, errors.New(err).
				Category(errors.CategoryAudio).
				ModelContext(settings.BirdNET.ModelPath, bn.ModelInfo.ID).
				Build()
		}
		results, err := bn.finalizeResults(predictions, settings)
		return results, nil, err
	}

	// Model supports embedding extraction: run the combined forward pass.
	// ee.EmbeddingDim() was called above while bn.mu is held; that reads a plain
	// int field on the ONNX backend, not bn.EmbeddingDim() (which would deadlock).
	predictions, embedding, err := ee.PredictWithEmbeddings(sample[0])
	if err != nil {
		return nil, nil, errors.New(err).
			Category(errors.CategoryAudio).
			ModelContext(settings.BirdNET.ModelPath, bn.ModelInfo.ID).
			Build()
	}

	results, err := bn.finalizeResults(predictions, settings)
	if err != nil {
		return nil, nil, err
	}
	return results, embedding, nil
}
