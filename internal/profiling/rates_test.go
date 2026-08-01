package profiling

import (
	"bytes"
	"runtime"
	"runtime/pprof"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/conf"
)

// Any test here that applies a rate must NOT call t.Parallel(). The block and
// mutex profile rates are process-global runtime state, so two tests setting
// them concurrently would read each other's values. TestAggressiveRateThresholds
// is the exception and is parallel: it exercises the threshold predicates as
// pure functions and never touches the runtime.

// sentinelMutexFraction is a value no code under test can produce, so an
// assertion that the fraction is something else proves the setter actually ran
// rather than that it happened to already hold the expected value.
const sentinelMutexFraction = 999

// resetRatesAfterTest restores both rates to off once the test finishes, so a
// test that turns sampling on cannot leave the rest of the package's tests, or
// the test binary itself, paying for samples nobody reads.
func resetRatesAfterTest(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		runtime.SetBlockProfileRate(0)
		runtime.SetMutexProfileFraction(0)
	})
}

// profilingConfig builds a config section carrying only the two rates.
func profilingConfig(blockRate, mutexFraction int) *conf.ProfilingConfig {
	return &conf.ProfilingConfig{
		BlockRate:     blockRate,
		MutexFraction: mutexFraction,
	}
}

// currentMutexFraction reads the live mutex fraction without changing it.
//
// A negative argument to SetMutexProfileFraction means "report the current
// value and leave it alone", which is the only way to read either rate back
// from the runtime. There is no equivalent for the block rate, which is why the
// block-rate assertions in this file go through an actual profile instead.
func currentMutexFraction() int {
	return runtime.SetMutexProfileFraction(-1)
}

func TestApplyRates(t *testing.T) {
	tests := []struct {
		name              string
		blockRate         int
		mutexFraction     int
		wantMutexFraction int
	}{
		{
			name: "unset leaves both profilers off",
		},
		{
			name:              "recommended sampling rates",
			blockRate:         conf.RecommendedBlockProfileRate,
			mutexFraction:     conf.RecommendedMutexProfileFraction,
			wantMutexFraction: conf.RecommendedMutexProfileFraction,
		},
		{
			name:      "block only",
			blockRate: 50000,
		},
		{
			name:              "mutex only",
			mutexFraction:     250,
			wantMutexFraction: 250,
		},
		{
			name:          "negatives resolve to off rather than leaving the rate unchanged",
			blockRate:     -1,
			mutexFraction: -1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetRatesAfterTest(t)

			// Start from a value nothing under test produces, so a case
			// expecting 0 proves the fraction was actively cleared rather than
			// never set. Without this the zero-expecting cases pass with the
			// SetMutexProfileFraction call deleted, and an unclamped negative
			// would read the fraction and leave it at the sentinel.
			runtime.SetMutexProfileFraction(sentinelMutexFraction)

			ApplyRates(profilingConfig(tt.blockRate, tt.mutexFraction))

			assert.Equal(t, tt.wantMutexFraction, currentMutexFraction(),
				"the runtime's live mutex fraction after ApplyRates")
		})
	}
}

// TestApplyRatesIgnoresDebug is the regression guard for the change this package
// exists for. Both rates used to be set to 1 whenever debug: true was set, which
// is a flag people turn on for verbose logging while chasing something
// unrelated, so it charged the real-time audio path for recording every blocking
// and contention event on their behalf. Nothing may reintroduce that coupling.
func TestApplyRatesIgnoresDebug(t *testing.T) {
	resetRatesAfterTest(t)

	settings := &conf.Settings{}
	settings.Debug = true
	settings.WebServer.Debug = true
	settings.Realtime.Telemetry.Enabled = true
	settings.Diagnostics.Profiling.Enabled = true

	// Same sentinel discipline as TestApplyRates: without it this asserts 0
	// against a fraction that was already 0, and passes with the setter gone.
	runtime.SetMutexProfileFraction(sentinelMutexFraction)

	ApplyRates(&settings.Diagnostics.Profiling)

	assert.Zero(t, currentMutexFraction(),
		"debug: true must not switch on mutex profiling, and nothing but the rate settings may move the fraction")
}

// TestApplyRatesNilConfig pins that a nil section is a no-op rather than a
// reset: it must not silently disable a profiler the operator asked for.
func TestApplyRatesNilConfig(t *testing.T) {
	resetRatesAfterTest(t)

	runtime.SetMutexProfileFraction(sentinelMutexFraction)
	ApplyRates(nil)

	assert.Equal(t, sentinelMutexFraction, currentMutexFraction(),
		"a nil config must leave the runtime alone")
}

