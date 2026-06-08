// Package classifier embedding-extraction capability (substrate M1).
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

// extractRawWithEmbeddings runs the capability-gated forward pass on an inference
// backend. If the backend implements EmbeddingExtractor with a positive dim it
// returns raw logits plus the embedding; otherwise it returns logits with a nil
// embedding (not an error). The caller must hold the model's lock and applies its
// own post-processing (sigmoid/softmax -> labels -> top-K) to the returned logits.
// The embedding from PredictWithEmbeddings is already a fresh allocation at the
// onnx layer, so callers do not copy it again.
func extractRawWithEmbeddings(c inference.Classifier, sample []float32) (logits, embedding []float32, err error) {
	if ee, ok := c.(inference.EmbeddingExtractor); ok && ee.EmbeddingDim() > 0 {
		return ee.PredictWithEmbeddings(sample)
	}
	logits, err = c.Predict(sample)
	return logits, nil, err
}

// embeddingDimOf reports the embedding dimension of an inference backend, or 0 if
// the backend does not expose embeddings. Callers that hold a per-model lock use
// this for their EmbeddingDim accessor.
func embeddingDimOf(c inference.Classifier) int {
	if ee, ok := c.(inference.EmbeddingExtractor); ok {
		return ee.EmbeddingDim()
	}
	return 0
}

// EmbeddingDim returns the embedding vector length of the underlying classifier,
// or 0 when the active model cannot produce embeddings.
// The result is read under bn.mu to avoid a race with concurrent model reloads.
func (bn *BirdNET) EmbeddingDim() int {
	bn.mu.Lock()
	defer bn.mu.Unlock()
	return embeddingDimOf(bn.classifier)
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
		if m := getMetrics(); m != nil {
			m.RecordPrediction(bn.ModelInfo.ID, time.Since(start).Seconds(), err)
		}
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
		if m := getMetrics(); m != nil {
			m.RecordPrediction(bn.ModelInfo.ID, time.Since(start).Seconds(), err)
		}
		return nil, nil, err
	}

	// Run the capability-gated forward pass under bn.mu. extractRawWithEmbeddings
	// performs the EmbeddingExtractor type assertion plus the EmbeddingDim() > 0
	// gate inline (reading a plain int field on the ONNX backend, not
	// bn.EmbeddingDim(), which would deadlock on bn.mu); incapable backends yield
	// a nil embedding.
	invokeStart := time.Now()
	predictions, embedding, invokeErr := extractRawWithEmbeddings(bn.classifier, sample[0])
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
			Context("confidence_count", len(predictions)).
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
