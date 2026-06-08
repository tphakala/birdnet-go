// Package classifier embedding-extraction capability (substrate M1, issue #948).
package classifier

import (
	"context"
	"time"

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
// Telemetry mirrors BirdNET.Predict exactly: same span data keys, same
// RecordModelInvoke/RecordPrediction call sites. The success-path
// RecordPrediction fires via span.Finish() (tracing.go switch case
// "birdnet.predict_embeddings"); error-path RecordPrediction is explicit.
//
// Implements EmbeddingCapable.
func (bn *BirdNET) PredictWithEmbeddings(ctx context.Context, sample [][]float32) ([]datastore.Results, []float32, error) {
	span, _ := StartSpan(ctx, "birdnet.predict_embeddings", "Species prediction with embeddings")
	defer span.Finish()
	span.SetTag("model", bn.ModelInfo.ID)

	settings := bn.currentSettings()
	start := time.Now()
	span.SetData("sample_count", len(sample))
	if len(sample) > 0 {
		span.SetData("sample_size", len(sample[0]))
	}

	// Guard against empty sample slice
	if len(sample) == 0 || len(sample[0]) == 0 {
		err := errors.Newf("empty audio sample").
			Category(errors.CategoryValidation).
			ModelContext(settings.BirdNET.ModelPath, bn.ModelInfo.ID).
			Build()
		span.SetTag("error", "true")
		span.SetData("error_type", "empty_sample")
		return nil, nil, err
	}

	// Lock to prevent concurrent access to the classifier backend and shared buffers
	bn.mu.Lock()
	defer bn.mu.Unlock()

	// Guard against nil classifier (e.g., after Delete() is called concurrently)
	if bn.classifier == nil {
		err := errors.Newf("classifier backend is not initialized").
			Category(errors.CategoryModelInit).
			ModelContext(settings.BirdNET.ModelPath, bn.ModelInfo.ID).
			Build()
		span.SetTag("error", "true")
		span.SetData("error_type", "classifier_nil")
		return nil, nil, err
	}

	// Capability check: type-assert once here (under bn.mu) so both branches
	// share a single invokeStart/invokeDuration timing block below.
	ee, capable := bn.classifier.(inference.EmbeddingExtractor)
	capable = capable && ee.EmbeddingDim() > 0

	// Run inference via the appropriate branch. Both branches produce
	// (predictions, embedding, err); the incapable branch always yields nil embedding.
	var (
		predictions []float32
		embedding   []float32
		invokeErr   error
	)
	invokeStart := time.Now()
	if capable {
		// Model supports embedding extraction: run the combined forward pass.
		// ee.EmbeddingDim() was called above while bn.mu is held; that reads a plain
		// int field on the ONNX backend, not bn.EmbeddingDim() (which would deadlock).
		predictions, embedding, invokeErr = ee.PredictWithEmbeddings(sample[0])
	} else {
		// Model cannot extract embeddings: fall back to plain prediction.
		predictions, invokeErr = bn.classifier.Predict(sample[0])
		// embedding stays nil
	}
	invokeDuration := time.Since(invokeStart)

	if invokeErr != nil {
		err := errors.New(invokeErr).
			Category(errors.CategoryAudio).
			ModelContext(settings.BirdNET.ModelPath, bn.ModelInfo.ID).
			Context("sample_length", len(sample[0])).
			Timing("prediction-invoke", time.Since(start)).
			Build()

		span.SetTag("error", "true")
		span.SetData("error_type", "invoke_failed")

		if m := getMetrics(); m != nil {
			m.RecordPrediction(bn.ModelInfo.ID, time.Since(start).Seconds(), err)
		}

		return nil, nil, err
	}

	span.SetData("invoke_duration_ms", invokeDuration.Milliseconds())

	// Record model invoke timing separately
	if m := getMetrics(); m != nil {
		m.RecordModelInvoke(bn.ModelInfo.ID, invokeDuration.Seconds())
	}

	results, err := bn.finalizeResults(predictions, settings)
	if err != nil {
		err = errors.New(err).
			Category(errors.CategoryValidation).
			Context("label_count", len(settings.BirdNET.Labels)).
			Timing("prediction-total", time.Since(start)).
			Build()

		span.SetTag("error", "true")
		span.SetData("error_type", "label_mismatch")

		// Record error in metrics
		if m := getMetrics(); m != nil {
			m.RecordPrediction(bn.ModelInfo.ID, time.Since(start).Seconds(), err)
		}

		return nil, nil, err
	}

	// Log prediction timing for performance monitoring
	duration := time.Since(start)
	bn.Debug("Prediction with embeddings completed in %v with %d results", duration, len(results))

	// Record metrics
	span.SetData("total_duration_ms", duration.Milliseconds())
	span.SetData("result_count", len(results))
	span.SetTag("error", "false")

	// The span.Finish() will automatically record the prediction metrics via the
	// "birdnet.predict_embeddings" case in tracing.go Finish().

	return results, embedding, nil
}
