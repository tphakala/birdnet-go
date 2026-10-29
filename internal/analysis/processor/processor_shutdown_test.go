package processor

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/analysis/jobqueue"
	"github.com/tphakala/birdnet-go/internal/conf"
)

func TestProcessor_ShutdownWithContext_RespectsDeadline(t *testing.T) {
	t.Parallel()

	t.Run("completes_within_short_deadline", func(t *testing.T) {
		t.Parallel()

		queue := jobqueue.NewJobQueue()
		queue.Start()
		t.Cleanup(func() {
			// Best-effort cleanup; shutdown test may have already stopped it.
			_ = queue.Stop()
		})

		p := &Processor{
			Settings: &conf.Settings{},
			JobQueue: queue,
		}

		ctx, cancel := context.WithTimeout(t.Context(), 500*time.Millisecond)
		defer cancel()

		start := time.Now()
		err := p.ShutdownWithContext(ctx)
		elapsed := time.Since(start)

		require.NoError(t, err)
		assert.Less(t, elapsed, 1*time.Second, "shutdown should complete well within 1s")
	})

	t.Run("nil_components_no_panic", func(t *testing.T) {
		t.Parallel()

		queue := jobqueue.NewJobQueue()
		queue.Start()
		t.Cleanup(func() {
			_ = queue.Stop()
		})

		// Processor with all optional components nil
		p := &Processor{
			Settings: &conf.Settings{},
			JobQueue: queue,
			// MqttClient: nil, BwClient: nil, NewSpeciesTracker: nil,
			// preRenderer: nil, thresholdsCancel: nil, flusherCancel: nil, workerCancel: nil
		}

		ctx, cancel := context.WithTimeout(t.Context(), 500*time.Millisecond)
		defer cancel()

		// Must not panic
		err := p.ShutdownWithContext(ctx)
		require.NoError(t, err)
	})

	t.Run("context_cancellation_aborts_threshold_flush", func(t *testing.T) {
		t.Parallel()

		queue := jobqueue.NewJobQueue()
		queue.Start()
		t.Cleanup(func() {
			_ = queue.Stop()
		})

		p := &Processor{
			Settings: &conf.Settings{
				Realtime: conf.RealtimeSettings{
					DynamicThreshold: conf.DynamicThresholdSettings{
						Enabled: true,
					},
				},
			},
			JobQueue:          queue,
			DynamicThresholds: make(map[string]*DynamicThreshold),
			pendingResets:     make(map[string]struct{}),
		}

		// Use a very short deadline; flush should be cut short
		ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
		defer cancel()

		start := time.Now()
		err := p.ShutdownWithContext(ctx)
		elapsed := time.Since(start)

		require.NoError(t, err)
		assert.Less(t, elapsed, 2*time.Second,
			"shutdown with dynamic thresholds enabled should not exceed 2s with a 100ms deadline")
	})

	t.Run("job_queue_receives_remaining_budget", func(t *testing.T) {
		t.Parallel()

		queue := jobqueue.NewJobQueue()
		queue.Start()
		t.Cleanup(func() {
			_ = queue.Stop()
		})

		p := &Processor{
			Settings: &conf.Settings{},
			JobQueue: queue,
		}

		ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
		defer cancel()

		err := p.ShutdownWithContext(ctx)
		require.NoError(t, err)
	})

	t.Run("expired_context_skips_non_critical_cleanup", func(t *testing.T) {
		t.Parallel()

		queue := jobqueue.NewJobQueue()
		queue.Start()
		t.Cleanup(func() {
			_ = queue.Stop()
		})

		p := &Processor{
			Settings: &conf.Settings{},
			JobQueue: queue,
		}

		// Already-cancelled context
		ctx, cancel := context.WithCancel(t.Context())
		cancel() // cancel immediately

		err := p.ShutdownWithContext(ctx)
		require.NoError(t, err)
	})

	t.Run("no_deadline_uses_default_timeout", func(t *testing.T) {
		t.Parallel()

		queue := jobqueue.NewJobQueue()
		queue.Start()
		t.Cleanup(func() {
			_ = queue.Stop()
		})

		p := &Processor{
			Settings: &conf.Settings{},
			JobQueue: queue,
		}

		// t.Context() has no deadline, so this exercises the fallback path
		start := time.Now()
		err := p.ShutdownWithContext(t.Context())
		elapsed := time.Since(start)

		require.NoError(t, err)
		// With an idle queue, this should complete quickly even with the 30s fallback
		assert.Less(t, elapsed, 5*time.Second, "idle queue shutdown should complete quickly")
	})
}

