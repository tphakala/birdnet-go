package classifier

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/conf/conftest"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/observability/metrics"
)

// predTelemetryModel is an arbitrary model label used by the span-level tests.
const predTelemetryModel = "BirdNET_GLOBAL_6K_V2.4"

// predictionCount returns the current value of birdnet_predictions_total for the
// given model/status combination. WithLabelValues lazily creates the child at 0,
// so an untouched combination reports 0.0 rather than panicking.
func predictionCount(t *testing.T, m *metrics.BirdNETMetrics, model, status string) float64 {
	t.Helper()
	return testutil.ToFloat64(m.PredictionTotal.WithLabelValues(model, status))
}

// newPredictSpan starts a birdnet.predict span for the exactly-once tests.
func newPredictSpan(t *testing.T) *TracingSpan {
	t.Helper()
	span, _ := StartSpan(t.Context(), "birdnet.predict", "telemetry test")
	require.NotNil(t, span)
	return span
}

// TestTracingSpan_ErrorPathRecordsExactlyOnce mirrors BirdNET.Predict's
// invoke_failed / label_mismatch paths: the caller records the error explicitly
// and tags the span errored. Finish must NOT add a second (success) record.
func TestTracingSpan_ErrorPathRecordsExactlyOnce(t *testing.T) {
	resetGlobalMetrics(t)
	t.Cleanup(func() { resetGlobalMetrics(t) })
	m := newTestMetrics(t)
	SetMetrics(m)

	span := newPredictSpan(t)
	span.SetTag(tagKeyModel, predTelemetryModel)

	// The caller records the error outcome explicitly, then tags the span errored.
	predErr := errors.Newf("inference failed").Category(errors.CategoryAudio).Build()
	m.RecordPrediction(predTelemetryModel, 0.01, predErr)
	span.SetTag(tagKeyError, tagValueTrue)

	span.Finish()

	assert.InDelta(t, 1.0, predictionCount(t, m, predTelemetryModel, "error"), 0,
		"errored prediction must be recorded exactly once as an error")
	assert.InDelta(t, 0.0, predictionCount(t, m, predTelemetryModel, "success"), 0,
		"Finish must not record a spurious success on an errored span")
}

// TestTracingSpan_SuccessPathRecordsExactlyOnce verifies the happy path records
// exactly one success and that tagging error=false does not flip the errored flag.
func TestTracingSpan_SuccessPathRecordsExactlyOnce(t *testing.T) {
	resetGlobalMetrics(t)
	t.Cleanup(func() { resetGlobalMetrics(t) })
	m := newTestMetrics(t)
	SetMetrics(m)

	span := newPredictSpan(t)
	span.SetTag(tagKeyModel, predTelemetryModel)
	span.SetTag(tagKeyError, tagValueFalse) // success path tags error=false

	span.Finish()

	assert.InDelta(t, 1.0, predictionCount(t, m, predTelemetryModel, "success"), 0,
		"successful prediction must be recorded exactly once")
	assert.InDelta(t, 0.0, predictionCount(t, m, predTelemetryModel, "error"), 0,
		"error=false must not record an error outcome")
}

// TestTracingSpan_ErroredEarlyGuardRecordsNothing documents the contract for
// BirdNET.Predict's early-guard error paths (empty_sample, classifier_nil): they
// tag the span errored but do not record explicitly, so the prediction is not
// counted at all. This is intentional: the success record is suppressed and these
// pre-inference rejections do not inflate the success or error series.
func TestTracingSpan_ErroredEarlyGuardRecordsNothing(t *testing.T) {
	resetGlobalMetrics(t)
	t.Cleanup(func() { resetGlobalMetrics(t) })
	m := newTestMetrics(t)
	SetMetrics(m)

	span := newPredictSpan(t)
	span.SetTag(tagKeyModel, predTelemetryModel)
	span.SetTag(tagKeyError, tagValueTrue) // tagged errored, but no explicit record

	span.Finish()

	assert.InDelta(t, 0.0, predictionCount(t, m, predTelemetryModel, "success"), 0,
		"an errored span must not record a success")
	assert.InDelta(t, 0.0, predictionCount(t, m, predTelemetryModel, "error"), 0,
		"an early-guard error without an explicit record must not be counted")
}

