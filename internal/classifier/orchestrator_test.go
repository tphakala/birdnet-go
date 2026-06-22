package classifier

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/conf/conftest"
	"github.com/tphakala/birdnet-go/internal/datastore"
)

// mockModelInstance implements ModelInstance for testing.
type mockModelInstance struct {
	id        string
	spec      ModelSpec
	labels    []string // optional; when nil a single default label is returned
	device    string   // optional; when empty RuntimeInfo reports "CPU"
	backend   string   // optional; reported verbatim by RuntimeInfo
	precision string   // optional; reported verbatim by RuntimeInfo
	predict   func(ctx context.Context, samples [][]float32) ([]datastore.Results, error)
}

func (m *mockModelInstance) Predict(ctx context.Context, samples [][]float32) ([]datastore.Results, error) {
	if m.predict != nil {
		return m.predict(ctx, samples)
	}
	return []datastore.Results{{Species: "Turdus merula", Confidence: 0.95}}, nil
}

func (m *mockModelInstance) Spec() ModelSpec { return m.spec }
func (m *mockModelInstance) ModelID() string { return m.id }
func (m *mockModelInstance) ModelName() string {
	return "mock-" + m.id
}
func (m *mockModelInstance) ModelVersion() string { return "1.0" }
func (m *mockModelInstance) NumSpecies() int      { return 1 }
func (m *mockModelInstance) Labels() []string {
	if m.labels != nil {
		return m.labels
	}
	return []string{"Turdus merula_Common Blackbird"}
}
func (m *mockModelInstance) Close() error { return nil }
func (m *mockModelInstance) RuntimeInfo() (device, backend, precision string) {
	device = m.device
	if device == "" {
		device = deviceCPU
	}
	return device, m.backend, m.precision
}

// newTestOrchestrator creates an Orchestrator with mock models for unit testing.
// It does not require real model files.
func newTestOrchestrator(t *testing.T, mocks ...*mockModelInstance) *Orchestrator {
	t.Helper()
	models := make(map[string]*modelEntry, len(mocks))
	for _, m := range mocks {
		models[m.id] = &modelEntry{instance: m}
	}
	return &Orchestrator{
		models:   models,
		modelRSS: make(map[string]int64),
	}
}

// TestPrimaryModelInfo covers the o.mu-guarded primary-identity accessors that
// callers outside the package use instead of reading o.ModelInfo directly.
func TestPrimaryModelInfo(t *testing.T) {
	t.Parallel()

	want := ModelInfo{ID: "BirdNET_V2.4", Name: "BirdNET v2.4", Spec: ModelSpec{SampleRate: 48000}}
	o := &Orchestrator{ModelInfo: want}
	assert.Equal(t, want, o.PrimaryModelInfo())
	assert.Equal(t, want.ID, o.PrimaryModelID())

	// Zero value when no primary is set.
	empty := &Orchestrator{}
	assert.Equal(t, ModelInfo{}, empty.PrimaryModelInfo())
	assert.Empty(t, empty.PrimaryModelID())
}

func TestNewOrchestrator_SyncsSharedState(t *testing.T) {
	t.Parallel()

	settings := conftest.GetTestSettings()
	o, err := NewOrchestrator(settings)
	if err != nil {
		t.Skipf("Skipping: model not available in test environment: %v", err)
	}
	t.Cleanup(func() { o.Delete() })

	// Verify shared state is synced from primary model
	assert.Equal(t, o.primary.ModelInfo, o.ModelInfo, "ModelInfo should be synced")
	assert.NotNil(t, o.TaxonomyMap, "TaxonomyMap should be populated")
	assert.NotNil(t, o.ScientificIndex, "ScientificIndex should be populated")
	assert.Equal(t, settings, o.Settings, "Settings should be the same pointer")
}

