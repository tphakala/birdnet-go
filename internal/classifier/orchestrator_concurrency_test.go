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
// after Delete() clears o.primary, every primary-delegating accessor must return
// its zero value instead of dereferencing a nil o.primary and panicking. A
// minimal Orchestrator with no primary reproduces the post-Delete state exactly.
func TestOrchestrator_AccessorsNilPrimary_NoPanic(t *testing.T) {
	t.Parallel()

	settings := conftest.GetTestSettings()
	o := &Orchestrator{Settings: settings}

	assert.Equal(t, 0, o.NumSpecies())
	assert.Nil(t, o.Labels())

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

	// BuildRangeFilter snapshots the primary and returns a typed error rather than
	// panicking when there is no primary.
	err = BuildRangeFilter(o)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no primary model")
}

// TestOrchestrator_AccessorsConcurrentWithPrimaryClear_NoRace is the regression
// guard for the accessor-vs-Delete() data race: Delete() sets o.primary = nil
// under o.mu.Lock(), while the accessors used to read o.primary with no lock.
// A writer toggles o.primary under o.mu.Lock() (mirroring Delete's write) while
// readers hammer the accessors; the snapshot-under-RLock fix must make this
// race-free and panic-free. Must be run with -race.
func TestOrchestrator_AccessorsConcurrentWithPrimaryClear_NoRace(t *testing.T) {
	t.Parallel()

	settings := conftest.GetTestSettings()
	settings.BirdNET.Labels = []string{"Turdus merula_Common Blackbird", "Parus major_Great Tit"}

	bn := &BirdNET{
		Settings:     settings,
		speciesCache: make(map[string]*speciesCacheEntry),
	}
	bn.ModelInfo = ModelInfo{ID: "BirdNET_V3", Name: "BirdNET v3.0"}

	o := &Orchestrator{
		Settings:  settings,
		ModelInfo: bn.ModelInfo,
		primary:   bn,
		models:    map[string]*modelEntry{"BirdNET_V3": {instance: bn}},
	}

	const readsPerGoroutine = 300
	const readerCount = 4

	var readerWg sync.WaitGroup
	var readersDone atomic.Bool
	start := make(chan struct{})

	// Writer: flip o.primary between bn and nil under o.mu.Lock(), the exact write
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
				o.primary = nil
			} else {
				o.primary = bn
			}
			cleared = !cleared
			o.mu.Unlock()
			runtime.Gosched() // yield so the spin does not starve readers on a busy CI core
		}
		// Leave the primary restored so any trailing read sees a valid instance.
		o.mu.Lock()
		o.primary = bn
		o.mu.Unlock()
	}()

	for range readerCount {
		readerWg.Go(func() {
			<-start
			for range readsPerGoroutine {
				_ = o.NumSpecies()
				_ = o.Labels()
			}
		})
	}

	close(start)
	readerWg.Wait()
	readersDone.Store(true)
	<-writerDone
}

// TestBirdNET_SetModelsDirConcurrentWithCoverage_NoRace is the regression guard
// for the bn.modelsDir data race: SetModelsDir wrote bn.modelsDir with no lock
// while PrimaryRangeFilterCoverage read it after releasing bn.mu. The write is
// now guarded and the read is snapshotted under bn.mu. Must be run with -race.
//
// Not parallel: PrimaryRangeFilterCoverage resolves its settings through
// conf.CurrentOrFallback, which prefers the global settings instance, so the
// test sets a global v3 range-filter config (the branch that reads modelsDir)
// and restores a clean default on cleanup.
func TestBirdNET_SetModelsDirConcurrentWithCoverage_NoRace(t *testing.T) {
	v3 := conftest.GetTestSettings()
	v3.BirdNET.RangeFilter.Model = "v3"
	v3.BirdNET.Labels = []string{"Turdus merula_Common Blackbird"}
	conftest.SetTestSettings(v3)
	t.Cleanup(func() { conftest.SetTestSettings(conftest.GetTestSettings()) })

	bn := &BirdNET{
		Settings:     v3,
		speciesCache: make(map[string]*speciesCacheEntry),
	}
	bn.ModelInfo = ModelInfo{ID: "BirdNET_V3", Name: "BirdNET v3.0"}

	const iterations = 300
	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() {
		<-start
		for i := range iterations {
			bn.SetModelsDir(fmt.Sprintf("/models/%d", i))
		}
	})
	wg.Go(func() {
		<-start
		for range iterations {
			_, _, _, _ = bn.PrimaryRangeFilterCoverage()
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
