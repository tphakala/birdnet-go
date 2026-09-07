package audiocore

import (
	"sync"
	"sync/atomic"
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
		var mu sync.Mutex
		notifyCalled := false

		w := NewLivenessWatchdog(cfg, r, LivenessCallbacks{
			IsQuietHours: func(_ string) bool { return true },
			Notify: func(_ string, _ LivenessState, _ string) {
				mu.Lock()
				notifyCalled = true
				mu.Unlock()
			},
		})

		// Seed a frame, then let silence accumulate during quiet hours.
		dispatchFrame(r, src)
		w.Start()
		defer w.Stop()

		time.Sleep(cfg.SilenceThreshold + 3*cfg.CheckInterval)

		snaps := w.Snapshot()
		for _, s := range snaps {
			assert.Equal(t, "HEALTHY", s.State,
				"no alarm should be raised during quiet hours")
		}
		mu.Lock()
		assert.False(t, notifyCalled, "notify should not be called during quiet hours")
		mu.Unlock()
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

// TestLiveness_NoRestartDuringSupervisedReconnect is contract case 16: while a
// producer reports RecoveryInProgress within the ceiling, the watchdog alarms
// once but never restarts or escalates, so it does not tear down a supervisor
// that is reconnecting in place.
func TestLiveness_NoRestartDuringSupervisedReconnect(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const src = "src-1"
		r := setupRouter(t, src)
		defer r.Close()

		cfg := fastConfig()
		cfg.ProducerRecoveryCeiling = 5 * time.Minute

		var restarts, escalations atomic.Int32
		var mu sync.Mutex
		alarms := 0

		// recoveryStart is fixed for the whole outage, mirroring the native
		// producer, which advances RecoveryEntered only when the recovery intent
		// changes, not on every reconnect attempt.
		recoveryStart := time.Now()

		w := NewLivenessWatchdog(cfg, r, LivenessCallbacks{
			RestartSource: func(_ string) error { restarts.Add(1); return nil },
			Escalate:      func(_ string) { escalations.Add(1) },
			Notify: func(_ string, state LivenessState, _ string) {
				if state == StateAlarmed {
					mu.Lock()
					alarms++
					mu.Unlock()
				}
			},
			RecoveryState: func(_ string) (RecoveryState, time.Time) {
				return RecoveryInProgress, recoveryStart
			},
		})

		dispatchFrame(r, src)
		w.Start()
		defer w.Stop()

		// Simulate a 60 s outage: the supervisor is reconnecting in place the whole
		// time, well within the 5 min ceiling.
		time.Sleep(60 * time.Second)

		assert.Zero(t, restarts.Load(), "watchdog must not restart while the supervisor reconnects within the ceiling")
		assert.Zero(t, escalations.Load(), "watchdog must not escalate while suppressed")

		snaps := w.Snapshot()
		require.Len(t, snaps, 1)
		assert.Equal(t, "RECOVERING", snaps[0].State, "source parks in RECOVERING while the supervisor works")

		mu.Lock()
		assert.Equal(t, 1, alarms, "silence still alarms exactly once on the HEALTHY->ALARMED edge")
		mu.Unlock()
	})
}

// TestLiveness_ZeroCeilingDisablesCoordination verifies that a non-positive
// ProducerRecoveryCeiling turns the coordination off entirely: the watchdog takes
// the legacy restart path even while the producer reports RecoveryInProgress, and
// it never invokes the RecoveryState callback.
func TestLiveness_ZeroCeilingDisablesCoordination(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const src = "src-1"
		r := setupRouter(t, src)
		defer r.Close()

		cfg := fastConfig()
		cfg.ProducerRecoveryCeiling = 0 // coordination disabled

		var restarts, recoveryCalls atomic.Int32
		w := NewLivenessWatchdog(cfg, r, LivenessCallbacks{
			RestartSource: func(_ string) error { restarts.Add(1); return nil },
			RecoveryState: func(_ string) (RecoveryState, time.Time) {
				recoveryCalls.Add(1)
				return RecoveryInProgress, time.Now()
			},
		})

		dispatchFrame(r, src)
		w.Start()
		defer w.Stop()

		time.Sleep(cfg.SilenceThreshold + 3*cfg.CheckInterval)

		assert.NotZero(t, restarts.Load(), "a zero ceiling must take the legacy restart path despite RecoveryInProgress")
		assert.Zero(t, recoveryCalls.Load(), "a zero ceiling short-circuits before invoking the RecoveryState callback")
	})
}