func TestOrchestrator_PrimaryIsModelInstance(t *testing.T) {
	t.Parallel()

	settings := conftest.GetTestSettings()
	o, err := NewOrchestrator(settings)
	if err != nil {
		t.Skipf("Skipping: model not available in test environment: %v", err)
	}
	t.Cleanup(func() { o.Delete() })

	// Verify primary model satisfies ModelInstance
	var mi ModelInstance = o.primary
	require.NotNil(t, mi)
	assert.NotEmpty(t, mi.ModelID())
	assert.NotEmpty(t, mi.ModelName())
	assert.NotEmpty(t, mi.ModelVersion())
	assert.Positive(t, mi.NumSpecies())
	assert.NotEmpty(t, mi.Labels())

	spec := mi.Spec()
	assert.Equal(t, 48000, spec.SampleRate)
	assert.Equal(t, 3*time.Second, spec.ClipLength)
}

func TestOrchestrator_ModelsMapPopulated(t *testing.T) {
	t.Parallel()

	settings := conftest.GetTestSettings()
	o, err := NewOrchestrator(settings)
	if err != nil {
		t.Skipf("Skipping: model not available in test environment: %v", err)
	}
	t.Cleanup(func() { o.Delete() })

	assert.Len(t, o.models, 1, "Should have exactly one model in Phase 3b")
	entry, exists := o.models[o.ModelInfo.ID]
	require.True(t, exists, "Primary model should be registered by ID")
	assert.Equal(t, o.primary, entry.instance)
}

func TestOrchestrator_PredictModel_Success(t *testing.T) {
	t.Parallel()

	expected := []datastore.Results{
		{Species: "Parus major", Confidence: 0.88},
	}
	mock := &mockModelInstance{
		id:   "test-model-1",
		spec: ModelSpec{SampleRate: 48000, ClipLength: 3 * time.Second},
		predict: func(_ context.Context, _ [][]float32) ([]datastore.Results, error) {
			return expected, nil
		},
	}

	o := newTestOrchestrator(t, mock)
	results, err := o.PredictModel(t.Context(), "test-model-1", [][]float32{{0.1, 0.2}})

	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "Parus major", results[0].Species)
	assert.InDelta(t, 0.88, float64(results[0].Confidence), 0.001)
}

func TestOrchestrator_PredictModel_UnknownModel(t *testing.T) {
	t.Parallel()

	o := newTestOrchestrator(t) // no models registered

	results, err := o.PredictModel(t.Context(), "nonexistent", [][]float32{{0.1}})

	require.Error(t, err)
	assert.Nil(t, results)
	assert.Contains(t, err.Error(), "unknown model")
	assert.Contains(t, err.Error(), "nonexistent")
}

func TestOrchestrator_PredictModel_SerializedInference(t *testing.T) {
	t.Parallel()

	// Track the order of inference events to verify serialization.
	var events []string
	var eventsMu sync.Mutex
	addEvent := func(name string) {
		eventsMu.Lock()
		events = append(events, name)
		eventsMu.Unlock()
	}

	slowStarted := make(chan struct{})
	slowMock := &mockModelInstance{
		id:   "slow-model",
		spec: ModelSpec{SampleRate: 48000, ClipLength: 3 * time.Second},
		predict: func(_ context.Context, _ [][]float32) ([]datastore.Results, error) {
			addEvent("slow-start")
			close(slowStarted)
			time.Sleep(100 * time.Millisecond)
			addEvent("slow-end")
			return []datastore.Results{{Species: "Slow Bird", Confidence: 0.5}}, nil
		},
	}
	fastMock := &mockModelInstance{
		id:   "fast-model",
		spec: ModelSpec{SampleRate: 48000, ClipLength: 3 * time.Second},
		predict: func(_ context.Context, _ [][]float32) ([]datastore.Results, error) {
			addEvent("fast-start")
			addEvent("fast-end")
			return []datastore.Results{{Species: "Fast Bird", Confidence: 0.9}}, nil
		},
	}

	o := newTestOrchestrator(t, slowMock, fastMock)

	var wg sync.WaitGroup
	sample := [][]float32{{0.1, 0.2}}
	ctx := t.Context()

	// Launch slow model first
	wg.Go(func() {
		_, _ = o.PredictModel(ctx, "slow-model", sample)
	})

	// Wait for slow model to actually start inference, then launch fast model.
	// The fast model should be blocked by inferenceMu until slow finishes.
	<-slowStarted
	wg.Go(func() {
		_, _ = o.PredictModel(ctx, "fast-model", sample)
	})

	wg.Wait()

	eventsMu.Lock()
	defer eventsMu.Unlock()

	require.Len(t, events, 4, "expected 4 inference events")
	assert.Equal(t, "slow-start", events[0])
	assert.Equal(t, "slow-end", events[1])
	assert.Equal(t, "fast-start", events[2])
	assert.Equal(t, "fast-end", events[3])
}

