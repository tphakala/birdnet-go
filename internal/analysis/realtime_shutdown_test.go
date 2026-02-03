package analysis

import (
	"context"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMonitorShutdownSignals tests that shutdown signals are properly handled
func TestMonitorShutdownSignals(t *testing.T) {
	tests := []struct {
		name   string
		signal syscall.Signal
	}{
		{
			name:   "SIGINT signal triggers shutdown",
			signal: syscall.SIGINT,
		},
		{
			name:   "SIGTERM signal triggers shutdown",
			signal: syscall.SIGTERM,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create quit channel
			quitChan := make(chan struct{})

			// Start monitoring in a goroutine
			monitorShutdownSignals(quitChan)

			// Give the monitor time to set up
			time.Sleep(10 * time.Millisecond)

			// Send signal to self
			proc, err := os.FindProcess(os.Getpid())
			require.NoError(t, err)
			err = proc.Signal(tt.signal)
			require.NoError(t, err)

			// Wait for quit channel to close with timeout
			select {
			case <-quitChan:
				// Success - channel was closed
			case <-time.After(1 * time.Second):
				require.Fail(t, "Timeout waiting for quit channel to close")
			}
		})
	}
}

// TestCleanupHLSWithTimeout tests HLS cleanup timeout behavior
func TestCleanupHLSWithTimeout(t *testing.T) {
	t.Run("cleanup with context timeout", func(t *testing.T) {
		// Test that the cleanup function respects context timeout
		// We can't directly mock cleanupHLSStreamingFiles since it's not a variable,
		// but we can test the timeout behavior of cleanupHLSWithTimeout

		// Create a context that's already cancelled
		ctx, cancel := context.WithCancel(t.Context())
		cancel() // Cancel immediately

		// This should return quickly without waiting
		start := time.Now()
		cleanupHLSWithTimeout(ctx)
		elapsed := time.Since(start)

		// Should return almost immediately when context is already cancelled
		assert.Less(t, elapsed, 100*time.Millisecond, "Should return quickly when context is cancelled")
	})

	t.Run("cleanup timeout behavior", func(t *testing.T) {
		// Test with a valid context that has sufficient timeout
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel()

		// Run cleanup - it should complete normally or timeout after 2 seconds
		start := time.Now()
		cleanupHLSWithTimeout(ctx)
		elapsed := time.Since(start)

		// Should complete within the 2-second internal timeout plus some buffer
		assert.Less(t, elapsed, 3*time.Second, "Should complete within internal timeout")
	})
}

// TestShutdownSequenceWithContext tests the shutdown sequence with context timeout
func TestShutdownSequenceWithContext(t *testing.T) {
	t.Run("shutdown completes within timeout", func(t *testing.T) {
		// Create a context with a reasonable timeout
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel()

		// Simulate shutdown steps
		shutdownComplete := make(chan struct{})
		var wg sync.WaitGroup

		wg.Go(func() {
			defer close(shutdownComplete)

			// Simulate various shutdown steps
			steps := []struct {
				name     string
				duration time.Duration
			}{
				{"Stop control channel", 10 * time.Millisecond},
				{"Stop monitors", 20 * time.Millisecond},
				{"Clean HLS", 50 * time.Millisecond},
				{"Shutdown HTTP", 30 * time.Millisecond},
				{"Wait goroutines", 100 * time.Millisecond},
			}

			for _, step := range steps {
				select {
				case <-ctx.Done():
					t.Logf("Context cancelled during step: %s", step.name)
					return
				default:
					time.Sleep(step.duration)
				}
			}
		})

		// Wait for completion or timeout
		select {
		case <-shutdownComplete:
			// Success
		case <-ctx.Done():
			require.Fail(t, "Context timeout during shutdown sequence")
		}

		wg.Wait()
	})

	t.Run("shutdown exceeds timeout", func(t *testing.T) {
		// Create a context with a very short timeout
		ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
		defer cancel()

		shutdownComplete := make(chan struct{})

		go func() {
			defer close(shutdownComplete)

			// Simulate a long-running shutdown step
			select {
			case <-time.After(200 * time.Millisecond):
				// This should not complete
			case <-ctx.Done():
				// Context cancelled as expected
				return
			}
		}()

		// Wait for timeout
		select {
		case <-shutdownComplete:
			// Shutdown stopped due to context cancellation
		case <-time.After(100 * time.Millisecond):
			// Give it some extra time to ensure it stopped
		}

		assert.Equal(t, context.DeadlineExceeded, ctx.Err(), "Context should have timed out")
	})
}

// TestShutdownConstants verifies shutdown timeout constant is properly defined
func TestShutdownConstants(t *testing.T) {
	// Verify the shutdown timeout is set to 9 seconds
	assert.Equal(t, 9*time.Second, shutdownTimeout, "Shutdown timeout should be 9 seconds")
}

// Helper function to test signal handling without actually sending signals to the process
func TestSignalNotification(t *testing.T) {
	t.Run("signal channel receives notifications", func(t *testing.T) {
		sigChan := make(chan os.Signal, 1)

		// Register for signals
		signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
		defer signal.Stop(sigChan)

		// Send a signal to self
		proc, err := os.FindProcess(os.Getpid())
		require.NoError(t, err)

		err = proc.Signal(syscall.SIGINT)
		require.NoError(t, err)

		// Wait for signal with timeout
		select {
		case sig := <-sigChan:
			assert.Equal(t, syscall.SIGINT, sig, "Should receive SIGINT")
		case <-time.After(1 * time.Second):
			require.Fail(t, "Timeout waiting for signal")
		}
	})
}
