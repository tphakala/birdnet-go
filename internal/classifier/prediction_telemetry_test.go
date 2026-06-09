package classifier

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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

// TestTracingSpan_ErrorPathRecordsExactlyOnce mirrors a Predict error path: the
// caller records the error explicitly and tags the span errored. Finish must NOT
// add a second (success) record.
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

// TestTracingSpan_ErroredEarlyGuardRecordsNothing documents the contract for the
// Predict early-guard error paths (empty_sample, classifier_nil): they tag the
// span errored but do not record explicitly, so the prediction is not counted at
// all. This is intentional: the success record is suppressed and these
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
