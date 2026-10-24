package classifier

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/notification"
)

// failingModel is a mock model whose Predict fails while fail is set, with the
// same non-finite error a real backend returns.
type failingModel struct {
	mock *mockModelInstance
	fail atomic.Bool
}

func newFailingModel(id string) *failingModel {
	f := &failingModel{}
	f.mock = &mockModelInstance{
		id:        id,
		spec:      ModelSpec{SampleRate: 48000, ClipLength: 3 * time.Second},
		backend:   "OpenVINO",
		device:    "CPU",
		precision: "f16",
	}
	f.mock.predict = func(_ context.Context, _ [][]float32) ([]datastore.Results, error) {
		if f.fail.Load() {
			return nil, newNonFiniteScoreError(nonFiniteScore{modelID: id, index: 0, count: 1}, f.mock.RuntimeInfo)
		}
		return []datastore.Results{{Species: "Turdus merula", Confidence: 0.95}}, nil
	}
	return f
}

// predictN runs n analysis windows through PredictModel.
func predictN(t *testing.T, o *Orchestrator, modelID string, n int) {
	t.Helper()
	for range n {
		_, _ = o.PredictModel(t.Context(), modelID, [][]float32{{0.1}})
	}
}

// failureNotices lists the inference-failure notices in svc.
func failureNotices(t *testing.T, svc *notification.Service) []*notification.Notification {
	t.Helper()
	all, err := svc.List(nil)
	require.NoError(t, err)
	var out []*notification.Notification
	for _, n := range all {
		if n.TitleKey == notification.MsgInferenceFailingTitle {
			out = append(out, n)
		}
	}
	return out
}

// TestInferenceFailureNotice_Lifecycle walks one model through the failure latch:
// no notice below the threshold, one notice at it (not repeated while failing),
// cleared on the first success, raised again after a new run of failures, and
// cleared when the model is unloaded. The observer fires on every change of the
// failing set and never otherwise.
func TestInferenceFailureNotice_Lifecycle(t *testing.T) {
	// Not parallel: uses the process-global notification service and health records.
	svc := setupTestNotification(t)
	const modelID = "failure-latch-model"
	dropInferenceHealth(modelID)
	t.Cleanup(func() { dropInferenceHealth(modelID) })

	m := newFailingModel(modelID)
	o := newTestOrchestrator(t, m.mock)
	var changes atomic.Int32
	o.SetInferenceHealthChangedCallback(func() { changes.Add(1) })

	settle := o.inferenceHealth.inFlight.Wait // drain the async reconciles

	m.fail.Store(true)
	predictN(t, o, modelID, InferenceFailureNoticeThreshold-1)
	settle()
	assert.Empty(t, failureNotices(t, svc), "no notice below the threshold")
	assert.Zero(t, changes.Load())

	predictN(t, o, modelID, 1)
	settle()
	require.Len(t, failureNotices(t, svc), 1, "reaching the threshold raises the notice")
	assert.Equal(t, int32(1), changes.Load())

	n := failureNotices(t, svc)[0]
	assert.Equal(t, notification.TypeError, n.Type)
	assert.Equal(t, notification.PriorityHigh, n.Priority)
	assert.Equal(t, notification.ComponentClassifier, n.Component)
	assert.Equal(t, notification.MsgInferenceFailingNonFiniteMessage, n.MessageKey)
	assert.Equal(t, modelID, n.Metadata["model_id"])
	assert.Equal(t, InferenceErrorClassNonFinite, n.Metadata["error_class"])
	assert.Contains(t, n.Message, "OpenVINO CPU (f16)")
	assert.Equal(t, "mock-"+modelID+" fails every analysis", n.Title, "the title uses the plain model name")

	predictN(t, o, modelID, inferenceFailureLogEvery)
	settle()
	assert.Len(t, failureNotices(t, svc), 1, "continued failures do not raise a second notice")
	assert.Equal(t, int32(1), changes.Load())

	m.fail.Store(false)
	predictN(t, o, modelID, 1)
	settle()
	assert.Empty(t, failureNotices(t, svc), "the first success clears the notice")
	assert.Equal(t, int32(2), changes.Load())

	m.fail.Store(true)
	predictN(t, o, modelID, InferenceFailureNoticeThreshold)
	settle()
	require.Len(t, failureNotices(t, svc), 1, "a new run of failures raises it again")
	assert.Equal(t, int32(3), changes.Load())

	require.NoError(t, o.UnloadModel(modelID))
	settle()
	assert.Empty(t, failureNotices(t, svc), "unloading the model clears its notice")
	assert.Equal(t, int32(4), changes.Load())
}

