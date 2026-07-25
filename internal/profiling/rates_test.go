package profiling

import (
	"bytes"
	"runtime"
	"runtime/pprof"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/conf"
)

// None of the tests in this file may call t.Parallel(). The block and mutex
// profile rates are process-global runtime state, so two tests setting them
// concurrently would read each other's values.

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

// settingsWithRates builds a settings snapshot carrying only the two rates.
func settingsWithRates(blockRate, mutexFraction int) *conf.Settings {
	settings := &conf.Settings{}
	settings.Diagnostics.Profiling.BlockRate = blockRate
	settings.Diagnostics.Profiling.MutexFraction = mutexFraction
	return settings
}

// currentMutexFraction reads the live mutex fraction without changing it.
//
// A negative argument to SetMutexProfileFraction means "report the current
// value and leave it alone", which is the only way to read either rate back
// from the runtime. There is no equivalent for the block rate, which is why
// ApplyRates returns what it applied.
func currentMutexFraction() int {
	return runtime.SetMutexProfileFraction(-1)
}

func TestApplyRates(t *testing.T) {
	tests := []struct {
		name              string
		blockRate         int
		mutexFraction     int
		wantBlockRate     int
		wantMutexFraction int
	}{
		{
			name: "unset leaves both profilers off",
		},
		{
			name:              "recommended sampling defaults",
			blockRate:         conf.DefaultBlockProfileRate,
			mutexFraction:     conf.DefaultMutexProfileFraction,
			wantBlockRate:     conf.DefaultBlockProfileRate,
			wantMutexFraction: conf.DefaultMutexProfileFraction,
		},
		{
			name:              "block only",
			blockRate:         50000,
			wantBlockRate:     50000,
			wantMutexFraction: 0,
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

			// Start from a known non-zero mutex fraction so a case expecting 0
			// proves the value was actively cleared rather than never set. This
			// is what catches an unclamped negative being passed through to the
			// runtime, where it would read the fraction and leave it at 999.
			runtime.SetMutexProfileFraction(999)

			blockRate, mutexFraction := ApplyRates(settingsWithRates(tt.blockRate, tt.mutexFraction))

			assert.Equal(t, tt.wantBlockRate, blockRate, "applied block rate")
			assert.Equal(t, tt.wantMutexFraction, mutexFraction, "applied mutex fraction")
			assert.Equal(t, tt.wantMutexFraction, currentMutexFraction(),
				"the runtime's live mutex fraction must match what ApplyRates reported")
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

	settings := settingsWithRates(0, 0)
	settings.Debug = true
	settings.WebServer.Debug = true
	settings.Realtime.Telemetry.Enabled = true
	settings.Diagnostics.Profiling.Enabled = true

	blockRate, mutexFraction := ApplyRates(settings)

	assert.Zero(t, blockRate,
		"debug: true must not switch on block profiling")
	assert.Zero(t, mutexFraction,
		"debug: true must not switch on mutex profiling")
	assert.Zero(t, currentMutexFraction(),
		"nothing but the explicit rate settings may move the runtime's mutex fraction")
}

// TestApplyRatesNilSettings pins the nil guard: a diagnostics setting must not
// be able to panic the startup path.
func TestApplyRatesNilSettings(t *testing.T) {
	resetRatesAfterTest(t)

	blockRate, mutexFraction := ApplyRates(nil)

	assert.Zero(t, blockRate)
	assert.Zero(t, mutexFraction)
}

// TestDefaultRatesProduceUsableProfiles is the acceptance test for the sampling
// defaults: coarser rates are only worth having if a real contention problem
// still shows up in the profile.
//
// It exercises genuine blocking and genuine lock contention rather than
// asserting the rates were stored, because "the value was applied" and "the
// profile is usable at that value" are different claims and only the second one
// matters to someone debugging a stall.
func TestDefaultRatesProduceUsableProfiles(t *testing.T) {
	resetRatesAfterTest(t)

	applied, fraction := ApplyRates(settingsWithRates(conf.DefaultBlockProfileRate, conf.DefaultMutexProfileFraction))
	require.Equal(t, conf.DefaultBlockProfileRate, applied)
	require.Equal(t, conf.DefaultMutexProfileFraction, fraction)

	t.Run("block profile records a blocked channel receive", func(t *testing.T) {
		// A 2ms block is 200x the 10µs sampling rate, and the runtime always
		// records an event at least as long as the rate, so one call suffices.
		// The loop is insurance against a scheduler that returns early, not a
		// statistical requirement.
		requireProfileContains(t, "block", "blockOnChannel", blockOnChannel)
	})

	t.Run("mutex profile records real lock contention", func(t *testing.T) {
		// Unlike the block case this genuinely is statistical: at 1-in-100,
		// each contention event has a 1% chance of being sampled, so the
		// generator produces thousands of events per round.
		requireProfileContains(t, "mutex", "contendMutex", contendMutex)
	})
}

// profileSampleDeadline bounds the retry loop below. Both generators are
// expected to succeed on the first round; this only stops a pathological
// scheduler from hanging the suite.
const profileSampleDeadline = 30 * time.Second

// requireProfileContains runs generate until the named runtime profile contains
// a frame mentioning wantFrame, or the deadline expires.
//
// Block and mutex profiles are cumulative for the life of the process and
// cannot be reset, so the assertion is scoped to a frame only this test
// produces rather than to a sample count.
func requireProfileContains(t *testing.T, profileName, wantFrame string, generate func()) {
	t.Helper()

	deadline := time.Now().Add(profileSampleDeadline)
	for rounds := 1; ; rounds++ {
		generate()

		if strings.Contains(dumpProfile(t, profileName), wantFrame) {
			t.Logf("%s profile contained %q after %d round(s)", profileName, wantFrame, rounds)
			return
		}

		if time.Now().After(deadline) {
			t.Fatalf("%s profile contained no %q frame after %d round(s) at the default sampling rate; "+
				"the default is too coarse to be usable, or sampling was never applied",
				profileName, wantFrame, rounds)
		}
	}
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
