// internal/health/registry_test.go
package health

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockCheck implements Check for testing.
type mockCheck struct {
	name     string
	category Category
	result   Result
	delay    time.Duration
}

func (m *mockCheck) Name() string       { return m.name }
func (m *mockCheck) Category() Category { return m.category }
func (m *mockCheck) Run(ctx context.Context) Result {
	if m.delay > 0 {
		select {
		case <-time.After(m.delay):
		case <-ctx.Done():
			return Result{
				Name:     m.name,
				Category: m.category,
				Status:   StatusUnknown,
				Message:  "timed out",
			}
		}
	}
	return m.result
}

func TestRegistry_RegisterAndCount(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	assert.Equal(t, 0, r.Count())

	r.Register(&mockCheck{name: "check-a", category: CategorySystem})
	assert.Equal(t, 1, r.Count())

	r.Register(&mockCheck{name: "check-b", category: CategoryAudio})
	assert.Equal(t, 2, r.Count())
}

func TestRegistry_RegisterAll(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	r.RegisterAll(
		&mockCheck{name: "check-a", category: CategorySystem},
		&mockCheck{name: "check-b", category: CategoryAudio},
		&mockCheck{name: "check-c", category: CategoryDatabase},
	)
	assert.Equal(t, 3, r.Count())
}

func TestRegistry_RunAll(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	r.RegisterAll(
		&mockCheck{
			name:     "sys",
			category: CategorySystem,
			result:   Result{Name: "sys", Category: CategorySystem, Status: StatusHealthy},
		},
		&mockCheck{
			name:     "audio",
			category: CategoryAudio,
			result:   Result{Name: "audio", Category: CategoryAudio, Status: StatusWarning},
		},
	)

	ctx := t.Context()
	results := r.RunAll(ctx)

	require.Len(t, results, 2)

	// Build a map for order-independent assertion (results run in parallel).
	byName := make(map[string]Result, len(results))
	for _, res := range results {
		byName[res.Name] = res
	}

	assert.Equal(t, StatusHealthy, byName["sys"].Status)
	assert.Equal(t, StatusWarning, byName["audio"].Status)
	// DurationMS should be set by RunAll.
	assert.GreaterOrEqual(t, byName["sys"].DurationMS, 0.0)
	// Timestamp should be populated.
	assert.False(t, byName["sys"].Timestamp.IsZero())
}

func TestRegistry_RunAll_Empty(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	results := r.RunAll(t.Context())
	assert.Empty(t, results)
}

func TestRegistry_RunCategory(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	r.RegisterAll(
		&mockCheck{
			name:     "sys",
			category: CategorySystem,
			result:   Result{Name: "sys", Category: CategorySystem, Status: StatusHealthy},
		},
		&mockCheck{
			name:     "audio",
			category: CategoryAudio,
			result:   Result{Name: "audio", Category: CategoryAudio, Status: StatusWarning},
		},
		&mockCheck{
			name:     "sys2",
			category: CategorySystem,
			result:   Result{Name: "sys2", Category: CategorySystem, Status: StatusCritical},
		},
	)

	ctx := t.Context()
	results := r.RunCategory(ctx, CategorySystem)

	require.Len(t, results, 2)
	for _, res := range results {
		assert.Equal(t, CategorySystem, res.Category)
	}
}

func TestRegistry_RunCategory_NoMatch(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	r.Register(&mockCheck{name: "sys", category: CategorySystem, result: Result{Status: StatusHealthy}})

	results := r.RunCategory(t.Context(), CategoryNetwork)
	assert.Empty(t, results)
}

func TestRegistry_Timeout(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	// A check that takes longer than the context deadline.
	r.Register(&mockCheck{
		name:     "slow",
		category: CategorySystem,
		delay:    5 * time.Second,
		result:   Result{Name: "slow", Category: CategorySystem, Status: StatusHealthy},
	})

	// Cancel the context immediately so the check observes cancellation.
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()

	results := r.RunAll(ctx)
	require.Len(t, results, 1)
	// The check should have returned StatusUnknown on context cancellation.
	assert.Equal(t, StatusUnknown, results[0].Status)
	assert.Equal(t, "timed out", results[0].Message)
}

func TestRegistry_Categories(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	r.RegisterAll(
		&mockCheck{name: "a", category: CategorySystem},
		&mockCheck{name: "b", category: CategoryAudio},
		&mockCheck{name: "c", category: CategorySystem}, // duplicate category
		&mockCheck{name: "d", category: CategoryDatabase},
	)

	cats := r.Categories()
	// Expect exactly 3 distinct categories.
	require.Len(t, cats, 3)

	catSet := make(map[Category]bool, len(cats))
	for _, c := range cats {
		catSet[c] = true
	}
	assert.True(t, catSet[CategorySystem])
	assert.True(t, catSet[CategoryAudio])
	assert.True(t, catSet[CategoryDatabase])
}

func TestRegistry_Categories_Empty(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	assert.Empty(t, r.Categories())
}

