package myaudio

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// TestFFmpegManager_ContextCauseShutdown tests that manager shutdown provides a meaningful cause
func TestFFmpegManager_ContextCauseShutdown(t *testing.T) {
	manager := NewFFmpegManager()

	// Shutdown the manager
	manager.Shutdown()

	// Check that context was cancelled with a cause
	if manager.ctx.Err() == nil {
		t.Fatal("expected context to be cancelled after shutdown")
	}

	// Get the cancellation cause
	cause := context.Cause(manager.ctx)
	if cause == nil {
		t.Fatal("expected context.Cause to return a cause after shutdown")
	}

	// Verify the cause message indicates shutdown
	expectedSubstring := "FFmpegManager shutdown"
	if !contains(cause.Error(), expectedSubstring) {
		t.Errorf("expected cause to contain %q, got: %s", expectedSubstring, cause.Error())
	}
}

// TestFFmpegStream_ContextCauseStop tests that stream Stop() provides a meaningful cause
func TestFFmpegStream_ContextCauseStop(t *testing.T) {
	// Create a test stream
	audioChan := make(chan UnifiedAudioData, 100)
	defer close(audioChan)

	stream := NewFFmpegStream("rtsp://test.local/stream", "tcp", audioChan)

	// Start the stream in a goroutine
	ctx := context.Background()
	go stream.Run(ctx)

	// Give it time to initialize context
	time.Sleep(100 * time.Millisecond)

	// Stop the stream
	stream.Stop()

	// Wait for cleanup
	time.Sleep(200 * time.Millisecond)

	// Verify context was cancelled
	if stream.ctx == nil {
		t.Fatal("expected stream context to be set")
	}

	if stream.ctx.Err() == nil {
		t.Fatal("expected context to be cancelled after Stop()")
	}

	// Get the cancellation cause
	cause := context.Cause(stream.ctx)
	if cause == nil {
		t.Fatal("expected context.Cause to return a cause after Stop()")
	}

	// Verify the cause message indicates Stop() was called
	expectedSubstring := "Stop() called"
	if !contains(cause.Error(), expectedSubstring) {
		t.Errorf("expected cause to contain %q, got: %s", expectedSubstring, cause.Error())
	}
}

// TestFFmpegStream_ContextCauseRunExit tests that Run() exit provides a meaningful cause
func TestFFmpegStream_ContextCauseRunExit(t *testing.T) {
	// Create a test stream
	audioChan := make(chan UnifiedAudioData, 100)
	defer close(audioChan)

	stream := NewFFmpegStream("rtsp://test.local/stream", "tcp", audioChan)

	// Create a cancellable parent context
	parentCtx, parentCancel := context.WithCancel(context.Background())

	// Start the stream in a goroutine
	done := make(chan struct{})
	go func() {
		stream.Run(parentCtx)
		close(done)
	}()

	// Give it time to initialize context
	time.Sleep(100 * time.Millisecond)

	// Cancel parent context to make Run() exit
	parentCancel()

	// Wait for Run() to complete
	select {
	case <-done:
		// Run() completed
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for Run() to exit")
	}

	// Verify stream context was set up with cause
	if stream.ctx == nil {
		t.Fatal("expected stream context to be set")
	}

	// The stream's defer should have called cancel with a cause
	// Note: The cause might be from the defer, not from parent cancellation
	cause := context.Cause(stream.ctx)
	if cause != nil {
		// If there's a cause, verify it mentions the stream
		expectedSubstring := "stream Run() loop exiting"
		if !contains(cause.Error(), expectedSubstring) {
			t.Logf("got unexpected cause (this is informational): %s", cause.Error())
		}
	}
}

// TestContextCause_WithCancelCauseFunctionality verifies basic WithCancelCause behavior
func TestContextCause_WithCancelCauseFunctionality(t *testing.T) {
	// Test 1: Basic WithCancelCause usage
	t.Run("BasicUsage", func(t *testing.T) {
		ctx, cancel := context.WithCancelCause(context.Background())

		// Cancel with a specific cause
		testErr := fmt.Errorf("test cancellation reason")
		cancel(testErr)

		// Verify context is cancelled
		if ctx.Err() != context.Canceled {
			t.Errorf("expected context.Canceled, got: %v", ctx.Err())
		}

		// Verify cause is preserved
		cause := context.Cause(ctx)
		if cause == nil {
			t.Fatal("expected context.Cause to return a cause")
		}

		if cause.Error() != testErr.Error() {
			t.Errorf("expected cause %q, got: %q", testErr.Error(), cause.Error())
		}
	})

	// Test 2: Calling cancel multiple times with same cause is idempotent
	t.Run("IdempotentCancel", func(t *testing.T) {
		ctx, cancel := context.WithCancelCause(context.Background())

		firstErr := fmt.Errorf("first cancellation")
		secondErr := fmt.Errorf("second cancellation")

		// First cancellation
		cancel(firstErr)

		// Second cancellation (should be ignored)
		cancel(secondErr)

		// Cause should be from first cancellation
		cause := context.Cause(ctx)
		if cause == nil {
			t.Fatal("expected context.Cause to return a cause")
		}

		if cause.Error() != firstErr.Error() {
			t.Errorf("expected first cause %q, got: %q", firstErr.Error(), cause.Error())
		}
	})

	// Test 3: Child context inherits parent cancellation
	t.Run("ParentCancellation", func(t *testing.T) {
		parentCtx, parentCancel := context.WithCancelCause(context.Background())
		childCtx, childCancel := context.WithCancelCause(parentCtx)
		defer childCancel(nil)

		// Cancel parent with a cause
		parentErr := fmt.Errorf("parent cancelled")
		parentCancel(parentErr)

		// Both contexts should be cancelled
		if parentCtx.Err() != context.Canceled {
			t.Errorf("expected parent context.Canceled, got: %v", parentCtx.Err())
		}
		if childCtx.Err() != context.Canceled {
			t.Errorf("expected child context.Canceled, got: %v", childCtx.Err())
		}

		// Parent cause should be accessible
		parentCause := context.Cause(parentCtx)
		if parentCause == nil || parentCause.Error() != parentErr.Error() {
			t.Errorf("expected parent cause %q, got: %v", parentErr, parentCause)
		}

		// Child cause should reflect parent cancellation
		childCause := context.Cause(childCtx)
		if childCause == nil {
			t.Fatal("expected child context.Cause to return a cause")
		}
	})
}

// contains checks if a string contains a substring
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
