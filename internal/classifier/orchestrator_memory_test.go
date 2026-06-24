package classifier

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/errors"
)

// fakeInstance is a minimal ModelInstance for warm-up/RSS tests.
type fakeInstance struct {
	id         string
	sampleRate int
	clip       time.Duration
	predictedN int
	predictErr error
}

func (f *fakeInstance) Predict(_ context.Context, samples [][]float32) ([]datastore.Results, error) {
	if len(samples) > 0 {
		f.predictedN = len(samples[0])
	}
	return nil, f.predictErr
}
func (f *fakeInstance) Spec() ModelSpec {
	return ModelSpec{SampleRate: f.sampleRate, ClipLength: f.clip}
}
func (f *fakeInstance) ModelID() string      { return f.id }
func (f *fakeInstance) ModelName() string    { return f.id }
func (f *fakeInstance) ModelVersion() string { return "test" }
func (f *fakeInstance) NumSpecies() int      { return 1 }
func (f *fakeInstance) Labels() []string     { return nil }
func (f *fakeInstance) Close() error         { return nil }
func (f *fakeInstance) RuntimeInfo() (device, backend, precision string) {
	return deviceCPU, BackendONNX, ""
}

func TestWarmupAndRecordRSS_RecordsNonNegativeDelta(t *testing.T) {
	t.Parallel()
	o := &Orchestrator{modelRSS: make(map[string]int64)}
	inst := &fakeInstance{id: "Test_Model", sampleRate: 48000, clip: 3 * time.Second}

	before := o.captureRSSBefore()
	if before == 0 {
		t.Skip("process RSS unavailable on this platform")
	}
	o.warmupAndRecordRSS(inst.ModelID(), before, inst)

	// Warm-up must size the dummy clip from the spec (48000 * 3s = 144000).
	require.Equal(t, 144000, inst.predictedN, "warm-up dummy size")

	perModel, baseline := o.ModelRSS()
	require.Contains(t, perModel, inst.ModelID(), "expected modelRSS entry for Test_Model")
	assert.GreaterOrEqual(t, perModel[inst.ModelID()], int64(0), "RSS delta must be clamped to >= 0")
	assert.GreaterOrEqual(t, baseline, int64(0), "runtime baseline must be >= 0")
}

func TestModelRSS_ReturnsCopy(t *testing.T) {
	t.Parallel()
	o := &Orchestrator{modelRSS: map[string]int64{"A": 10}}
	m, _ := o.ModelRSS()
	m["A"] = 999
	m2, _ := o.ModelRSS()
	assert.Equal(t, int64(10), m2["A"], "ModelRSS must return a copy; mutation of returned map must not affect the original")
}

// TestRunPendingWarmups_WarmsAndRecordsRSS verifies the deferred warm-up path:
// a model registered in o.models and queued via deferWarmup is warmed up (one
// sized inference) and its RSS delta recorded when runPendingWarmups drains the
// queue. This is the post-publish, lock-free replacement for the old inline
// warm-up that ran under o.mu.
func TestRunPendingWarmups_WarmsAndRecordsRSS(t *testing.T) {
	t.Parallel()
	o := &Orchestrator{models: map[string]*modelEntry{}, modelRSS: make(map[string]int64)}
	inst := &fakeInstance{id: "Test_Model", sampleRate: 48000, clip: 3 * time.Second}

	before := o.captureRSSBefore()
	if before == 0 {
		t.Skip("process RSS unavailable on this platform")
	}
	// Register the entry first (mirrors the loader: build -> register -> defer).
	o.models[inst.ModelID()] = &modelEntry{instance: inst}
	o.deferWarmup(inst.ModelID(), before)
	o.runPendingWarmups()

	require.Equal(t, 144000, inst.predictedN, "warm-up dummy size (48000 * 3s)")
	perModel, _ := o.ModelRSS()
	require.Contains(t, perModel, inst.ModelID(), "expected modelRSS entry after deferred warm-up")
	assert.GreaterOrEqual(t, perModel[inst.ModelID()], int64(0), "RSS delta must be clamped to >= 0")
	assert.Empty(t, o.pendingWarmups, "queue must be drained")
}

