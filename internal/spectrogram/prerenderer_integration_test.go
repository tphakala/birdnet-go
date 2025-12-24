//go:build integration

package spectrogram

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/errors"
)

const (
	// Test timeouts
	testShortTimeout  = 5 * time.Second
	testMediumTimeout = 15 * time.Second
	testLongTimeout   = 30 * time.Second

	// Test polling interval
	testPollInterval = 100 * time.Millisecond
)

// TestPreRenderer_RealSoxExecution tests actual spectrogram generation with Sox
// Run with: go test -v -tags=integration ./internal/spectrogram/...
func TestPreRenderer_RealSoxExecution(t *testing.T) {
	soxPath := requireSoxAvailable(t)
	env := createIntegrationPreRenderer(t, soxPath, "test")
	defer env.PreRenderer.Stop()

	// Generate synthetic PCM data using shared helper
	pcmData := generateTestPCMData(nil)

	// Submit job
	clipPath := filepath.Join(env.AudioDir, "test.wav")
	job := &Job{
		PCMData:   pcmData,
		ClipPath:  clipPath,
		NoteID:    1,
		Timestamp: time.Now(),
	}

	err := env.PreRenderer.Submit(job)
	require.NoError(t, err, "Failed to submit job")

	// Wait for processing with timeout
	timeout := time.After(testShortTimeout)
	ticker := time.NewTicker(testPollInterval)
	defer ticker.Stop()

	spectrogramPath := filepath.Join(env.AudioDir, "test.png")

	for {
		select {
		case <-timeout:
			stats := env.PreRenderer.GetStats()
			require.Fail(t, "Timeout waiting for spectrogram generation", "Stats: %+v", stats)
		case <-ticker.C:
			if _, statErr := os.Stat(spectrogramPath); statErr == nil {
				// File exists, verify it's a valid PNG
				f, openErr := os.Open(spectrogramPath)
				require.NoError(t, openErr, "Failed to open spectrogram")
				defer f.Close()

				// Check PNG magic number (first 8 bytes: 89 50 4E 47 0D 0A 1A 0A)
				hdr := make([]byte, 8)
				_, readErr := io.ReadFull(f, hdr)
				require.NoError(t, readErr, "Failed to read PNG header")

				pngMagic := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}
				assert.Equal(t, pngMagic, hdr, "Invalid PNG magic number")

				// Verify stats
				stats := env.PreRenderer.GetStats()
				assert.Equal(t, int64(1), stats.Completed, "Expected 1 completed job")
				assert.Equal(t, int64(0), stats.Failed, "Expected 0 failed jobs")

				// Log file size
				if fi, fiErr := f.Stat(); fiErr == nil {
					t.Logf("Successfully generated spectrogram: %s (%d bytes)", spectrogramPath, fi.Size())
				} else {
					t.Logf("Successfully generated spectrogram: %s", spectrogramPath)
				}
				return // Test passed
			}
			// File doesn't exist yet, continue waiting
		}
	}
}

// TestPreRenderer_ConcurrentProcessing tests multiple jobs being processed concurrently
// Run with: go test -v -race -tags=integration ./internal/spectrogram/...
func TestPreRenderer_ConcurrentProcessing(t *testing.T) {
	soxPath := requireSoxAvailable(t)
	env := createIntegrationPreRenderer(t, soxPath, "concurrent")
	defer env.PreRenderer.Stop()

	// Generate synthetic PCM data using shared helper
	pcmData := generateTestPCMData(nil)

	// Submit multiple jobs concurrently
	numJobs := 5
	for i := 0; i < numJobs; i++ {
		clipPath := filepath.Join(env.AudioDir, fmt.Sprintf("test-%d.wav", i))
		job := &Job{
			PCMData:   pcmData,
			ClipPath:  clipPath,
			NoteID:    uint(i + 1),
			Timestamp: time.Now(),
		}

		err := env.PreRenderer.Submit(job)
		require.NoError(t, err, "Failed to submit job %d", i)
	}

	// Wait for all jobs to complete
	timeout := time.After(testMediumTimeout)
	ticker := time.NewTicker(testPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-timeout:
			stats := env.PreRenderer.GetStats()
			require.Fail(t, "Timeout waiting for concurrent jobs", "Stats: %+v", stats)
		case <-ticker.C:
			stats := env.PreRenderer.GetStats()
			if stats.Completed+stats.Skipped >= int64(numJobs) {
				// All jobs processed
				assert.Equal(t, int64(0), stats.Failed, "Some jobs failed")

				// Verify all spectrograms exist
				for i := 0; i < numJobs; i++ {
					spectrogramPath := filepath.Join(env.AudioDir, fmt.Sprintf("test-%d.png", i))
					_, err := os.Stat(spectrogramPath)
					assert.NoError(t, err, "Spectrogram %d not found", i)
				}

				t.Logf("Successfully processed %d concurrent jobs. Stats: %+v", numJobs, stats)
				return // Test passed
			}
		}
	}
}

