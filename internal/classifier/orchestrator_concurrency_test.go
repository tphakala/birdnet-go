package classifier

import (
	"fmt"
	"math"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf/conftest"
)

// TestOrchestrator_AccessorsNilPrimary_NoPanic verifies the teardown contract:
// when no v2.4 model is loaded (as after Delete() clears o.models), every
// anchor-gated accessor must return its zero value instead of panicking. A minimal
// Orchestrator with no models reproduces that state exactly.
func TestOrchestrator_AccessorsNilPrimary_NoPanic(t *testing.T) {
	t.Parallel()

	settings := conftest.GetTestSettings()
	o := &Orchestrator{Settings: settings}

	assert.Nil(t, o.AllLabels())
	assert.Nil(t, o.DefaultTargets())
	assert.Empty(t, o.ModelInfos())

	code, ok := o.GetSpeciesCode("Turdus merula_Common Blackbird")
	assert.Empty(t, code)
	assert.False(t, ok)

	scores, err := o.GetProbableSpecies(time.Now(), 0)
	require.NoError(t, err)
	assert.Nil(t, scores)

	scores2, err := o.GetProbableSpeciesWithSettings(time.Now(), 0, settings)
	require.NoError(t, err)
	assert.Nil(t, scores2)

	assert.Zero(t, o.GetSpeciesOccurrence("Turdus merula_Common Blackbird"))
	assert.Zero(t, o.GetSpeciesOccurrenceAtTime("Turdus merula_Common Blackbird", time.Now()))

	// Must not panic with a nil primary.
	assert.NotPanics(t, func() { o.RunFilterProcess(time.Now().Format(time.DateOnly), 0) })
	assert.NotPanics(t, func() { o.Debug("noop %d", 1) })

	// BuildRangeFilter returns a typed error rather than panicking when the range
	// filter service is absent (a bare orchestrator with no rangeFilter). Readiness is
	// keyed on the service, not on BirdNET v2.4, since the range filter is decoupled.
	err = BuildRangeFilter(o)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no range filter service")
}

// TestOrchestrator_AccessorsConcurrentWithPrimaryClear_NoRace is the regression
// guard for the accessor-vs-Delete() data race: Delete() clears o.models
// under o.mu.Lock(), while accessors read under o.mu.RLock().
// A writer toggles the v2.4 model under o.mu.Lock() (mirroring Delete's write) while
// readers hammer the accessors; the snapshot-under-RLock fix must make this
// race-free and panic-free. Must be run with -race.
func TestOrchestrator_AccessorsConcurrentWithPrimaryClear_NoRace(t *testing.T) {
	t.Parallel()

	settings := conftest.GetTestSettings()
	settings.BirdNET.Labels = []string{"Turdus merula_Common Blackbird", "Parus major_Great Tit"}

	bn := &BirdNET{
		Settings: settings,
	}
	bn.ModelInfo = ModelInfo{ID: RegistryIDBirdNETV24, Name: "BirdNET v2.4"}

	o := &Orchestrator{
		Settings: settings,
	}
	registerTestV24(o, bn)

	const readsPerGoroutine = 300
	const readerCount = 4

	var readerWg sync.WaitGroup
	var readersDone atomic.Bool
	start := make(chan struct{})

	// Writer: flip v2.4 entry between bn and nil under o.mu.Lock(), mirroring the write
	// Delete() performs. Spins until the readers finish so the write window always
	// overlaps the reads.
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		<-start
		cleared := false
		for !readersDone.Load() {
			o.mu.Lock()
			if cleared {
				delete(o.models, RegistryIDBirdNETV24)
			} else {
				registerTestV24(o, bn)
			}
			cleared = !cleared
			o.mu.Unlock()
			runtime.Gosched() // yield so the spin does not starve readers on a busy CI core
		}
		// Leave the model restored so any trailing read sees a valid instance.
		o.mu.Lock()
		registerTestV24(o, bn)
		o.mu.Unlock()
	}()

	for range readerCount {
		readerWg.Go(func() {
			<-start
			for range readsPerGoroutine {
				_ = o.DefaultTargets()
				_ = o.AllLabels()
			}
		})
	}

	close(start)
	readerWg.Wait()
	readersDone.Store(true)
	<-writerDone
}

