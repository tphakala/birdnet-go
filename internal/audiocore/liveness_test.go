package audiocore

import (
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// nullConsumer implements AudioConsumer with no-op writes.
// Used to register routes so ActiveSourceIDs returns results.
type nullConsumer struct {
	id         string
	sampleRate int
}

func (c *nullConsumer) ID() string               { return c.id }
func (c *nullConsumer) SampleRate() int          { return c.sampleRate }
func (c *nullConsumer) BitDepth() int            { return 16 }
func (c *nullConsumer) Channels() int            { return 1 }
func (c *nullConsumer) Write(_ AudioFrame) error { return nil } //nolint:gocritic // hugeParam: signature required by AudioConsumer interface
func (c *nullConsumer) Close() error             { return nil }

// fastConfig returns a LivenessConfig with short intervals suitable for
// deterministic tests under synctest.
func fastConfig() LivenessConfig {
	return LivenessConfig{
		CheckInterval:      100 * time.Millisecond,
		SilenceThreshold:   300 * time.Millisecond,
		MaxRetries:         3,
		RetryBackoff:       100 * time.Millisecond,
		CooldownAfterRecov: 500 * time.Millisecond,
		EscalationTimeout:  500 * time.Millisecond,
	}
}

// setupRouter creates a router with a single source that has an active route.
func setupRouter(t *testing.T, sourceID string) *AudioRouter {
	t.Helper()
	r := NewAudioRouter(GetLogger(), nil)
	consumer := &nullConsumer{id: "null-consumer", sampleRate: 48000}
	err := r.AddRoute(sourceID, consumer, 48000, 0, nil)
	require.NoError(t, err)
	return r
}

// dispatchFrame sends a minimal audio frame through the router, which updates
// the last dispatch timestamp for the source.
func dispatchFrame(r *AudioRouter, sourceID string) {
	r.Dispatch(AudioFrame{
		SourceID:   sourceID,
		Data:       []byte{0, 0},
		SampleRate: 48000,
		BitDepth:   16,
		Channels:   1,
		Timestamp:  time.Now(),
	})
}

func TestLiveness_HealthyWithFrames(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const src = "src-1"
		r := setupRouter(t, src)
		defer r.Close()

		cfg := fastConfig()
		var mu sync.Mutex
		var lastState LivenessState = -1

		w := NewLivenessWatchdog(cfg, r, LivenessCallbacks{
			Notify: func(_ string, state LivenessState, _ string) {
				mu.Lock()
				lastState = state
				mu.Unlock()
			},
		})
		w.Start()
		defer w.Stop()

		// Keep dispatching frames faster than the silence threshold.
		for range 5 {
			dispatchFrame(r, src)
			time.Sleep(cfg.CheckInterval)
		}

		snaps := w.Snapshot()
		require.Len(t, snaps, 1)
		assert.Equal(t, "HEALTHY", snaps[0].State)

		mu.Lock()
		assert.Equal(t, LivenessState(-1), lastState, "notify should not be called when healthy")
		mu.Unlock()
	})
}

func TestLiveness_SilenceTriggersAlarm(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const src = "src-1"
		r := setupRouter(t, src)
		defer r.Close()

		cfg := fastConfig()
		restarts := make(chan string, 10)

		w := NewLivenessWatchdog(cfg, r, LivenessCallbacks{
			RestartSource: func(id string) error {
				restarts <- id
				return nil
			},
		})

		// Dispatch once to seed the timestamp, then let silence accumulate.
		dispatchFrame(r, src)

		w.Start()
		defer w.Stop()

		// Wait past silence threshold + two ticks (alarm then recovering).
		time.Sleep(cfg.SilenceThreshold + 2*cfg.CheckInterval)

		snaps := w.Snapshot()
		require.Len(t, snaps, 1)
		assert.Equal(t, "RECOVERING", snaps[0].State)

		// At least one restart should have been attempted.
		assert.NotEmpty(t, restarts, "expected at least one restart attempt")
	})
}