// TestInferenceFailureNotice_DeleteClearsNotices pins that Orchestrator.Delete
// clears the failure notices of the models it closes.
func TestInferenceFailureNotice_DeleteClearsNotices(t *testing.T) {
	// Not parallel: uses the process-global notification service and health records.
	svc := setupTestNotification(t)
	const modelID = "failure-delete-model"
	dropInferenceHealth(modelID)
	t.Cleanup(func() { dropInferenceHealth(modelID) })

	m := newFailingModel(modelID)
	o := newTestOrchestrator(t, m.mock)
	m.fail.Store(true)
	predictN(t, o, modelID, InferenceFailureNoticeThreshold)
	o.inferenceHealth.inFlight.Wait()
	require.Len(t, failureNotices(t, svc), 1)

	o.Delete()
	assert.Empty(t, failureNotices(t, svc))
}

// TestPredictModel_CancellationDoesNotCountAsFailure pins that a window whose
// context was cancelled (shutdown) neither advances nor ends the failure streak.
func TestPredictModel_CancellationDoesNotCountAsFailure(t *testing.T) {
	// Not parallel: uses the package-global health records.
	const modelID = "failure-cancel-model"
	dropInferenceHealth(modelID)
	t.Cleanup(func() { dropInferenceHealth(modelID) })

	mock := &mockModelInstance{
		id:   modelID,
		spec: ModelSpec{SampleRate: 48000, ClipLength: 3 * time.Second},
		predict: func(ctx context.Context, _ [][]float32) ([]datastore.Results, error) {
			return nil, ctx.Err()
		},
	}
	o := newTestOrchestrator(t, mock)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for range InferenceFailureNoticeThreshold + 1 {
		_, err := o.PredictModel(ctx, modelID, [][]float32{{0.1}})
		require.ErrorIs(t, err, context.Canceled)
	}
	health := o.InferenceHealth()
	require.Len(t, health, 1)
	assert.Zero(t, health[0].ConsecutiveFailures)
	assert.False(t, health[0].Failing)
}

// TestInferenceHealth_Telemetry pins the per-model telemetry fields.
func TestInferenceHealth_Telemetry(t *testing.T) {
	// Not parallel: uses the package-global health records.
	const modelID = "failure-telemetry-model"
	dropInferenceHealth(modelID)
	t.Cleanup(func() { dropInferenceHealth(modelID) })

	m := newFailingModel(modelID)
	o := newTestOrchestrator(t, m.mock)

	health := o.InferenceHealth()
	require.Len(t, health, 1)
	assert.Equal(t, modelID, health[0].ModelID)
	assert.Equal(t, "mock-"+modelID, health[0].Name, "the plain model name, without the backend suffix")
	assert.Zero(t, health[0].InferenceCount, "a loaded model that has not run is idle")
	assert.True(t, health[0].LastInferenceAt.IsZero())
	assert.True(t, health[0].LastSuccessAt.IsZero())

	before := time.Now()
	predictN(t, o, modelID, 2)
	m.fail.Store(true)
	predictN(t, o, modelID, 3)

	health = o.InferenceHealth()
	require.Len(t, health, 1)
	h := health[0]
	assert.Equal(t, int64(5), h.InferenceCount)
	assert.Equal(t, int64(3), h.ConsecutiveFailures)
	assert.False(t, h.Failing)
	assert.Equal(t, InferenceErrorClassNonFinite, h.ErrorClass)
	assert.False(t, h.LastSuccessAt.Before(before))
	assert.False(t, h.LastInferenceAt.Before(h.LastSuccessAt))
	assert.Equal(t, "OpenVINO", h.Backend)
	assert.Equal(t, "CPU", h.Device)
	assert.Equal(t, "f16", h.Precision)
}