// TestDefaultRatesProduceUsableProfiles is the acceptance test for the sampling
// rates: coarser rates are only worth having if a real contention problem still
// shows up in the profile.
//
// It exercises genuine blocking and genuine lock contention rather than
// asserting the rates were stored, because "the value was applied" and "the
// profile is usable at that value" are different claims and only the second one
// matters to someone debugging a stall. It is also the only coverage that
// SetBlockProfileRate is called at all, since the runtime exposes no getter for
// it.
func TestDefaultRatesProduceUsableProfiles(t *testing.T) {
	resetRatesAfterTest(t)

	ApplyRates(profilingConfig(conf.RecommendedBlockProfileRate, conf.RecommendedMutexProfileFraction))
	require.Equal(t, conf.RecommendedMutexProfileFraction, currentMutexFraction())

	t.Run("block profile records a blocked channel receive", func(t *testing.T) {
		// Deterministic, so it gets exactly one round and no retries.
		// blocksampled records an event unconditionally once its blocked time
		// reaches the rate, and 2ms is 200x the 10us rate, so a single call
		// must produce a sample. Retrying here is what would let a rate orders
		// of magnitude too coarse pass by accumulating lucky rounds.
		before := countProfileEvents(t, "block", "blockOnChannel")
		blockOnChannel()
		after := countProfileEvents(t, "block", "blockOnChannel")

		assert.Greater(t, after, before,
			"one 2ms channel block must be sampled at rate %d; a single round failing means the rate is far too coarse or sampling was never applied",
			conf.RecommendedBlockProfileRate)
	})

	t.Run("mutex profile records real lock contention", func(t *testing.T) {
		// One round, no retries, for the same reason as the block case: a retry
		// loop lets a rate too coarse to be usable pass by accumulating lucky
		// rounds, which is exactly how the first version of this test accepted
		// a fraction 10,000x coarser than the recommended one.
		//
		// Genuinely statistical, unlike the block case. contendMutex produces a
		// few thousand contention events and 1-in-100 is sampled, so the chance
		// of a round recording nothing at the shipped fraction is vanishingly
		// small.
		//
		// Its discriminating power is limited and worth stating rather than
		// overselling: measured, a fraction 100x coarser fails this about half
		// the time, because a single sample is enough and one is often still
		// taken. What it does catch every time is sampling not being applied at
		// all, which is the regression that actually matters. See
		// countProfileEvents for why the magnitude of the number cannot carry a
		// finer claim.
		before := countProfileEvents(t, "mutex", "contendMutex")
		contendMutex()
		after := countProfileEvents(t, "mutex", "contendMutex")

		assert.Greater(t, after, before,
			"contention must be recorded at fraction %d", conf.RecommendedMutexProfileFraction)
	})
}

// countProfileEvents returns the event count the named runtime profile
// attributes to stacks mentioning wantFrame.
//
// It returns a count rather than testing for the frame's presence because
// presence cannot fail on a repeat run: block and mutex profiles accumulate for
// the life of the process and cannot be reset, so under go test -count=2 a
// frame left by the first run satisfies the second even if sampling was never
// switched on. Comparing a count before and against after does fail there.
//
// What the number is NOT is the raw count of samples taken. saveBlockEventStack
// up-scales by the sampling rate so the recorded value estimates the whole
// event population rather than the sampled subset: for the mutex profile it
// adds rate per sample, and for the block profile it adds rate/cycles when the
// event was shorter than the rate. Either way the result is close to
// rate-invariant wherever sampling happens at all, so this discriminates
// "sampling produced records" from "sampling produced none" and nothing finer.
//
// The legacy text format writes one record per unique stack as
// "<cycles> <count> @ <addrs>" (runtime/pprof.writeProfileInternal:
// Fprintf(w, "%v %v @", r.Cycles, r.Count), cycles FIRST) followed by
// "#\t<addr>\t<function>..." lines, so a record's count is attributed to
// wantFrame when any of its frame lines mentions it.
func countProfileEvents(t *testing.T, profileName, wantFrame string) int {
	t.Helper()

	var (
		total        int
		pendingCount int
		matched      bool
	)
	flush := func() {
		if matched {
			total += pendingCount
		}
		pendingCount, matched = 0, false
	}

	for line := range strings.Lines(dumpProfile(t, profileName)) {
		line = strings.TrimRight(line, "\n")
		switch {
		case strings.HasPrefix(line, "#"):
			if strings.Contains(line, wantFrame) {
				matched = true
			}
		case strings.Contains(line, " @ "):
			flush()
			// "<cycles> <count> @ ..." -- the record header. Field 1, not 0:
			// cycles come first.
			if fields := strings.Fields(line); len(fields) >= 2 {
				if count, err := strconv.Atoi(fields[1]); err == nil {
					pendingCount = count
				}
			}
		}
	}
	flush()

	return total
}