func TestLiveness_RecoveryAfterRestart(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const src = "src-1"
		r := setupRouter(t, src)
		defer r.Close()

		cfg := fastConfig()
		var mu sync.Mutex
		states := make([]LivenessState, 0, 8)

		w := NewLivenessWatchdog(cfg, r, LivenessCallbacks{
			RestartSource: func(_ string) error { return nil },
			Notify: func(_ string, state LivenessState, _ string) {
				mu.Lock()
				states = append(states, state)
				mu.Unlock()
			},
		})

		// Seed a frame, then let silence build up.
		dispatchFrame(r, src)
		w.Start()
		defer w.Stop()

		// Wait for alarm + recovery attempt.
		time.Sleep(cfg.SilenceThreshold + 2*cfg.CheckInterval)

		// Resume frames and wait for two ticks so the watchdog sees the fresh frame.
		dispatchFrame(r, src)
		time.Sleep(2 * cfg.CheckInterval)

		snaps := w.Snapshot()
		require.Len(t, snaps, 1)
		assert.Equal(t, "HEALTHY", snaps[0].State)

		mu.Lock()
		assert.Contains(t, states, StateHealthy, "should have notified recovery")
		mu.Unlock()
	})
}

func TestLiveness_EscalationAfterMaxRetries(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const src = "src-1"
		r := setupRouter(t, src)
		defer r.Close()

		cfg := fastConfig()
		cfg.MaxRetries = 2
		cfg.RetryBackoff = 50 * time.Millisecond

		escalated := make(chan string, 1)

		w := NewLivenessWatchdog(cfg, r, LivenessCallbacks{
			RestartSource: func(_ string) error { return nil },
			Escalate: func(id string) {
				escalated <- id
			},
		})

		dispatchFrame(r, src)
		w.Start()
		defer w.Stop()

		// Wait long enough for: silence detection + alarm + retries exhausted.
		// silence threshold + alarm tick + (maxRetries * backoff) + extra ticks
		waitTime := cfg.SilenceThreshold + cfg.CheckInterval*(time.Duration(cfg.MaxRetries)+5)
		time.Sleep(waitTime)

		snaps := w.Snapshot()
		require.Len(t, snaps, 1)
		assert.Contains(t, []string{"ESCALATED", "FAILED"}, snaps[0].State,
			"should have escalated or failed after retries exhausted")
		assert.NotEmpty(t, escalated, "escalate callback should have been called")
	})
}

func TestLiveness_FailedAfterEscalationTimeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const src = "src-1"
		r := setupRouter(t, src)
		defer r.Close()

		cfg := fastConfig()
		cfg.MaxRetries = 1
		cfg.RetryBackoff = 50 * time.Millisecond
		cfg.EscalationTimeout = 200 * time.Millisecond

		var mu sync.Mutex
		notifiedStates := make([]LivenessState, 0, 8)

		w := NewLivenessWatchdog(cfg, r, LivenessCallbacks{
			RestartSource: func(_ string) error { return nil },
			Escalate:      func(_ string) {},
			Notify: func(_ string, state LivenessState, _ string) {
				mu.Lock()
				notifiedStates = append(notifiedStates, state)
				mu.Unlock()
			},
		})

		dispatchFrame(r, src)
		w.Start()
		defer w.Stop()

		// Wait for full progression: alarm -> recovering -> escalated -> failed.
		waitTime := cfg.SilenceThreshold + cfg.EscalationTimeout +
			cfg.CheckInterval*20
		time.Sleep(waitTime)

		snaps := w.Snapshot()
		require.Len(t, snaps, 1)
		assert.Equal(t, "FAILED", snaps[0].State)

		mu.Lock()
		assert.Contains(t, notifiedStates, StateFailed, "should have notified FAILED")
		mu.Unlock()
	})
}