func TestClassifyInferenceError(t *testing.T) {
	t.Parallel()
	nonFinite := newNonFiniteScoreError(nonFiniteScore{modelID: "m", index: 2, count: 6},
		func() (string, string, string) { return "CPU", "ONNX", "fp32" })
	assert.Equal(t, "m classifier returned a non-finite score (index 2 of 6)", nonFinite.Error(),
		"the message is unchanged by the sentinel")
	assert.Equal(t, errorClassNonFinite, classifyInferenceError(nonFinite))
	assert.Equal(t, errorClassNonFinite, classifyInferenceError(fmt.Errorf("wrapped: %w", nonFinite)))
	assert.Equal(t, errorClassOther, classifyInferenceError(errors.NewStd("backend exploded")))
}

func TestFailureStreakNeedsSync(t *testing.T) {
	t.Parallel()
	tests := []struct {
		streak int64
		want   bool
	}{
		{1, false},
		{InferenceFailureNoticeThreshold - 1, false},
		{InferenceFailureNoticeThreshold, true},
		{InferenceFailureNoticeThreshold + 1, false},
		{inferenceFailureLogEvery, true},
		{2 * inferenceFailureLogEvery, true},
		{2*inferenceFailureLogEvery + 1, false},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, failureStreakNeedsSync(tt.streak), "streak %d", tt.streak)
	}
}

func TestDescribeRuntime(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "OpenVINO CPU (f16)", describeRuntime("OpenVINO", "CPU", "f16"))
	assert.Equal(t, "ONNX Runtime", describeRuntime("ONNX Runtime", deviceUnknown, ""))
	assert.Equal(t, "an unknown backend", describeRuntime("", "", ""))
}

// TestInferenceFailureNotice_NoServiceLatchesNothing pins the nil-service path:
// with no notification service a failing model latches no notice (a later pass
// raises it), and an unloaded model's empty latch is dropped.
func TestInferenceFailureNotice_NoServiceLatchesNothing(t *testing.T) {
	// Not parallel: uses the process-global notification service and health records.
	notification.ResetForTest()
	t.Cleanup(notification.ResetForTest)
	const modelID = "failure-noservice-model"
	dropInferenceHealth(modelID)
	t.Cleanup(func() { dropInferenceHealth(modelID) })

	m := newFailingModel(modelID)
	o := newTestOrchestrator(t, m.mock)
	m.fail.Store(true)
	predictN(t, o, modelID, InferenceFailureNoticeThreshold)
	o.inferenceHealth.inFlight.Wait()

	o.inferenceHealth.mu.Lock()
	p := o.inferenceHealth.notices[modelID]
	o.inferenceHealth.mu.Unlock()
	require.NotNil(t, p, "the failing model has a latch")
	assert.Empty(t, p.ID(), "but no notice without a service")

	require.NoError(t, o.UnloadModel(modelID))
	o.inferenceHealth.inFlight.Wait()
	o.inferenceHealth.mu.Lock()
	_, kept := o.inferenceHealth.notices[modelID]
	o.inferenceHealth.mu.Unlock()
	assert.False(t, kept, "the unloaded model's empty latch is dropped")
}

// TestSetInferenceHealthChangedCallback pins the registration paths, including
// the ModelManager forwarder and a manager without an orchestrator.
func TestSetInferenceHealthChangedCallback(t *testing.T) {
	t.Parallel()
	o := newTestOrchestrator(t)
	mm := &ModelManager{orchestrator: o}
	mm.SetInferenceHealthChangedCallback(func() {})
	assert.NotNil(t, o.inferenceHealth.changed.Load(), "the manager forwards to its orchestrator")

	o.SetInferenceHealthChangedCallback(nil)
	assert.Nil(t, o.inferenceHealth.changed.Load(), "nil disables the callback")

	assert.NotPanics(t, func() { (&ModelManager{}).SetInferenceHealthChangedCallback(func() {}) })
}

func TestInferenceErrorClassString(t *testing.T) {
	t.Parallel()
	assert.Equal(t, InferenceErrorClassNonFinite, errorClassNonFinite.String())
	assert.Equal(t, InferenceErrorClassOther, errorClassOther.String())
	assert.Empty(t, errorClassNone.String())
}

