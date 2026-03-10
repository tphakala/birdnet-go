package myaudio

import (
	"os/exec"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// skipOnWindows skips the test on Windows (tests use sh and Unix exit format).
func skipOnWindows(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("test requires Unix shell (sh)")
	}
}

// TestHandleQuickExitError_CapturesExitCode verifies that handleQuickExitError
// correctly captures the real exit code from the process via cmd.Wait(),
// rather than always returning -1/"unavailable" due to the async lifecycle.
func TestHandleQuickExitError_CapturesExitCode(t *testing.T) {
	skipOnWindows(t)
	t.Parallel()

	tests := []struct {
		name             string
		command          string
		args             []string
		wantExitCode     int
		wantStateContain string
	}{
		{
			name:             "exit code 1",
			command:          "sh",
			args:             []string{"-c", "exit 1"},
			wantExitCode:     1,
			wantStateContain: "exit status 1",
		},
		{
			name:             "exit code 42",
			command:          "sh",
			args:             []string{"-c", "exit 42"},
			wantExitCode:     42,
			wantStateContain: "exit status 42",
		},
		{
			name:             "exit code 0",
			command:          "sh",
			args:             []string{"-c", "exit 0"},
			wantExitCode:     0,
			wantStateContain: "exit status 0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result := newTestStream(t, "rtsp://test.example.com/stream")
			stream := result.Stream

			// Set up a real command that exits with a known code
			cmd := exec.Command(tt.command, tt.args...)
			require.NoError(t, cmd.Start(), "command should start")

			// Wait for the process to actually exit before calling handleQuickExitError
			// (simulates the real scenario where stdout returns EOF because process exited)
			_ = cmd.Wait()

			// Now re-start the command so we can test handleQuickExitError's Wait path
			// (handleQuickExitError calls cmd.Wait() itself, so the cmd must have been
			// started but not yet waited on)
			cmd = exec.Command(tt.command, tt.args...)
			require.NoError(t, cmd.Start(), "command should start for wait test")

			// Assign the command to the stream (simulating what startProcess does)
			stream.cmdMu.Lock()
			stream.cmd = cmd
			stream.cmdMu.Unlock()

			// Give the short-lived process time to exit
			time.Sleep(100 * time.Millisecond)

			// Call handleQuickExitError — this should call cmd.Wait() and capture
			// the real exit code instead of returning -1/"unavailable"
			startTime := time.Now().Add(-1 * time.Second) // pretend we started 1s ago
			err := stream.handleQuickExitError(startTime)

			require.Error(t, err, "should return an error for quick exit")

			// Verify the exit info was captured on the struct
			stream.exitInfoMu.Lock()
			exitCode := stream.exitExitCode
			processState := stream.exitProcessState
			waitCalled := stream.exitWaitCalled
			stream.exitInfoMu.Unlock()

			assert.True(t, waitCalled, "Wait should have been called")
			assert.Equal(t, tt.wantExitCode, exitCode, "exit code should be captured")
			assert.Contains(t, processState, tt.wantStateContain, "process state should contain exit info")

			// Verify the error message includes the exit code context
			assert.Contains(t, err.Error(), "FFmpeg process failed to start properly")
		})
	}
}

// TestHandleQuickExitError_NilCmd verifies handleQuickExitError handles nil cmd gracefully.
func TestHandleQuickExitError_NilCmd(t *testing.T) {
	skipOnWindows(t)
	t.Parallel()

	result := newTestStream(t, "rtsp://test.example.com/stream")
	stream := result.Stream

	// Ensure cmd is nil
	stream.cmdMu.Lock()
	stream.cmd = nil
	stream.cmdMu.Unlock()

	startTime := time.Now().Add(-1 * time.Second)
	err := stream.handleQuickExitError(startTime)

	require.Error(t, err, "should return an error even with nil cmd")
	assert.Contains(t, err.Error(), "FFmpeg process failed to start properly")

	// Exit info should remain at struct zero values (cmd was nil, nothing to wait on)
	stream.exitInfoMu.Lock()
	assert.False(t, stream.exitWaitCalled, "Wait should not be called when cmd is nil")
	stream.exitInfoMu.Unlock()
}

// TestCleanupProcess_SkipsWaitWhenAlreadyCalled verifies that cleanupProcess
// does not call Wait() again when handleQuickExitError already did.
func TestCleanupProcess_SkipsWaitWhenAlreadyCalled(t *testing.T) {
	skipOnWindows(t)
	t.Parallel()

	result := newTestStream(t, "rtsp://test.example.com/stream")
	stream := result.Stream

	// Start a real process
	cmd := exec.Command("sh", "-c", "exit 1")
	require.NoError(t, cmd.Start(), "command should start")

	// Assign to stream
	stream.cmdMu.Lock()
	stream.cmd = cmd
	stream.cmdMu.Unlock()

	// Give process time to exit
	time.Sleep(100 * time.Millisecond)

	// Simulate handleQuickExitError having called Wait
	startTime := time.Now().Add(-1 * time.Second)
	err := stream.handleQuickExitError(startTime)
	require.Error(t, err)

	// Verify Wait was marked as called
	stream.exitInfoMu.Lock()
	assert.True(t, stream.exitWaitCalled)
	stream.exitInfoMu.Unlock()

	// Now call cleanupProcess — it should skip Wait and not panic
	// We need to re-assign cmd since handleQuickExitError doesn't clear it
	stream.cmdMu.Lock()
	stream.cmd = cmd
	stream.cmdMu.Unlock()

	// This should not panic or hang
	stream.cleanupProcess()

	// After cleanup, exit info should be reset
	stream.exitInfoMu.Lock()
	assert.False(t, stream.exitWaitCalled, "exitWaitCalled should be reset after cleanup")
	stream.exitInfoMu.Unlock()
}