// TestRunPendingWarmups_SkipsRemovedEntry verifies that draining a warm-up whose
// model was unloaded (absent from o.models) before the drain is a no-op: no
// panic, no stale modelRSS entry. Covers the teardown race where UnloadModel
// removes the entry between registration and the deferred warm-up.
func TestRunPendingWarmups_SkipsRemovedEntry(t *testing.T) {
	t.Parallel()
	o := &Orchestrator{models: map[string]*modelEntry{}, modelRSS: make(map[string]int64)}
	o.deferWarmup("Removed_Model", 123)

	require.NotPanics(t, func() { o.runPendingWarmups() })
	perModel, _ := o.ModelRSS()
	assert.NotContains(t, perModel, "Removed_Model", "no RSS recorded for an absent entry")
}

// TestRunPendingWarmups_SkipsNilInstance verifies that an entry whose instance
// was already torn down (instance == nil) records nothing. Covers the teardown
// race where UnloadModel nils the instance before the deferred warm-up runs.
func TestRunPendingWarmups_SkipsNilInstance(t *testing.T) {
	t.Parallel()
	o := &Orchestrator{
		models:   map[string]*modelEntry{"Orphan_Model": {instance: nil}},
		modelRSS: make(map[string]int64),
	}
	o.deferWarmup("Orphan_Model", 123)

	require.NotPanics(t, func() { o.runPendingWarmups() })
	perModel, _ := o.ModelRSS()
	assert.NotContains(t, perModel, "Orphan_Model", "no RSS recorded for a torn-down entry")
}

// TestRunPendingWarmups_DrainsAllQueuedModels verifies the drain loop warms up
// EVERY queued model, not just the first. A regression that drained only one
// entry, or mis-snapshotted the queue, would be caught here.
func TestRunPendingWarmups_DrainsAllQueuedModels(t *testing.T) {
	t.Parallel()
	o := &Orchestrator{models: map[string]*modelEntry{}, modelRSS: make(map[string]int64)}
	before := o.captureRSSBefore()
	if before == 0 {
		t.Skip("process RSS unavailable on this platform")
	}
	a := &fakeInstance{id: "Model_A", sampleRate: 48000, clip: 3 * time.Second}
	b := &fakeInstance{id: "Model_B", sampleRate: 48000, clip: 3 * time.Second}
	o.models[a.ModelID()] = &modelEntry{instance: a}
	o.models[b.ModelID()] = &modelEntry{instance: b}
	o.deferWarmup(a.ModelID(), before)
	o.deferWarmup(b.ModelID(), before)
	o.runPendingWarmups()

	assert.Equal(t, 144000, a.predictedN, "Model_A warm-up must run")
	assert.Equal(t, 144000, b.predictedN, "Model_B warm-up must run")
	perModel, _ := o.ModelRSS()
	assert.Contains(t, perModel, a.ModelID(), "Model_A RSS must be recorded")
	assert.Contains(t, perModel, b.ModelID(), "Model_B RSS must be recorded")
	assert.Empty(t, o.pendingWarmups, "queue must be fully drained")
}

// TestRunPendingWarmups_WarmupFailureIsNonFatal verifies that a warm-up whose
// inference returns an error is non-fatal: it still records the RSS delta (the
// allocation already happened) and does not panic or abort the drain.
func TestRunPendingWarmups_WarmupFailureIsNonFatal(t *testing.T) {
	t.Parallel()
	o := &Orchestrator{models: map[string]*modelEntry{}, modelRSS: make(map[string]int64)}
	before := o.captureRSSBefore()
	if before == 0 {
		t.Skip("process RSS unavailable on this platform")
	}
	inst := &fakeInstance{
		id:         "Failing_Model",
		sampleRate: 48000,
		clip:       3 * time.Second,
		predictErr: errors.Newf("simulated warm-up failure").Build(),
	}
	o.models[inst.ModelID()] = &modelEntry{instance: inst}
	o.deferWarmup(inst.ModelID(), before)

	require.NotPanics(t, func() { o.runPendingWarmups() })
	assert.Equal(t, 144000, inst.predictedN, "warm-up Predict is still invoked despite the error")
	perModel, _ := o.ModelRSS()
	assert.Contains(t, perModel, inst.ModelID(), "RSS is still recorded after a non-fatal warm-up failure")
}