// TestTracingSpan_FinishIsIdempotent verifies that calling Finish more than once
// (e.g. a manual call plus a deferred one) records the prediction only once and
// does not decrement the active-operations counter twice.
func TestTracingSpan_FinishIsIdempotent(t *testing.T) {
	resetGlobalMetrics(t)
	t.Cleanup(func() { resetGlobalMetrics(t) })
	m := newTestMetrics(t)
	SetMetrics(m)

	span := newPredictSpan(t)
	span.SetTag(tagKeyModel, predTelemetryModel)

	span.Finish()
	span.Finish() // second call must be a no-op

	assert.InDelta(t, 1.0, predictionCount(t, m, predTelemetryModel, "success"), 0,
		"a span finished twice must record exactly one success")
}

// predTelemetryClassifier is a fake inference.Classifier for the Perch tests.
type predTelemetryClassifier struct {
	logits []float32
	err    error
}

func (f *predTelemetryClassifier) Predict(_ []float32) ([]float32, error) {
	return f.logits, f.err
}
func (f *predTelemetryClassifier) NumSpecies() int { return len(f.logits) }
func (f *predTelemetryClassifier) Close()          {}

// TestPerchPredict_SuccessRecordsExactlyOnce verifies a happy-path Perch.Predict
// records exactly one success under the RegistryIDPerchV2 model label, making
// Perch visible in birdnet_predictions_total separately from BirdNET.
func TestPerchPredict_SuccessRecordsExactlyOnce(t *testing.T) {
	resetGlobalMetrics(t)
	t.Cleanup(func() { resetGlobalMetrics(t) })
	m := newTestMetrics(t)
	SetMetrics(m)

	labels := []string{"Species A", "Species B"}
	p := &Perch{classifier: &predTelemetryClassifier{logits: []float32{0.2, 0.8}}, labels: labels}

	results, err := p.Predict(t.Context(), [][]float32{{0.1, 0.2, 0.3}})
	require.NoError(t, err)
	require.NotEmpty(t, results)

	assert.InDelta(t, 1.0, predictionCount(t, m, RegistryIDPerchV2, "success"), 0,
		"a successful Perch prediction must be recorded exactly once")
	assert.InDelta(t, 0.0, predictionCount(t, m, RegistryIDPerchV2, "error"), 0,
		"a successful Perch prediction must not record an error")
}

// TestPerchPredict_InferenceErrorRecordsExactlyOnce verifies an inference failure
// records exactly one error (and no success) under the Perch model label.
func TestPerchPredict_InferenceErrorRecordsExactlyOnce(t *testing.T) {
	resetGlobalMetrics(t)
	t.Cleanup(func() { resetGlobalMetrics(t) })
	m := newTestMetrics(t)
	SetMetrics(m)

	boom := errors.Newf("backend inference failed").Category(errors.CategoryAudio).Build()
	p := &Perch{classifier: &predTelemetryClassifier{err: boom}, labels: []string{"A", "B"}}

	_, err := p.Predict(t.Context(), [][]float32{{0.1, 0.2}})
	require.Error(t, err)

	assert.InDelta(t, 1.0, predictionCount(t, m, RegistryIDPerchV2, "error"), 0,
		"a failed Perch inference must be recorded exactly once as an error")
	assert.InDelta(t, 0.0, predictionCount(t, m, RegistryIDPerchV2, "success"), 0,
		"a failed Perch inference must not record a spurious success")
}

// TestPerchPredict_EmptySampleRecordsNothing verifies the empty-sample guard
// tags the span errored but records no prediction, mirroring BirdNET.Predict:
// pre-inference rejections are not counted in either series.
func TestPerchPredict_EmptySampleRecordsNothing(t *testing.T) {
	resetGlobalMetrics(t)
	t.Cleanup(func() { resetGlobalMetrics(t) })
	m := newTestMetrics(t)
	SetMetrics(m)

	p := &Perch{classifier: &predTelemetryClassifier{}, labels: []string{"A"}}

	_, err := p.Predict(t.Context(), nil)
	require.Error(t, err)

	assert.InDelta(t, 0.0, predictionCount(t, m, RegistryIDPerchV2, "error"), 0,
		"an empty-sample rejection must not be counted as a prediction error")
	assert.InDelta(t, 0.0, predictionCount(t, m, RegistryIDPerchV2, "success"), 0,
		"an empty-sample rejection must not record a success")
}

