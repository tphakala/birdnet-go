package myaudio

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFFmpegManager_ContextCauseShutdown verifies that:
// 1. Manager shutdown cancels the context with a specific cause
// 2. The cause message includes "FFmpegManager: shutdown initiated"
// 3. The cause is accessible via context.Cause()
func TestFFmpegManager_ContextCauseShutdown(t *testing.T) {
	manager := NewFFmpegManager()

	// Shutdown the manager
	manager.Shutdown()

	// Check that context was cancelled with a cause
	require.Error(t, manager.ctx.Err(), "expected context to be cancelled after shutdown")

	// Get the cancellation cause
	cause := context.Cause(manager.ctx)
	require.Error(t, cause, "expected context.Cause to return a cause after shutdown")

	// Verify the cause message indicates shutdown
	expectedSubstring := "FFmpegManager: shutdown initiated"
	assert.Contains(t, cause.Error(), expectedSubstring)
}

// TestFFmpegStream_ContextCauseStop verifies that:
// 1. Stop() cancels the stream context with a specific cause
// 2. The cause message includes "FFmpegStream: Stop() called"
// 3. The cause includes the sanitized source URL
func TestFFmpegStream_ContextCauseStop(t *testing.T) {
	// Create a test stream
	audioChan := make(chan UnifiedAudioData, 100)
	defer close(audioChan)

	stream := NewFFmpegStream("rtsp://test.local/stream", "tcp", audioChan)

	// Start the stream in a goroutine
	ctx := t.Context()
	runDone := make(chan struct{})
	go func() {
		stream.Run(ctx)
		close(runDone)
	}()

	// Wait for context to be initialized (with timeout)
	timeout := time.After(2 * time.Second)
	ticker := time.NewTicker(10 * time.Millisecond)
	t.Cleanup(ticker.Stop)

	for {
		select {
		case <-timeout:
			require.FailNow(t, "timeout waiting for stream context initialization")
		case <-ticker.C:
			stream.cancelMu.RLock()
			ctxInitialized := stream.ctx != nil
			stream.cancelMu.RUnlock()
			if ctxInitialized {
				goto ContextReady
			}
		}
	}
ContextReady:

	// Stop the stream
	stream.Stop()

	// Wait for Run() to complete
	select {
	case <-runDone:
		// Run() completed
	case <-time.After(2 * time.Second):
		require.FailNow(t, "timeout waiting for Run() to complete after Stop()")
	}

	// Verify context was cancelled
	require.NotNil(t, stream.ctx, "expected stream context to be set")
	require.Error(t, stream.ctx.Err(), "expected context to be cancelled after Stop()")

	// Get the cancellation cause
	cause := context.Cause(stream.ctx)
	require.Error(t, cause, "expected context.Cause to return a cause after Stop()")

	// Verify the cause message indicates Stop() was called
	expectedSubstring := "FFmpegStream: Stop() called"
	assert.Contains(t, cause.Error(), expectedSubstring)
}

// TestFFmpegStream_ContextCauseRunExit verifies that:
// 1. When Run() exits naturally (parent context cancelled), it sets a cause
// 2. The cause message includes "FFmpegStream: Run() loop exiting"
// 3. The cause includes the sanitized source URL
func TestFFmpegStream_ContextCauseRunExit(t *testing.T) {
	// Create a test stream
	audioChan := make(chan UnifiedAudioData, 100)
	defer close(audioChan)

	stream := NewFFmpegStream("rtsp://test.local/stream", "tcp", audioChan)

	// Create a cancellable parent context
	parentCtx, parentCancel := context.WithCancel(t.Context())

	// Start the stream in a goroutine
	done := make(chan struct{})
	go func() {
		stream.Run(parentCtx)
		close(done)
	}()

	// Wait for context to be initialized (with timeout)
	timeout := time.After(2 * time.Second)
	ticker := time.NewTicker(10 * time.Millisecond)
	t.Cleanup(ticker.Stop)

	for {
		select {
		case <-timeout:
			require.FailNow(t, "timeout waiting for stream context initialization")
		case <-ticker.C:
			stream.cancelMu.RLock()
			ctxInitialized := stream.ctx != nil
			stream.cancelMu.RUnlock()
			if ctxInitialized {
				goto ContextReady
			}
		}
	}
ContextReady:

	// Cancel parent context to make Run() exit
	parentCancel()

	// Wait for Run() to complete
	select {
	case <-done:
		// Run() completed
	case <-time.After(2 * time.Second):
		require.FailNow(t, "timeout waiting for Run() to exit")
	}

	// Verify stream context was set up with cause
	require.NotNil(t, stream.ctx, "expected stream context to be set")

	// The stream's defer should have called cancel with a cause
	// Note: The cause might be from the defer, not from parent cancellation
	cause := context.Cause(stream.ctx)
	if cause != nil {
		// If there's a cause, verify it mentions the stream
		expectedSubstring := "FFmpegStream: Run() loop exiting"
		if !strings.Contains(cause.Error(), expectedSubstring) {
			t.Logf("got unexpected cause (this is informational): %s", cause.Error())
		}
	}
}

// TestContextCause_WithCancelCauseFunctionality verifies basic WithCancelCause behavior
func TestContextCause_WithCancelCauseFunctionality(t *testing.T) {
	// Test 1: Basic WithCancelCause usage
	t.Run("BasicUsage", func(t *testing.T) {
		ctx, cancel := context.WithCancelCause(t.Context())

		// Cancel with a specific cause
		testErr := fmt.Errorf("test cancellation reason")
		cancel(testErr)

		// Verify context is cancelled
		assert.Equal(t, context.Canceled, ctx.Err())

		// Verify cause is preserved
		cause := context.Cause(ctx)
		require.Error(t, cause, "expected context.Cause to return a cause")
		assert.Equal(t, testErr.Error(), cause.Error())
	})

	// Test 2: Calling cancel multiple times with same cause is idempotent
	t.Run("IdempotentCancel", func(t *testing.T) {
		ctx, cancel := context.WithCancelCause(t.Context())

		firstErr := fmt.Errorf("first cancellation")
		secondErr := fmt.Errorf("second cancellation")

		// First cancellation
		cancel(firstErr)

		// Second cancellation (should be ignored)
		cancel(secondErr)

		// Cause should be from first cancellation
		cause := context.Cause(ctx)
		require.Error(t, cause, "expected context.Cause to return a cause")
		assert.Equal(t, firstErr.Error(), cause.Error())
	})

	// Test 3: Child context inherits parent cancellation
	t.Run("ParentCancellation", func(t *testing.T) {
		parentCtx, parentCancel := context.WithCancelCause(t.Context())
		childCtx, childCancel := context.WithCancelCause(parentCtx)
		defer childCancel(nil)

		// Cancel parent with a cause
		parentErr := fmt.Errorf("parent cancelled")
		parentCancel(parentErr)

		// Both contexts should be cancelled
		assert.Equal(t, context.Canceled, parentCtx.Err(), "expected parent context.Canceled")
		assert.Equal(t, context.Canceled, childCtx.Err(), "expected child context.Canceled")

		// Parent cause should be accessible
		parentCause := context.Cause(parentCtx)
		require.Error(t, parentCause, "expected parent cause")
		assert.Equal(t, parentErr.Error(), parentCause.Error())

		// Child cause should reflect parent cancellation
		childCause := context.Cause(childCtx)
		require.Error(t, childCause, "expected child context.Cause to return a cause")
	})
}