// TestInferenceHealth_SortedByID pins the deterministic order of InferenceHealth.
func TestInferenceHealth_SortedByID(t *testing.T) {
	t.Parallel()
	o := newTestOrchestrator(t,
		&mockModelInstance{id: "zeta-model"},
		&mockModelInstance{id: "alpha-model"},
		&mockModelInstance{id: "mid-model"},
	)
	health := o.InferenceHealth()
	ids := make([]string, len(health))
	for i := range health {
		ids[i] = health[i].ModelID
	}
	assert.Equal(t, []string{"alpha-model", "mid-model", "zeta-model"}, ids)
}

// TestInferenceHealth_NamelessModelFallsBackToID pins that a model whose info
// carries no name is reported by its registry ID, so the notice never starts
// with an empty name.
func TestInferenceHealth_NamelessModelFallsBackToID(t *testing.T) {
	// Not parallel: mutates the package-global ModelRegistry.
	const modelID = "nameless-health-model"
	ModelRegistry[modelID] = ModelInfo{ID: modelID}
	t.Cleanup(func() { delete(ModelRegistry, modelID) })

	o := newTestOrchestrator(t, &mockModelInstance{id: modelID})
	health := o.InferenceHealth()
	require.Len(t, health, 1)
	assert.Equal(t, modelID, health[0].Name)
	assert.Equal(t, modelID, health[0].ModelName, "the display name falls back to the ID too")
}

// TestInferenceFailureNotice_ReloadClearsNotice pins that replacing a failing
// model's instance (a variant swap or settings reload) clears its notice: the new
// instance starts with a fresh record and is not failing.
func TestInferenceFailureNotice_ReloadClearsNotice(t *testing.T) {
	// Not parallel: uses the process-global notification service, settings and health records.
	setTestGlobalSettings(t)
	svc := setupTestNotification(t)
	dropInferenceHealth(RegistryIDBirdNETV24)
	t.Cleanup(func() { dropInferenceHealth(RegistryIDBirdNETV24) })

	m := newFailingModel(RegistryIDBirdNETV24)
	o := newTestOrchestrator(t, m.mock)
	m.fail.Store(true)
	predictN(t, o, RegistryIDBirdNETV24, InferenceFailureNoticeThreshold)
	o.inferenceHealth.inFlight.Wait()
	require.Len(t, failureNotices(t, svc), 1)

	fresh := newFailingModel(RegistryIDBirdNETV24)
	swapped, err := o.reloadEntry(RegistryIDBirdNETV24, func(_ *Orchestrator, _ *conf.Settings, _ int) (ModelInstance, error) {
		return fresh.mock, nil
	}, reloadOpts{})
	require.NoError(t, err)
	require.True(t, swapped)
	o.inferenceHealth.inFlight.Wait()

	assert.Empty(t, failureNotices(t, svc), "the replaced instance's notice is cleared")
	health := o.InferenceHealth()
	require.Len(t, health, 1)
	assert.Zero(t, health[0].ConsecutiveFailures, "the new instance starts with a fresh record")
}

// TestKickInferenceHealthSyncIfTracked_UntrackedStartsNothing pins the common
// case: unloading a healthy model with no notice latched queues no reconcile.
func TestKickInferenceHealthSyncIfTracked_UntrackedStartsNothing(t *testing.T) {
	// Not parallel: uses the package-global health records.
	const modelID = "untracked-unload-model"
	dropInferenceHealth(modelID)
	t.Cleanup(func() { dropInferenceHealth(modelID) })

	o := newTestOrchestrator(t, &mockModelInstance{id: modelID})
	predictN(t, o, modelID, 3)
	require.NoError(t, o.UnloadModel(modelID))

	// Wait out any pass, then check none ran: a reconcile pass always records
	// the failing set (an empty, non-nil map when nothing fails).
	o.inferenceHealth.inFlight.Wait()
	o.inferenceHealth.mu.Lock()
	defer o.inferenceHealth.mu.Unlock()
	assert.Nil(t, o.inferenceHealth.failing, "no reconcile ran for an untracked unload")
}