// TestPerchPredict_NilClassifierRecordsNothing verifies the nil-classifier guard
// (e.g. after Close()) tags the span errored but records no prediction, mirroring
// BirdNET.Predict. The Perch is built with a true nil classifier interface (zero
// value) so the p.classifier == nil guard fires.
func TestPerchPredict_NilClassifierRecordsNothing(t *testing.T) {
	resetGlobalMetrics(t)
	t.Cleanup(func() { resetGlobalMetrics(t) })
	m := newTestMetrics(t)
	SetMetrics(m)

	p := &Perch{labels: []string{"A"}} // classifier is a nil interface

	_, err := p.Predict(t.Context(), [][]float32{{0.1, 0.2}})
	require.Error(t, err)

	assert.InDelta(t, 0.0, predictionCount(t, m, RegistryIDPerchV2, "error"), 0,
		"a nil-classifier rejection must not be counted as a prediction error")
	assert.InDelta(t, 0.0, predictionCount(t, m, RegistryIDPerchV2, "success"), 0,
		"a nil-classifier rejection must not record a success")
}

// TestPerchPredict_LabelMismatchRecordsErrorOnce verifies a label/score count
// mismatch (a failure after inference begins) records exactly one error.
func TestPerchPredict_LabelMismatchRecordsErrorOnce(t *testing.T) {
	resetGlobalMetrics(t)
	t.Cleanup(func() { resetGlobalMetrics(t) })
	m := newTestMetrics(t)
	SetMetrics(m)

	// Two labels but only one logit: pairLabelsAndConfidence returns an error.
	p := &Perch{classifier: &predTelemetryClassifier{logits: []float32{0.5}}, labels: []string{"A", "B"}}

	_, err := p.Predict(t.Context(), [][]float32{{0.1, 0.2}})
	require.Error(t, err)

	assert.InDelta(t, 1.0, predictionCount(t, m, RegistryIDPerchV2, "error"), 0,
		"a label/score mismatch must be recorded exactly once as an error")
	assert.InDelta(t, 0.0, predictionCount(t, m, RegistryIDPerchV2, "success"), 0,
		"a label/score mismatch must not record a success")
}

// batEmbeddingExtractor is a fake inference.EmbeddingExtractor for the Bat tests.
type batEmbeddingExtractor struct {
	embeddings []float32
	err        error
}

func (f *batEmbeddingExtractor) Predict(_ []float32) ([]float32, error) { return nil, nil }
func (f *batEmbeddingExtractor) NumSpecies() int                        { return 0 }
func (f *batEmbeddingExtractor) Close()                                 {}
func (f *batEmbeddingExtractor) PredictWithEmbeddings(_ []float32) (logits, embeddings []float32, err error) {
	return nil, f.embeddings, f.err
}

// batCustomClassifier is a fake inference.CustomClassifier for the Bat tests.
type batCustomClassifier struct {
	scores   []float32
	labels   []string
	inputDim int
	err      error
}

func (f *batCustomClassifier) PredictEmbedding(_ []float32) ([]float32, error) {
	return f.scores, f.err
}
func (f *batCustomClassifier) NumClasses() int  { return len(f.labels) }
func (f *batCustomClassifier) InputDim() int    { return f.inputDim }
func (f *batCustomClassifier) Labels() []string { return f.labels }
func (f *batCustomClassifier) Close()           {}