func TestOrchestrator_ModelInfos(t *testing.T) {
	t.Parallel()

	o := &Orchestrator{
		models: map[string]*modelEntry{
			"BirdNET_V2.4": {instance: &mockModelInstance{id: "BirdNET_V2.4"}},
			"Perch_V2":     {instance: &mockModelInstance{id: "Perch_V2"}},
		},
	}

	infos := o.ModelInfos()

	assert.Len(t, infos, 2)
	ids := make(map[string]bool)
	for _, info := range infos {
		ids[info.ID] = true
	}
	assert.True(t, ids["BirdNET_V2.4"])
	assert.True(t, ids["Perch_V2"])
}

func TestOrchestrator_LoadAdditionalModels_UnknownModelSkipped(t *testing.T) {
	t.Parallel()

	settings := &conf.Settings{}
	settings.Models.Enabled = []string{"birdnet", "unknown_model"}

	o := &Orchestrator{
		Settings: settings,
		models:   map[string]*modelEntry{},
	}

	err := o.loadAdditionalModels(map[string]int{})
	assert.NoError(t, err)
}

func TestOrchestrator_ModelSpecFor(t *testing.T) {
	t.Parallel()

	birdnet := &mockModelInstance{
		id:   "BirdNET_V2.4",
		spec: ModelSpec{SampleRate: 48000, ClipLength: 3 * time.Second},
	}
	perch := &mockModelInstance{
		id:   "Google_Perch_V2",
		spec: ModelSpec{SampleRate: 32000, ClipLength: 5 * time.Second},
	}

	o := newTestOrchestrator(t, birdnet, perch)

	t.Run("returns BirdNET spec", func(t *testing.T) {
		t.Parallel()
		spec, ok := o.ModelSpecFor("BirdNET_V2.4")
		require.True(t, ok)
		assert.Equal(t, 3*time.Second, spec.ClipLength)
		assert.Equal(t, 48000, spec.SampleRate)
	})

	t.Run("returns Perch spec", func(t *testing.T) {
		t.Parallel()
		spec, ok := o.ModelSpecFor("Google_Perch_V2")
		require.True(t, ok)
		assert.Equal(t, 5*time.Second, spec.ClipLength)
		assert.Equal(t, 32000, spec.SampleRate)
	})

	t.Run("unknown model returns false", func(t *testing.T) {
		t.Parallel()
		_, ok := o.ModelSpecFor("nonexistent")
		assert.False(t, ok)
	})

	t.Run("nil instance returns false", func(t *testing.T) {
		t.Parallel()
		o2 := &Orchestrator{
			models: map[string]*modelEntry{
				"closed": {instance: nil},
			},
		}
		_, ok := o2.ModelSpecFor("closed")
		assert.False(t, ok)
	})
}