// TestLiveness_RestartWhenSupervisorGivesUp is the other half of contract case
// 16: once the producer reports RecoveryGivenUp the watchdog restarts exactly
// once, which (in production) re-adds the source under a fresh dispatch clock.
func TestLiveness_RestartWhenSupervisorGivesUp(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const src = "src-1"
		r := setupRouter(t, src)
		defer r.Close()

		cfg := fastConfig()
		cfg.ProducerRecoveryCeiling = 5 * time.Minute

		var restarts atomic.Int32
		w := NewLivenessWatchdog(cfg, r, LivenessCallbacks{
			RestartSource: func(_ string) error {
				restarts.Add(1)
				// A successful restart brings the source back, mirroring the real
				// RestartSource re-adding the source with a fresh dispatch clock.
				dispatchFrame(r, src)
				return nil
			},
			RecoveryState: func(_ string) (RecoveryState, time.Time) {
				return RecoveryGivenUp, time.Time{}
			},
		})

		dispatchFrame(r, src)
		w.Start()
		defer w.Stop()

		time.Sleep(cfg.SilenceThreshold + 5*cfg.CheckInterval)

		assert.Equal(t, int32(1), restarts.Load(), "a give-up must restart exactly once")

		snaps := w.Snapshot()
		require.Len(t, snaps, 1)
		assert.Equal(t, "HEALTHY", snaps[0].State, "source recovers after the single restart")
	})
}

// TestLiveness_RestartPastRecoveryCeiling verifies the ceiling backstop: a
// producer stuck in RecoveryInProgress past ProducerRecoveryCeiling falls back
// to the legacy restart ladder so a genuinely dead source is not parked forever.
func TestLiveness_RestartPastRecoveryCeiling(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const src = "src-1"
		r := setupRouter(t, src)
		defer r.Close()

		cfg := fastConfig()
		cfg.ProducerRecoveryCeiling = 200 * time.Millisecond // outage quickly exceeds it

		var restarts atomic.Int32
		recoveryStart := time.Now()
		w := NewLivenessWatchdog(cfg, r, LivenessCallbacks{
			RestartSource: func(_ string) error { restarts.Add(1); return nil },
			RecoveryState: func(_ string) (RecoveryState, time.Time) {
				return RecoveryInProgress, recoveryStart
			},
		})

		dispatchFrame(r, src)
		w.Start()
		defer w.Stop()

		time.Sleep(cfg.SilenceThreshold + 10*cfg.CheckInterval)

		assert.NotZero(t, restarts.Load(), "past the ceiling the legacy restart ladder resumes")
	})
}

// TestLiveness_UnknownRecoveryTakesLegacyPath pins FFmpeg parity: a producer
// reporting RecoveryUnknown (as FFmpeg always does) takes the legacy restart
// path unchanged, so the coordination is a no-op under the ffmpeg gate.
func TestLiveness_UnknownRecoveryTakesLegacyPath(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const src = "src-1"
		r := setupRouter(t, src)
		defer r.Close()

		cfg := fastConfig()
		cfg.ProducerRecoveryCeiling = 5 * time.Minute

		var restarts atomic.Int32
		w := NewLivenessWatchdog(cfg, r, LivenessCallbacks{
			RestartSource: func(_ string) error { restarts.Add(1); return nil },
			RecoveryState: func(_ string) (RecoveryState, time.Time) {
				return RecoveryUnknown, time.Time{}
			},
		})

		dispatchFrame(r, src)
		w.Start()
		defer w.Stop()

		time.Sleep(cfg.SilenceThreshold + 3*cfg.CheckInterval)

		assert.NotZero(t, restarts.Load(), "RecoveryUnknown (FFmpeg) takes the legacy restart path unchanged")
	})
}