// dumpProfile renders a runtime profile in the legacy text format, which is
// symbolized and therefore greppable by function name.
func dumpProfile(t *testing.T, name string) string {
	t.Helper()

	profile := pprof.Lookup(name)
	require.NotNil(t, profile, "runtime profile %q must exist", name)

	var buf bytes.Buffer
	require.NoError(t, profile.WriteTo(&buf, 1))
	return buf.String()
}

// TestAggressiveRateThresholds covers the two branches that produce the
// advisory log lines.
//
// Without this, inverting either comparison is invisible: the branches only
// log, so the whole suite stays green with the guidance firing on exactly the
// rates it was written to bless.
func TestAggressiveRateThresholds(t *testing.T) {
	t.Parallel()

	t.Run("block rate", func(t *testing.T) {
		t.Parallel()

		assert.False(t, blockRateIsAggressive(0), "off is not aggressive")
		assert.True(t, blockRateIsAggressive(1), "rate 1 records every blocking event")
		assert.True(t, blockRateIsAggressive(aggressiveBlockRateNanos-1))
		assert.False(t, blockRateIsAggressive(aggressiveBlockRateNanos))
		assert.False(t, blockRateIsAggressive(conf.RecommendedBlockProfileRate),
			"the rate we recommend must never trigger our own warning")
	})

	t.Run("coarse block rate", func(t *testing.T) {
		t.Parallel()

		assert.False(t, blockRateIsCoarse(0), "off is not coarse, it is free")
		assert.False(t, blockRateIsCoarse(conf.RecommendedBlockProfileRate),
			"the rate we recommend must never trigger the coarse advisory either")
		assert.False(t, blockRateIsCoarse(coarseBlockProfileRate))
		assert.True(t, blockRateIsCoarse(coarseBlockProfileRate+1))

		// The two advisories must not both fire for one value, or the operator
		// gets told to go in both directions at once.
		assert.False(t, blockRateIsAggressive(coarseBlockProfileRate+1))
		assert.False(t, blockRateIsCoarse(1))
	})

	t.Run("mutex fraction", func(t *testing.T) {
		t.Parallel()

		assert.False(t, mutexFractionIsAggressive(0), "off is not aggressive")
		assert.True(t, mutexFractionIsAggressive(1), "fraction 1 records every contention event")
		assert.True(t, mutexFractionIsAggressive(aggressiveMutexFraction-1))
		assert.False(t, mutexFractionIsAggressive(aggressiveMutexFraction))
		assert.False(t, mutexFractionIsAggressive(conf.RecommendedMutexProfileFraction),
			"the fraction we recommend must never trigger our own warning")
	})
}

// blockOnChannel blocks the calling goroutine on a channel receive for long
// enough to be sampled.
//
// It must be a channel receive and not time.Sleep: the block profiler records
// goroutines parked on synchronization primitives (channel operations, select,
// sync.Mutex, sync.WaitGroup, sync.Cond), and a sleeping goroutine is parked on
// a timer, which it does not record at all. The sleep here runs in the OTHER
// goroutine, purely to guarantee the receive has to wait.
func blockOnChannel() {
	const blockFor = 2 * time.Millisecond

	ch := make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() {
		time.Sleep(blockFor)
		close(ch)
	})

	<-ch
	wg.Wait()
}

// contendMutex generates mutex contention events.
//
// An uncontended Lock/Unlock pair takes the fast path (a single atomic swap)
// and records nothing at all: the mutex profile only sees an event when a
// waiter had to park on the semaphore, and the sample is attributed to the
// goroutine whose Unlock released it. Yielding inside the critical section is
// what forces that, and it forces it even at GOMAXPROCS=1, where the waiters
// would otherwise never get scheduled while the lock was held.
func contendMutex() {
	const (
		contenders = 4
		iterations = 1000
	)

	var (
		mu sync.Mutex
		wg sync.WaitGroup
	)
	for range contenders {
		wg.Go(func() {
			for range iterations {
				mu.Lock()
				runtime.Gosched()
				mu.Unlock()
			}
		})
	}
	wg.Wait()
}