func TestOrchestrator_ModelInfos_SkipsNilInstances(t *testing.T) {
	t.Parallel()

	o := &Orchestrator{
		models: map[string]*modelEntry{
			"BirdNET_V2.4": {instance: &mockModelInstance{id: "BirdNET_V2.4"}},
			"Perch_V2":     {instance: nil}, // closed/deleted
		},
	}

	infos := o.ModelInfos()

	assert.Len(t, infos, 1)
	assert.Equal(t, "BirdNET_V2.4", infos[0].ID)
}

func TestUnionLabels_DedupesAcrossModelsPreservingOrder(t *testing.T) {
	t.Parallel()

	primary := []string{"Turdus merula_Common Blackbird", "Parus major_Great Tit"}
	bat := []string{"Barbastella barbastellus", "Turdus merula_Common Blackbird"}

	got := unionLabels(primary, bat)

	assert.Equal(t, []string{
		"Turdus merula_Common Blackbird",
		"Parus major_Great Tit",
		"Barbastella barbastellus",
	}, got)
}

func TestUnionLabels_SkipsEmptyEntries(t *testing.T) {
	t.Parallel()

	got := unionLabels([]string{"", "Parus major_Great Tit", ""})
	assert.Equal(t, []string{"Parus major_Great Tit"}, got)
}

// TestModelInfos_LivePrimaryInfo verifies that ModelInfos returns the live
// o.ModelInfo for the primary model entry rather than the static registry
// template. This matters for the arm64 ONNX-only default, where o.ModelInfo
// carries Backend=ONNX and Quantization=INT8 while the registry template has
// Backend=TFLite and Quantization=FP32.
func TestModelInfos_LivePrimaryInfo(t *testing.T) {
	t.Parallel()

	primaryInfo := stockBirdNETV24ONNXVariant("/models/BirdNET_INT8_ARM.onnx", QuantizationINT8)

	o := &Orchestrator{
		ModelInfo: primaryInfo,
		models: map[string]*modelEntry{
			primaryInfo.ID: {instance: &mockModelInstance{id: primaryInfo.ID}},
		},
	}

	infos := o.ModelInfos()

	require.Len(t, infos, 1)
	got := infos[0]
	assert.Equal(t, BackendONNX, got.Backend, "live backend must be ONNX, not the TFLite template")
	assert.Equal(t, QuantizationINT8, got.Quantization, "live quantization must be INT8")
	assert.True(t, got.IsStock, "IsStock must carry through from live info")
	assert.Equal(t, "/models/BirdNET_INT8_ARM.onnx", got.CustomPath)

	// Confirm ToDetectionModelInfo keeps attribution correct: IsStock keeps Variant "default".
	det := got.ToDetectionModelInfo()
	assert.Equal(t, "default", det.Variant, "IsStock stock model must attribute as default")
}

// TestAllLabels_IncludesSecondaryModelLabels verifies that AllLabels returns the
// union of primary and secondary model labels, including scientific-only bat labels.
// This is the label source used by the reverse name-search maps, so a secondary
// model label must appear for localized search to find it.
// When o.primary is nil (as in unit tests that avoid real model files), AllLabels
// iterates o.models only; unionLabels deduplicates, so the result is still correct.
func TestAllLabels_IncludesSecondaryModelLabels(t *testing.T) {
	t.Parallel()

	bird := &mockModelInstance{
		id:     "BirdNET_V2.4",
		labels: []string{"Turdus merula_Common Blackbird", "Parus major_Great Tit"},
	}
	bat := &mockModelInstance{
		id:     "BattyBirdNET_V1.0",
		labels: []string{"Barbastella barbastellus", "Myotis daubentonii"},
	}

	// newTestOrchestrator builds o.models but leaves o.primary nil, which is fine:
	// AllLabels handles nil primary by iterating all entries in o.models.
	o := newTestOrchestrator(t, bird, bat)

	got := o.AllLabels()

	assert.Contains(t, got, "Turdus merula_Common Blackbird", "bird model label must be included")
	assert.Contains(t, got, "Parus major_Great Tit", "bird model label must be included")
	assert.Contains(t, got, "Barbastella barbastellus", "bat model scientific-only label must be included")
	assert.Contains(t, got, "Myotis daubentonii", "bat model scientific-only label must be included")
}