// TestLoadModel_RunsDeferredWarmup is the wiring guard: it proves LoadModel
// actually drains the deferred warm-up after releasing o.mu. A fake loader is
// registered for a synthetic model so the test does not need real model files.
// Guards against a regression that removes LoadModel's runPendingWarmups drain.
func TestLoadModel_RunsDeferredWarmup(t *testing.T) {
	// Mutates the package-global ModelRegistry/modelLoaders, so not parallel.
	const testID = "GateTest_WarmupWiring"
	inst := &fakeInstance{id: testID, sampleRate: 48000, clip: 3 * time.Second}

	ModelRegistry[testID] = ModelInfo{ID: testID, Spec: ModelSpec{SampleRate: 48000, ClipLength: 3 * time.Second}}
	t.Cleanup(func() { delete(ModelRegistry, testID) })
	modelLoaders[testID] = func(o *Orchestrator, _ int) error {
		// Mirror the real loaders: capture before-build RSS, register the entry,
		// then defer the warm-up (all under o.mu, which LoadModel holds here).
		b := o.captureRSSBefore()
		o.models[testID] = &modelEntry{instance: inst}
		o.deferWarmup(testID, b)
		return nil
	}
	t.Cleanup(func() { delete(modelLoaders, testID) })

	o := &Orchestrator{models: map[string]*modelEntry{}, modelRSS: make(map[string]int64)}
	o.updateSettings(&conf.Settings{})

	require.NoError(t, o.LoadModel(testID))

	// The deferred warm-up must have run (proves LoadModel drained the queue).
	assert.Equal(t, 144000, inst.predictedN, "LoadModel must run the deferred warm-up")
	assert.Empty(t, o.pendingWarmups, "LoadModel must drain the warm-up queue")
}

// TestRunPendingWarmups_DoesNotHoldMapLockDuringWarmup is the core regression
// guard: while the deferred warm-up inference runs, callers that take
// o.mu.RLock (PredictModel, ModelInfos, PrimaryModelID) must not block. The
// warm-up serializes via inferenceMu instead, exactly like a normal inference.
func TestRunPendingWarmups_DoesNotHoldMapLockDuringWarmup(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	inst := &mockModelInstance{
		id:   "Blocking_Model",
		spec: ModelSpec{SampleRate: 48000, ClipLength: 3 * time.Second},
		predict: func(_ context.Context, _ [][]float32) ([]datastore.Results, error) {
			close(started)
			<-release
			return nil, nil
		},
	}
	o := &Orchestrator{
		models:   map[string]*modelEntry{inst.id: {instance: inst}},
		modelRSS: make(map[string]int64),
	}
	o.deferWarmup(inst.id, 1)

	done := make(chan struct{})
	go func() { defer close(done); o.runPendingWarmups() }()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("warm-up Predict did not start")
	}

	// The map lock must be free while the warm-up inference is in flight.
	gotLock := make(chan struct{})
	go func() {
		// Mirror a PredictModel-style map read: fetch an entry under o.mu.RLock.
		o.mu.RLock()
		_, ok := o.models[inst.id]
		o.mu.RUnlock()
		_ = ok
		close(gotLock)
	}()
	select {
	case <-gotLock:
	case <-time.After(2 * time.Second):
		t.Fatal("o.mu.RLock blocked during warm-up: warm-up must not hold the orchestrator map lock")
	}

	// The warm-up must hold inferenceMu (it serializes via the inference path).
	assert.False(t, o.inferenceMu.TryLock(), "warm-up must hold inferenceMu during Predict")

	close(release)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("runPendingWarmups did not finish")
	}
}