// TestProcessor_Shutdown_DelegatesToShutdownWithContext verifies that the existing
// Shutdown() method still works correctly after being refactored to delegate.
func TestProcessor_Shutdown_DelegatesToShutdownWithContext(t *testing.T) {
	t.Parallel()

	queue := jobqueue.NewJobQueue()
	queue.Start()
	t.Cleanup(func() {
		_ = queue.Stop()
	})

	p := &Processor{
		Settings: &conf.Settings{},
		JobQueue: queue,
	}

	start := time.Now()
	err := p.Shutdown()
	elapsed := time.Since(start)

	require.NoError(t, err)
	assert.Less(t, elapsed, 5*time.Second, "Shutdown() on idle processor should complete quickly")
}

// newShutdownFlushProcessor builds a processor in the state ShutdownWithContext
// sees at shutdown: one learned threshold and a threshold lifecycle context that
// is still set (as after StartDynamicThresholds). ShutdownWithContext cancels it
// before flushing, which is the state that used to abort the flush retry.
func newShutdownFlushProcessor(t *testing.T) (*Processor, *MockDatastore) {
	t.Helper()

	p := createTestProcessor()
	p.JobQueue = jobqueue.NewJobQueue() // not started: stopping it returns at once
	p.DynamicThresholds["american crow"] = &DynamicThreshold{
		Level:         1,
		BaseThreshold: 0.75,
		Timer:         time.Now().Add(24 * time.Hour),
		ValidHours:    48,
	}

	lifeCtx, lifeCancel := context.WithCancel(t.Context())
	t.Cleanup(lifeCancel)
	p.thresholdsCtx = lifeCtx
	p.thresholdsCancel = lifeCancel

	return p, p.Ds.(*MockDatastore)
}

// TestShutdownWithContext_DynamicThresholdFlush verifies the shutdown flush keeps
// the connectivity retry working after the threshold lifecycle context is
// cancelled, and that the retry stops at the shutdown deadline.
//
// Not parallel: currentSettings() reads the global settings snapshot that other
// non-parallel tests publish.
func TestShutdownWithContext_DynamicThresholdFlush(t *testing.T) {
	t.Run("retries a connectivity error and persists", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			p, mockDs := newShutdownFlushProcessor(t)

			var attempts atomic.Int32
			mockDs.batchSaveHook = func(call int) error {
				attempts.Add(1)
				if call == 1 {
					return gormJoinedInvalidConn(mysql.ErrInvalidConn)
				}
				return nil
			}

			ctx, cancel := context.WithTimeout(t.Context(), DefaultFlushTimeout)
			defer cancel()

			require.NoError(t, p.ShutdownWithContext(ctx))

			assert.Equal(t, int32(2), attempts.Load(), "the connectivity error must be retried once")
			assert.Contains(t, mockDs.thresholds, "american crow")
		})
	})

	t.Run("stops retrying at the shutdown deadline", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			p, mockDs := newShutdownFlushProcessor(t)

			var (
				mu           sync.Mutex
				attemptTimes []time.Time
			)
			mockDs.batchSaveHook = func(int) error {
				mu.Lock()
				attemptTimes = append(attemptTimes, time.Now())
				mu.Unlock()
				return gormJoinedInvalidConn(mysql.ErrInvalidConn)
			}

			// Between the second attempt (1 x base delay) and the third (3 x base delay).
			shutdownDeadline := thresholdSaveBaseDelay + thresholdSaveBaseDelay/2
			ctx, cancel := context.WithTimeout(t.Context(), shutdownDeadline)
			defer cancel()

			start := time.Now()
			require.NoError(t, p.ShutdownWithContext(ctx))
			assert.LessOrEqual(t, time.Since(start), shutdownDeadline)

			// Fake clock: run past where a third attempt would have fired.
			time.Sleep(4 * thresholdSaveBaseDelay)
			synctest.Wait()

			mu.Lock()
			defer mu.Unlock()
			assert.Len(t, attemptTimes, 2, "no save attempt may start after the shutdown deadline")
			for _, at := range attemptTimes {
				assert.True(t, at.Before(start.Add(shutdownDeadline)), "attempt at %v is past the deadline", at.Sub(start))
			}
		})
	})
}
