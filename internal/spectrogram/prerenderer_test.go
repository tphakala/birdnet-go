package spectrogram

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/securefs"
)

// Note: TestPreRenderer_SizeToPixels and TestPreRenderer_BuildSpectrogramPath
// have been moved to utils_test.go as they now test shared package-level functions.

// TestPreRenderer_Submit_FileAlreadyExists tests early file existence check
func TestPreRenderer_Submit_FileAlreadyExists(t *testing.T) {
	// Create temp directory
	tempDir := t.TempDir()

	// Use a clip path relative to temp dir (this is how it works in production)
	clipPath := filepath.Join(tempDir, "test.wav")
	spectrogramPath := filepath.Join(tempDir, "test.png")

	// Create the spectrogram file (simulating it already exists)
	err := os.WriteFile(spectrogramPath, []byte("fake png"), 0o600)
	require.NoError(t, err, "Failed to create test file")

	// Create PreRenderer
	settings := &conf.Settings{}
	settings.Realtime.Audio.Export.Path = tempDir

	sfs, err := securefs.New(tempDir)
	require.NoError(t, err, "Failed to create SecureFS")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pr := &PreRenderer{
		settings: settings,
		sfs:      sfs,
		logger:   slog.Default(),
		ctx:      ctx,
		cancel:   cancel,
		jobs:     make(chan *Job, 10),
	}

	// Submit job for file that already exists
	job := &Job{
		PCMData:   []byte{0, 1, 2, 3},
		ClipPath:  clipPath,
		NoteID:    1,
		Timestamp: time.Now(),
	}

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
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tempDir := t.TempDir()
	settings := &conf.Settings{}
	settings.Realtime.Audio.Export.Path = tempDir

	pr := &PreRenderer{
		settings: settings,
		logger:   slog.Default(),
		ctx:      ctx,
		cancel:   cancel,
		jobs:     make(chan *Job, 10),
	}

	job := &Job{
		PCMData:   []byte{0, 1, 2, 3},
		ClipPath:  filepath.Join(tempDir, "nonexistent.wav"),
		NoteID:    1,
		Timestamp: time.Now(),
	}

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
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tempDir := t.TempDir()
	settings := &conf.Settings{}
	settings.Realtime.Audio.Export.Path = tempDir

	// Create PreRenderer with very small queue
	pr := &PreRenderer{
		settings: settings,
		logger:   slog.Default(),
		ctx:      ctx,
		cancel:   cancel,
		jobs:     make(chan *Job, 1), // Only 1 slot
	}

	// Fill the queue
	job1 := &Job{
		PCMData:   []byte{0},
		ClipPath:  filepath.Join(tempDir, "test1.wav"),
		NoteID:    1,
		Timestamp: time.Now(),
	}
	_ = pr.Submit(job1)

	// Try to submit when full
	job2 := &Job{
		PCMData:   []byte{1},
		ClipPath:  filepath.Join(tempDir, "test2.wav"),
		NoteID:    2,
		Timestamp: time.Now(),
	}
	err := pr.Submit(job2)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrQueueFull, "Submit() expected ErrQueueFull")
}

// TestPreRenderer_GracefulShutdown tests shutdown with timeout
func TestPreRenderer_GracefulShutdown(t *testing.T) {
	ctx := t.Context()

	// Create minimal settings
	tempDir := t.TempDir()
	settings := &conf.Settings{}
	settings.Realtime.Audio.SoxPath = "/usr/bin/sox" // Will fail but that's ok for shutdown test
	settings.Realtime.Audio.Export.Path = tempDir
	settings.Realtime.Dashboard.Spectrogram.Enabled = true
	settings.Realtime.Dashboard.Spectrogram.Size = "sm"
	settings.Realtime.Dashboard.Spectrogram.Raw = true

	sfs, err := securefs.New(tempDir)
	require.NoError(t, err, "Failed to create SecureFS")

	pr := NewPreRenderer(ctx, settings, sfs, slog.Default())
	pr.Start()

	// Stop and verify graceful shutdown (Start launches workers, Stop cancels and waits)
	pr.Stop()

	// After stop, verify stats are accessible (no panics)
	_ = pr.GetStats()
}

// TestPreRenderer_Stats tests statistics tracking
func TestPreRenderer_Stats(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tempDir := t.TempDir()
	settings := &conf.Settings{}
	settings.Realtime.Audio.Export.Path = tempDir

	pr := &PreRenderer{
		settings: settings,
		logger:   slog.Default(),
		ctx:      ctx,
		cancel:   cancel,
		jobs:     make(chan *Job, 10),
	}

	// Initial stats should be zero
	stats := pr.GetStats()
	assert.Equal(t, int64(0), stats.Queued, "Initial Queued stat")
	assert.Equal(t, int64(0), stats.Completed, "Initial Completed stat")
	assert.Equal(t, int64(0), stats.Failed, "Initial Failed stat")
	assert.Equal(t, int64(0), stats.Skipped, "Initial Skipped stat")

	// Queue a job
	job := &Job{
		PCMData:   []byte{0},
		ClipPath:  filepath.Join(tempDir, "test.wav"),
		NoteID:    1,
		Timestamp: time.Now(),
	}
	_ = pr.Submit(job)

	// Stats should show queued
	stats = pr.GetStats()
	assert.Equal(t, int64(1), stats.Queued, "Stats queued after submit")
}

// TestPreRenderer_Submit_AfterStop tests submit after stop (panic guard)
func TestPreRenderer_Submit_AfterStop(t *testing.T) {
	ctx := t.Context()

	tempDir := t.TempDir()
	settings := &conf.Settings{}
	settings.Realtime.Audio.Export.Path = tempDir
	settings.Realtime.Dashboard.Spectrogram.Size = "sm"

	sfs, err := securefs.New(tempDir)
	require.NoError(t, err, "Failed to create SecureFS")

	pr := NewPreRenderer(ctx, settings, sfs, slog.Default())
	pr.Start()
	pr.Stop() // Closes channel and cancels context

	job := &Job{
		PCMData:   []byte{0},
		ClipPath:  filepath.Join(tempDir, "test.wav"),
		NoteID:    1,
		Timestamp: time.Now(),
	}

	// Submit after Stop should return error due to cancelled context
	err = pr.Submit(job)
	assert.Error(t, err, "Expected error after Stop()")
}