// TestOrchestrator_PredictModel_ErrorIncrementsInvokeErrors verifies that a
// failed model Predict call increments the readable inference error counter via
// GetInferenceCounters().PeekAll()[id].InvokeErrors.
func TestOrchestrator_PredictModel_ErrorIncrementsInvokeErrors(t *testing.T) {
	// Not parallel: asserts a delta on the package-global inference counters
	// (globalInferenceCounters). Keeping it serial avoids coupling the delta to
	// any other test that touches the shared counters.
	const modelID = "error-model"
	predictErr := errors.New("injected predict failure")

	mock := &mockModelInstance{
		id:   modelID,
		spec: ModelSpec{SampleRate: 48000, ClipLength: 3 * time.Second},
		predict: func(_ context.Context, _ [][]float32) ([]datastore.Results, error) {
			return nil, predictErr
		},
	}

	o := newTestOrchestrator(t, mock)

	// Confirm the counter starts at zero (entry may not exist yet).
	before := GetInferenceCounters().PeekAll()[modelID].InvokeErrors

	results, err := o.PredictModel(t.Context(), modelID, [][]float32{{0.1}})

	require.Error(t, err)
	assert.Nil(t, results)

	after := GetInferenceCounters().PeekAll()[modelID].InvokeErrors
	assert.Equal(t, before+1, after, "InvokeErrors must be incremented on predict failure")

	// The success counter must not have been touched.
	assert.Equal(t, int64(0), GetInferenceCounters().PeekAll()[modelID].InvokeCount,
		"InvokeCount must remain zero after a failed predict")
}

// testRegistryIDForLoadFailure is a synthetic registry ID used only in tests
// that exercise the LoadModel failure path. It is registered in ModelRegistry
// and modelLoaders inside TestOrchestrator_LoadModel_FailureIncrementsLoadFailures.
const testRegistryIDForLoadFailure = "__test_load_fail__"

// TestOrchestrator_LoadModel_FailureIncrementsLoadFailures verifies that a
// failed LoadModel call increments the per-model load-failure counter returned
// by LoadFailures().
func TestOrchestrator_LoadModel_FailureIncrementsLoadFailures(t *testing.T) {
	// Do NOT run in parallel: mutates package-level ModelRegistry and modelLoaders.

	loaderErr := fmt.Errorf("injected loader failure")

	// Register a synthetic model so LoadModel's registry check passes.
	ModelRegistry[testRegistryIDForLoadFailure] = ModelInfo{
		ID:      testRegistryIDForLoadFailure,
		IsStock: false,
	}
	modelLoaders[testRegistryIDForLoadFailure] = func(_ *Orchestrator, _ int) error {
		return loaderErr
	}
	t.Cleanup(func() {
		delete(ModelRegistry, testRegistryIDForLoadFailure)
		delete(modelLoaders, testRegistryIDForLoadFailure)
	})

	o := &Orchestrator{
		Settings: conftest.GetTestSettings(),
		models:   map[string]*modelEntry{},
		modelRSS: make(map[string]int64),
	}

	// The first failure must register the counter and set it to 1.
	err := o.LoadModel(testRegistryIDForLoadFailure)
	require.Error(t, err, "LoadModel should propagate the loader error")

	failures := o.LoadFailures()
	assert.Equal(t, int64(1), failures[testRegistryIDForLoadFailure],
		"LoadFailures must be 1 after one failed LoadModel call")

	// A second failure must increment to 2.
	err = o.LoadModel(testRegistryIDForLoadFailure)
	require.Error(t, err)

	failures = o.LoadFailures()
	assert.Equal(t, int64(2), failures[testRegistryIDForLoadFailure],
		"LoadFailures must be 2 after two failed LoadModel calls")
}
