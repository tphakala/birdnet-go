package classifier

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
)

// mockModelInstance implements ModelInstance for testing.
type mockModelInstance struct {
	id      string
	spec    ModelSpec
	predict func(ctx context.Context, samples [][]float32) ([]datastore.Results, error)
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
func (m *mockModelInstance) Labels() []string     { return []string{"Turdus merula_Common Blackbird"} }
func (m *mockModelInstance) Close() error         { return nil }

// newTestOrchestrator creates an Orchestrator with mock models for unit testing.
// It does not require real model files.
func newTestOrchestrator(t *testing.T, mocks ...*mockModelInstance) *Orchestrator {
	t.Helper()
	models := make(map[string]*modelEntry, len(mocks))
	for _, m := range mocks {
		models[m.id] = &modelEntry{instance: m}
	}
	return &Orchestrator{
		models: models,
	}
}

func TestNewOrchestrator_SyncsSharedState(t *testing.T) {
	t.Parallel()

	settings := conf.GetTestSettings()
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

	settings := conf.GetTestSettings()
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

	settings := conf.GetTestSettings()
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

func TestOrchestrator_PredictModel_NoCrossModelBlocking(t *testing.T) {
	t.Parallel()

	slowMock := &mockModelInstance{
		id:   "slow-model",
		spec: ModelSpec{SampleRate: 48000, ClipLength: 3 * time.Second},
		predict: func(_ context.Context, _ [][]float32) ([]datastore.Results, error) {
			time.Sleep(100 * time.Millisecond)
			return []datastore.Results{{Species: "Slow Bird", Confidence: 0.5}}, nil
		},
	}
	fastMock := &mockModelInstance{
		id:   "fast-model",
		spec: ModelSpec{SampleRate: 48000, ClipLength: 3 * time.Second},
		predict: func(_ context.Context, _ [][]float32) ([]datastore.Results, error) {
			return []datastore.Results{{Species: "Fast Bird", Confidence: 0.9}}, nil
		},
	}

	o := newTestOrchestrator(t, slowMock, fastMock)

	var wg sync.WaitGroup
	fastDone := make(chan time.Time, 1)
	slowDone := make(chan time.Time, 1)

	sample := [][]float32{{0.1, 0.2}}
	ctx := t.Context()

	// Launch slow model first
	wg.Go(func() {
		_, _ = o.PredictModel(ctx, "slow-model", sample)
		slowDone <- time.Now()
	})

	// Launch fast model immediately after
	wg.Go(func() {
		_, _ = o.PredictModel(ctx, "fast-model", sample)
		fastDone <- time.Now()
	})

	wg.Wait()

	fastFinish := <-fastDone
	slowFinish := <-slowDone

	// Fast model should finish well before slow model (at least 50ms earlier).
	// If per-model locking is broken and they share a lock, fast would be blocked
	// until slow finishes.
	assert.True(t, fastFinish.Before(slowFinish),
		"fast model should finish before slow model; fast=%v slow=%v",
		fastFinish, slowFinish)
}