// TestBatPredict_SuccessRecordsExactlyOnce verifies a happy-path Bat.Predict
// records exactly one success under the RegistryIDBat model label, so Bat is
// visible in birdnet_predictions_total alongside BirdNET and Perch.
func TestBatPredict_SuccessRecordsExactlyOnce(t *testing.T) {
	resetGlobalMetrics(t)
	t.Cleanup(func() { resetGlobalMetrics(t) })
	m := newTestMetrics(t)
	SetMetrics(m)

	// Bat.Predict reads conf.Setting().Bat.Threshold; publish default test settings
	// so the lookup returns a non-nil snapshot (threshold 0 disables filtering).
	conftest.SetTestSettings(conftest.GetTestSettings())
	t.Cleanup(func() { conftest.SetTestSettings(nil) })

	b := &Bat{
		embeddingExtractor: &batEmbeddingExtractor{embeddings: []float32{0.1, 0.2, 0.3}},
		batClassifier:      &batCustomClassifier{scores: []float32{0.6, 0.4}, labels: []string{"BatA", "BatB"}},
	}

	results, err := b.Predict(t.Context(), [][]float32{{0.1, 0.2}})
	require.NoError(t, err)
	require.NotEmpty(t, results)

	assert.InDelta(t, 1.0, predictionCount(t, m, RegistryIDBat, "success"), 0,
		"a successful Bat prediction must be recorded exactly once")
	assert.InDelta(t, 0.0, predictionCount(t, m, RegistryIDBat, "error"), 0,
		"a successful Bat prediction must not record an error")
}

// TestBatPredict_EmbeddingErrorRecordsExactlyOnce verifies an embedding-extraction
// failure records exactly one error under the Bat model label.
func TestBatPredict_EmbeddingErrorRecordsExactlyOnce(t *testing.T) {
	resetGlobalMetrics(t)
	t.Cleanup(func() { resetGlobalMetrics(t) })
	m := newTestMetrics(t)
	SetMetrics(m)

	boom := errors.Newf("embedding extraction failed").Category(errors.CategoryAudio).Build()
	b := &Bat{
		embeddingExtractor: &batEmbeddingExtractor{err: boom},
		batClassifier:      &batCustomClassifier{labels: []string{"BatA"}},
	}

	_, err := b.Predict(t.Context(), [][]float32{{0.1, 0.2}})
	require.Error(t, err)

	assert.InDelta(t, 1.0, predictionCount(t, m, RegistryIDBat, "error"), 0,
		"a failed Bat embedding extraction must be recorded exactly once as an error")
	assert.InDelta(t, 0.0, predictionCount(t, m, RegistryIDBat, "success"), 0,
		"a failed Bat embedding extraction must not record a spurious success")
}

// TestBatPredict_EmptySampleRecordsNothing verifies the empty-sample guard tags
// the span errored but records no prediction, mirroring BirdNET.Predict.
func TestBatPredict_EmptySampleRecordsNothing(t *testing.T) {
	resetGlobalMetrics(t)
	t.Cleanup(func() { resetGlobalMetrics(t) })
	m := newTestMetrics(t)
	SetMetrics(m)

	b := &Bat{} // extractors nil; empty sample returns before they are touched

	_, err := b.Predict(t.Context(), nil)
	require.Error(t, err)

	assert.InDelta(t, 0.0, predictionCount(t, m, RegistryIDBat, "error"), 0,
		"an empty-sample rejection must not be counted as a prediction error")
	assert.InDelta(t, 0.0, predictionCount(t, m, RegistryIDBat, "success"), 0,
		"an empty-sample rejection must not record a success")
}

// TestBatPredict_NilClassifierRecordsNothing verifies the nil-classifier guard
// (e.g. after Close()) tags the span errored but records no prediction, mirroring
// the Perch and BirdNET early-guard contract.
func TestBatPredict_NilClassifierRecordsNothing(t *testing.T) {
	resetGlobalMetrics(t)
	t.Cleanup(func() { resetGlobalMetrics(t) })
	m := newTestMetrics(t)
	SetMetrics(m)

	b := &Bat{} // embeddingExtractor and batClassifier are nil interfaces

	_, err := b.Predict(t.Context(), [][]float32{{0.1, 0.2}})
	require.Error(t, err)

	assert.InDelta(t, 0.0, predictionCount(t, m, RegistryIDBat, "error"), 0,
		"a nil-classifier rejection must not be counted as a prediction error")
	assert.InDelta(t, 0.0, predictionCount(t, m, RegistryIDBat, "success"), 0,
		"a nil-classifier rejection must not record a success")
}
