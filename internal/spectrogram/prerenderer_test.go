package spectrogram

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Note: TestPreRenderer_SizeToPixels and TestPreRenderer_BuildSpectrogramPath
// have been moved to utils_test.go as they now test shared package-level functions.

// TestPreRenderer_Submit_FileAlreadyExists tests early file existence check
func TestPreRenderer_Submit_FileAlreadyExists(t *testing.T) {
	pr, env := createMinimalPreRenderer(t, 10)

	// Use a clip path relative to temp dir (this is how it works in production)
	clipPath := filepath.Join(env.TempDir, "test.wav")
	spectrogramPath := filepath.Join(env.TempDir, "test.png")

	// Create the spectrogram file (simulating it already exists)
	err := os.WriteFile(spectrogramPath, []byte("fake png"), 0o600)
	require.NoError(t, err, "Failed to create test file")

	// Submit job for file that already exists
	job := createTestJob(clipPath, 1)

	// Should return nil (not queued, but not an error)
	err = pr.Submit(job)
	require.NoError(t, err, "Submit() unexpected error for existing file")

	// Verify job was not queued
	select {
	case <-pr.jobs:
		assert.Fail(t, "Submit() queued job for existing file, expected to skip")
	default:
		// Expected: job not queued
	}

	// Verify stats show skipped
	stats := pr.GetStats()
	assert.Equal(t, int64(1), stats.Skipped, "Submit() skipped count")
}

// TestPreRenderer_Submit_Success tests successful job submission
func TestPreRenderer_Submit_Success(t *testing.T) {
	pr, env := createMinimalPreRenderer(t, 10)

	job := createTestJob(filepath.Join(env.TempDir, "nonexistent.wav"), 1)

	err := pr.Submit(job)
	require.NoError(t, err, "Submit() unexpected error")

	// Verify job was queued
	select {
	case queuedJob := <-pr.jobs:
		assert.Equal(t, job.NoteID, queuedJob.NoteID, "Submit() queued wrong job")
	case <-time.After(100 * time.Millisecond):
		assert.Fail(t, "Submit() did not queue job within timeout")
	}

	// Verify stats
	stats := pr.GetStats()
	assert.Equal(t, int64(1), stats.Queued, "Submit() queued count")
}

// TestPreRenderer_Submit_QueueFull tests queue overflow behavior
func TestPreRenderer_Submit_QueueFull(t *testing.T) {
	// Create PreRenderer with very small queue (1 slot)
	pr, env := createMinimalPreRenderer(t, 1)

	// Fill the queue
	job1 := createTestJob(filepath.Join(env.TempDir, "test1.wav"), 1)
	_ = pr.Submit(job1)

	// Try to submit when full
	job2 := createTestJob(filepath.Join(env.TempDir, "test2.wav"), 2)
	err := pr.Submit(job2)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrQueueFull, "Submit() expected ErrQueueFull")
}

// TestPreRenderer_GracefulShutdown tests shutdown with timeout
func TestPreRenderer_GracefulShutdown(t *testing.T) {
	pr, _ := createTestPreRenderer(t, nil)
	pr.Start()

	// Stop and verify graceful shutdown (Start launches workers, Stop cancels and waits)
	pr.Stop()

	// After stop, verify stats are accessible (no panics)
	_ = pr.GetStats()
}

// TestPreRenderer_Stats tests statistics tracking
func TestPreRenderer_Stats(t *testing.T) {
	pr, env := createMinimalPreRenderer(t, 10)

	// Initial stats should be zero
	stats := pr.GetStats()
	assert.Equal(t, int64(0), stats.Queued, "Initial Queued stat")
	assert.Equal(t, int64(0), stats.Completed, "Initial Completed stat")
	assert.Equal(t, int64(0), stats.Failed, "Initial Failed stat")
	assert.Equal(t, int64(0), stats.Skipped, "Initial Skipped stat")

	// Queue a job
	job := createTestJob(filepath.Join(env.TempDir, "test.wav"), 1)
	_ = pr.Submit(job)

	// Stats should show queued
	stats = pr.GetStats()
	assert.Equal(t, int64(1), stats.Queued, "Stats queued after submit")
}

// TestPreRenderer_Submit_AfterStop tests submit after stop (panic guard)
func TestPreRenderer_Submit_AfterStop(t *testing.T) {
	pr, env := createTestPreRenderer(t, nil)
	pr.Start()
	pr.Stop() // Closes channel and cancels context

	job := createTestJob(filepath.Join(env.TempDir, "test.wav"), 1)

	// Submit after Stop should return error due to cancelled context
	err := pr.Submit(job)
	assert.Error(t, err, "Expected error after Stop()")
}