// hangingCheck ignores its context and blocks until the channel is closed.
type hangingCheck struct {
	name     string
	category Category
	unblock  chan struct{}
}

func (h *hangingCheck) Name() string       { return h.name }
func (h *hangingCheck) Category() Category { return h.category }
func (h *hangingCheck) Run(_ context.Context) Result {
	<-h.unblock
	return Result{Name: h.name, Category: h.category, Status: StatusHealthy}
}

func TestRunChecks_OverallTimeout(t *testing.T) {
	t.Parallel()
	unblock := make(chan struct{})
	defer close(unblock)

	r := NewRegistry()
	r.RegisterAll(
		&mockCheck{
			name:     "fast",
			category: CategorySystem,
			result:   Result{Name: "fast", Category: CategorySystem, Status: StatusHealthy, Message: "ok"},
		},
		&hangingCheck{
			name:     "hung",
			category: CategoryAudio,
			unblock:  unblock,
		},
	)

	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()

	results := r.RunAll(ctx)
	require.Len(t, results, 2)

	byName := make(map[string]Result, len(results))
	for _, res := range results {
		byName[res.Name] = res
	}

	assert.Equal(t, StatusHealthy, byName["fast"].Status)
	assert.Equal(t, "ok", byName["fast"].Message)

	assert.Equal(t, StatusUnknown, byName["hung"].Status)
	assert.Equal(t, "check did not complete within deadline", byName["hung"].Message)
}

func TestRunChecks_OverallTimeout_MultiResultNilSlice(t *testing.T) {
	t.Parallel()
	unblock := make(chan struct{})
	defer close(unblock)

	r := NewRegistry()
	r.RegisterAll(
		&mockMultiCheck{
			name:     "empty_multi",
			category: CategoryAnalysis,
			results:  nil,
		},
		&hangingCheck{
			name:     "hung",
			category: CategoryAudio,
			unblock:  unblock,
		},
	)

	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()

	results := r.RunAll(ctx)

	// The empty multi-check completed (returned nil/empty), so no results from it.
	// The hung check should get a synthetic StatusUnknown.
	require.Len(t, results, 1)
	assert.Equal(t, "hung", results[0].Name)
	assert.Equal(t, StatusUnknown, results[0].Status)
}

func TestRunChecks_MultiResultDefensiveCopy(t *testing.T) {
	t.Parallel()

	shared := []Result{
		{Name: "model_a", Category: CategoryAnalysis, Status: StatusHealthy},
		{Name: "model_b", Category: CategoryAnalysis, Status: StatusWarning},
	}

	mc := &mockMultiCheck{
		name:     "shared",
		category: CategoryAnalysis,
		results:  shared,
	}

	r := NewRegistry()
	r.Register(mc)
	r.RunAll(t.Context())

	// The original slice should NOT have been mutated by the orchestrator.
	assert.Zero(t, shared[0].DurationMS, "original slice DurationMS should be untouched")
	assert.True(t, shared[0].Timestamp.IsZero(), "original slice Timestamp should be untouched")
	assert.Zero(t, shared[1].DurationMS, "original slice DurationMS should be untouched")
	assert.True(t, shared[1].Timestamp.IsZero(), "original slice Timestamp should be untouched")
}

// mockMultiCheck implements both Check and MultiResultCheck.
type mockMultiCheck struct {
	name     string
	category Category
	results  []Result
}

func (m *mockMultiCheck) Name() string       { return m.name }
func (m *mockMultiCheck) Category() Category { return m.category }
func (m *mockMultiCheck) Run(_ context.Context) Result {
	if len(m.results) == 0 {
		return Result{Name: m.name, Category: m.category, Status: StatusUnknown}
	}
	return m.results[0]
}
func (m *mockMultiCheck) RunMulti(_ context.Context) []Result { return m.results }

func TestRegistry_RunAll_MultiResultCheck(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	r.RegisterAll(
		&mockCheck{
			name:     "single",
			category: CategorySystem,
			result:   Result{Name: "single", Category: CategorySystem, Status: StatusHealthy},
		},
		&mockMultiCheck{
			name:     "multi",
			category: CategoryAnalysis,
			results: []Result{
				{Name: "model_a", Category: CategoryAnalysis, Status: StatusHealthy},
				{Name: "model_b", Category: CategoryAnalysis, Status: StatusWarning},
			},
		},
	)

	results := r.RunAll(t.Context())

	require.Len(t, results, 3)
	byName := make(map[string]Result, len(results))
	for _, res := range results {
		byName[res.Name] = res
	}
	assert.Equal(t, StatusHealthy, byName["single"].Status)
	assert.Equal(t, StatusHealthy, byName["model_a"].Status)
	assert.Equal(t, StatusWarning, byName["model_b"].Status)
}

func TestRegistry_RunAll_MultiResultCheck_Empty(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	r.Register(&mockMultiCheck{
		name:     "empty_multi",
		category: CategoryAnalysis,
		results:  nil,
	})

	results := r.RunAll(t.Context())
	assert.Empty(t, results)
}