// TestPreRenderer_GracefulShutdownUnderLoad tests shutdown with active jobs
func TestPreRenderer_GracefulShutdownUnderLoad(t *testing.T) {
	soxPath := requireSoxAvailable(t)
	env := createIntegrationPreRenderer(t, soxPath, "shutdown")
	// Note: Don't defer Stop() here - we manually stop to test shutdown behavior

	// Generate synthetic PCM data using shared helper
	pcmData := generateTestPCMData(nil)

	// Submit several jobs
	numJobs := 10
	for i := 0; i < numJobs; i++ {
		clipPath := filepath.Join(env.AudioDir, fmt.Sprintf("test-%d.wav", i))
		job := &Job{
			PCMData:   pcmData,
			ClipPath:  clipPath,
			NoteID:    uint(i + 1),
			Timestamp: time.Now(),
		}

		_ = env.PreRenderer.Submit(job)
	}

	// Immediately trigger shutdown (jobs may still be in queue)
	env.PreRenderer.Stop()

	// Verify clean shutdown (no panics, no goroutine leaks)
	// This is verified by the test completing without hanging

	stats := env.PreRenderer.GetStats()
	t.Logf("Shutdown completed. Stats: queued=%d, completed=%d, failed=%d, skipped=%d",
		stats.Queued, stats.Completed, stats.Failed, stats.Skipped)

	// Some jobs may not have completed due to shutdown, but that's expected
	if stats.Queued > 0 && stats.Completed+stats.Failed+stats.Skipped == 0 {
		assert.Fail(t, "No jobs were processed before shutdown")
	}
}

// TestPreRenderer_QueueOverflow tests behavior when submitting more jobs than queue size
func TestPreRenderer_QueueOverflow(t *testing.T) {
	soxPath := requireSoxAvailable(t)
	env := createIntegrationPreRenderer(t, soxPath, "overflow")
	defer env.PreRenderer.Stop()

	// Generate synthetic PCM data using shared helper
	pcmData := generateTestPCMData(nil)

	// Submit more jobs than queue size to trigger overflow
	// Queue size is 3 (2 workers + 1 waiting), submit 20 jobs rapidly
	numJobs := 20
	queueFull := 0
	submitted := 0

	for i := 0; i < numJobs; i++ {
		clipPath := filepath.Join(env.AudioDir, fmt.Sprintf("test-%03d.wav", i))
		job := &Job{
			PCMData:   pcmData,
			ClipPath:  clipPath,
			NoteID:    uint(i + 1),
			Timestamp: time.Now(),
		}

		if err := env.PreRenderer.Submit(job); err != nil {
			if errors.Is(err, ErrQueueFull) {
				queueFull++
			} else {
				assert.Fail(t, "Unexpected error submitting job", "job %d: %v", i, err)
			}
		} else {
			submitted++
		}
	}

	// Verify that some jobs were rejected due to queue overflow
	assert.Greater(t, queueFull, 0, "Expected some jobs to be rejected due to queue overflow")

	t.Logf("Submitted %d jobs, %d rejected due to queue overflow", submitted, queueFull)

	// Wait for workers to drain the queue
	timeout := time.After(testLongTimeout)
	ticker := time.NewTicker(testPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-timeout:
			stats := env.PreRenderer.GetStats()
			require.Fail(t, "Timeout waiting for queue to drain", "Stats: %+v", stats)
		case <-ticker.C:
			stats := env.PreRenderer.GetStats()
			// Queue is drained when all submitted jobs are processed
			if stats.Completed+stats.Failed+stats.Skipped >= int64(submitted) {
				t.Logf("Queue drained. Final stats: queued=%d, completed=%d, failed=%d, skipped=%d",
					stats.Queued, stats.Completed, stats.Failed, stats.Skipped)

				// Verify stats consistency
				assert.Equal(t, int64(submitted), stats.Queued, "Expected queued count")

				return // Test passed
			}
		}
	}
}