// TestOrchestrator_SetModelsDirConcurrentWithCoverage_NoRace is the regression guard
// for the modelsDir data race: SetModelsDir writes o.modelsDir under o.mu.Lock()
// while RangeFilterStatus reads it under o.mu.RLock(). Must be run with -race.
//
// Not parallel: RangeFilterStatus resolves its settings through
// conf.CurrentOrFallback, which prefers the global settings instance, so the
// test sets a global v3 range-filter config (the branch that reads modelsDir)
// and restores a clean default on cleanup.
func TestOrchestrator_SetModelsDirConcurrentWithCoverage_NoRace(t *testing.T) {
	v3 := conftest.GetTestSettings()
	v3.BirdNET.RangeFilter.Model = "v3"
	v3.BirdNET.Labels = []string{"Turdus merula_Common Blackbird"}
	conftest.SetTestSettings(v3)
	// Restore the clean nil state, NOT GetTestSettings(): GetTestSettings() allocates
	// a fresh default *conf.Settings, so the old cleanup re-published a non-nil default
	// snapshot rather than clearing the global. A non-nil global snapshot makes
	// conf.CurrentOrFallback prefer it over the local settings a later -shuffle-ordered
	// test passes in (e.g. TestNewBirdNET_LocaleNormalization would then read the global
	// locale instead of its own). Clearing to nil lets each later test's own settings win.
	t.Cleanup(func() { conftest.SetTestSettings(nil) })

	bn := &BirdNET{
		Settings:  v3,
		ModelInfo: ModelInfo{ID: RegistryIDBirdNETV24, Name: "BirdNET v2.4"},
	}
	bn.settingsAtomic.Store(v3)

	o := &Orchestrator{
		Settings: v3,
	}
	registerTestV24(o, bn)
	o.settingsAtomic.Store(v3)

	const iterations = 300
	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() {
		<-start
		for i := range iterations {
			o.SetModelsDir(fmt.Sprintf("/models/%d", i))
		}
	})
	wg.Go(func() {
		<-start
		for range iterations {
			_ = o.RangeFilterStatus()
		}
	})
	close(start)
	wg.Wait()
}

// TestBatchRangeFilterInference_SizeValidation covers the input-size guards,
// including the integer-overflow-safe form: a near-math.MaxInt batchSize whose
// batchSize*inputWidth would overflow must be rejected by the divisor check
// before the multiplication is trusted, instead of slipping an oversized batch
// into the backend.
func TestBatchRangeFilterInference_SizeValidation(t *testing.T) {
	t.Parallel()

	o := &Orchestrator{}

	tests := []struct {
		name      string
		inputs    []float32
		batchSize int
		wantMsg   string
	}{
		{
			name:      "non-positive batchSize",
			inputs:    make([]float32, 3),
			batchSize: 0,
			wantMsg:   "must be positive",
		},
		{
			name:      "plain length mismatch",
			inputs:    make([]float32, 4),
			batchSize: 1,
			wantMsg:   "does not match batchSize",
		},
		{
			name:      "oversized batchSize",
			inputs:    make([]float32, 9),
			batchSize: math.MaxInt,
			wantMsg:   "does not match batchSize",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			out, err := o.BatchRangeFilterInference(tt.inputs, tt.batchSize)
			require.Error(t, err)
			assert.Nil(t, out)
			assert.Contains(t, err.Error(), tt.wantMsg)
		})
	}

	// The decisive overflow case: a batchSize whose batchSize*inputWidth wraps
	// (two's complement) to EXACTLY len(inputs). The old check
	// `len(inputs) != batchSize*inputWidth` computes 2 != 2 (false) and would
	// ACCEPT this oversized batch, passing it to the backend; only the divisor
	// guard rejects it. This is the regression this fix exists for: it fails on
	// the pre-fix code (which returns a different downstream error) and passes
	// only with the guard. 3 * 6148914691236517206 == 2^64 + 2, which wraps to 2
	// as int64. The construction only fits a 64-bit int; build it through uint64
	// so the literal never overflows a 32-bit int at compile time, and skip where
	// int is not 64-bit (overflow boundary differs there).
	t.Run("overflow wraps to len(inputs)", func(t *testing.T) {
		t.Parallel()
		if math.MaxInt != math.MaxInt64 {
			t.Skip("overflow construction requires a 64-bit int")
		}
		wrapBatch := int(uint64(6148914691236517206)) // 3*this == 2^64+2 -> wraps to 2
		out, err := o.BatchRangeFilterInference(make([]float32, 2), wrapBatch)
		require.Error(t, err)
		assert.Nil(t, out)
		assert.Contains(t, err.Error(), "does not match batchSize")
	})
}