func TestLiveness_QuietHoursSuppressesAlarm(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const src = "src-1"
		r := setupRouter(t, src)
		defer r.Close()

		cfg := fastConfig()
		notifyCalled := false

		w := NewLivenessWatchdog(cfg, r, LivenessCallbacks{
			IsQuietHours: func(_ string) bool { return true },
			Notify: func(_ string, _ LivenessState, _ string) {
				notifyCalled = true
			},
		})

		// Seed a frame, then let silence accumulate during quiet hours.
		dispatchFrame(r, src)
		w.Start()
		defer w.Stop()

		time.Sleep(cfg.SilenceThreshold + 3*cfg.CheckInterval)

		snaps := w.Snapshot()
		// During quiet hours no sources should be tracked (checkAll returns early).
		for _, s := range snaps {
			assert.Equal(t, "HEALTHY", s.State,
				"no alarm should be raised during quiet hours")
		}
		assert.False(t, notifyCalled, "notify should not be called during quiet hours")
	})
}

func TestLiveness_QuietHoursEndResetsTimestamp(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const src = "src-1"
		r := setupRouter(t, src)
		defer r.Close()

		cfg := fastConfig()
		var mu sync.Mutex
		quiet := true

		w := NewLivenessWatchdog(cfg, r, LivenessCallbacks{
			IsQuietHours: func(_ string) bool {
				mu.Lock()
				defer mu.Unlock()
				return quiet
			},
			RestartSource: func(_ string) error { return nil },
		})

		// Seed a frame.
		dispatchFrame(r, src)
		w.Start()
		defer w.Stop()

		// Run a few ticks in quiet hours so the frame timestamp gets stale.
		time.Sleep(cfg.SilenceThreshold + 2*cfg.CheckInterval)

		// Transition out of quiet hours.
		mu.Lock()
		quiet = false
		mu.Unlock()

		// After transition, dispatch time should be reset. The source should
		// stay healthy because the watchdog reset timestamps.
		time.Sleep(2 * cfg.CheckInterval)

		snaps := w.Snapshot()
		require.Len(t, snaps, 1)
		assert.Equal(t, "HEALTHY", snaps[0].State,
			"source should be healthy after quiet hours end with reset timestamp")
	})
}

func TestLiveness_SnapshotIsEmpty(t *testing.T) {
	r := NewAudioRouter(GetLogger(), nil)
	defer r.Close()

	w := NewLivenessWatchdog(DefaultLivenessConfig(), r, LivenessCallbacks{})
	snaps := w.Snapshot()
	assert.Empty(t, snaps, "snapshot should be empty when no sources are tracked")
}

func TestLiveness_RecoveryFromFailed(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const src = "src-1"
		r := setupRouter(t, src)
		defer r.Close()

		cfg := fastConfig()
		cfg.MaxRetries = 1
		cfg.RetryBackoff = 50 * time.Millisecond
		cfg.EscalationTimeout = 200 * time.Millisecond

		w := NewLivenessWatchdog(cfg, r, LivenessCallbacks{
			RestartSource: func(_ string) error { return nil },
			Escalate:      func(_ string) {},
		})

		dispatchFrame(r, src)
		w.Start()
		defer w.Stop()

		// Wait for FAILED state.
		waitTime := cfg.SilenceThreshold + cfg.EscalationTimeout +
			cfg.CheckInterval*20
		time.Sleep(waitTime)

		snaps := w.Snapshot()
		require.Len(t, snaps, 1)
		require.Equal(t, "FAILED", snaps[0].State, "should reach FAILED first")

		// Resume frames and verify recovery.
		dispatchFrame(r, src)
		time.Sleep(2 * cfg.CheckInterval)

		snaps = w.Snapshot()
		require.Len(t, snaps, 1)
		assert.Equal(t, "HEALTHY", snaps[0].State,
			"should recover from FAILED when frames resume")
	})
}
