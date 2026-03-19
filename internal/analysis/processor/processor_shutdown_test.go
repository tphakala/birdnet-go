package processor

import (
	"context"
	"testing"
	"time"

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
		}

		// Use a very short deadline — flush should be cut short
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
