package spectrogram

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/securefs"
)

// TestPreRenderer_SizeToPixels tests the size string to pixel width conversion
func TestPreRenderer_SizeToPixels(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		size      string
		wantWidth int
		wantErr   bool
	}{
		{"small", "sm", 400, false},
		{"medium", "md", 800, false},
		{"large", "lg", 1000, false},
		{"extra large", "xl", 1200, false},
		{"invalid", "invalid", 0, true},
		{"empty", "", 0, true},
	}

	pr := &PreRenderer{}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			gotWidth, err := pr.sizeToPixels(tt.size)
			if (err != nil) != tt.wantErr {
				t.Errorf("sizeToPixels() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if gotWidth != tt.wantWidth {
				t.Errorf("sizeToPixels() = %v, want %v", gotWidth, tt.wantWidth)
			}
		})
	}
}

// TestPreRenderer_BuildSpectrogramPath tests spectrogram path building
func TestPreRenderer_BuildSpectrogramPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		clipPath  string
		wantPath  string
		wantErr   bool
	}{
		{
			name:     "wav file",
			clipPath: "clips/2024-01-15/test.wav",
			wantPath: "clips/2024-01-15/test.png",
			wantErr:  false,
		},
		{
			name:     "mp3 file",
			clipPath: "clips/bird.mp3",
			wantPath: "clips/bird.png",
			wantErr:  false,
		},
		{
			name:     "no extension",
			clipPath: "clips/noext",
			wantPath: "",
			wantErr:  true,
		},
	}

	pr := &PreRenderer{}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			gotPath, err := pr.buildSpectrogramPath(tt.clipPath)
			if (err != nil) != tt.wantErr {
				t.Errorf("buildSpectrogramPath() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if gotPath != tt.wantPath {
				t.Errorf("buildSpectrogramPath() = %v, want %v", gotPath, tt.wantPath)
			}
		})
	}
}

// TestPreRenderer_Submit_FileAlreadyExists tests early file existence check
func TestPreRenderer_Submit_FileAlreadyExists(t *testing.T) {
	// Create temp directory
	tempDir := t.TempDir()

	// Use a clip path relative to temp dir (this is how it works in production)
	clipPath := filepath.Join(tempDir, "test.wav")
	spectrogramPath := filepath.Join(tempDir, "test.png")

	// Create the spectrogram file (simulating it already exists)
	if err := os.WriteFile(spectrogramPath, []byte("fake png"), 0o644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Create PreRenderer
	settings := &conf.Settings{}
	settings.Realtime.Audio.Export.Path = tempDir

	sfs, err := securefs.New(tempDir)
	if err != nil {
		t.Fatalf("Failed to create SecureFS: %v", err)
	}

	pr := &PreRenderer{
		settings: settings,
		sfs:      sfs,
		logger:   slog.Default(),
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
	if err != nil {
		t.Errorf("Submit() unexpected error for existing file: %v", err)
	}

	// Verify job was not queued
	select {
	case <-pr.jobs:
		t.Error("Submit() queued job for existing file, expected to skip")
	default:
		// Expected: job not queued
	}

	// Verify stats show skipped
	stats := pr.GetStats()
	if stats.Skipped != 1 {
		t.Errorf("Submit() skipped count = %d, want 1", stats.Skipped)
	}
}

// TestPreRenderer_Submit_Success tests successful job submission
func TestPreRenderer_Submit_Success(t *testing.T) {
	pr := &PreRenderer{
		settings: &conf.Settings{},
		logger:   slog.Default(),
		jobs:     make(chan *Job, 10),
	}

	job := &Job{
		PCMData:   []byte{0, 1, 2, 3},
		ClipPath:  "nonexistent.wav",
		NoteID:    1,
		Timestamp: time.Now(),
	}

	err := pr.Submit(job)
	if err != nil {
		t.Errorf("Submit() unexpected error: %v", err)
	}

	// Verify job was queued
	select {
	case queuedJob := <-pr.jobs:
		if queuedJob.NoteID != job.NoteID {
			t.Errorf("Submit() queued wrong job, got NoteID %d, want %d", queuedJob.NoteID, job.NoteID)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("Submit() did not queue job within timeout")
	}

	// Verify stats
	stats := pr.GetStats()
	if stats.Queued != 1 {
		t.Errorf("Submit() queued count = %d, want 1", stats.Queued)
	}
}

// TestPreRenderer_Submit_QueueFull tests queue overflow behavior
func TestPreRenderer_Submit_QueueFull(t *testing.T) {
	// Create PreRenderer with very small queue
	pr := &PreRenderer{
		settings: &conf.Settings{},
		logger:   slog.Default(),
		jobs:     make(chan *Job, 1), // Only 1 slot
	}

	// Fill the queue
	job1 := &Job{
		PCMData:   []byte{0},
		ClipPath:  "test1.wav",
		NoteID:    1,
		Timestamp: time.Now(),
	}
	_ = pr.Submit(job1)

	// Try to submit when full
	job2 := &Job{
		PCMData:   []byte{1},
		ClipPath:  "test2.wav",
		NoteID:    2,
		Timestamp: time.Now(),
	}
	err := pr.Submit(job2)
	if err == nil || !errors.Is(err, ErrQueueFull) {
		t.Errorf("Submit() expected ErrQueueFull, got %v", err)
	}
}

// TestPreRenderer_GracefulShutdown tests shutdown with timeout
func TestPreRenderer_GracefulShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Create minimal settings
	tempDir := t.TempDir()
	settings := &conf.Settings{}
	settings.Realtime.Audio.SoxPath = "/usr/bin/sox" // Will fail but that's ok for shutdown test
	settings.Realtime.Audio.Export.Path = tempDir
	settings.Realtime.Dashboard.Spectrogram.Enabled = true
	settings.Realtime.Dashboard.Spectrogram.Size = "sm"
	settings.Realtime.Dashboard.Spectrogram.Raw = true

	sfs, err := securefs.New(tempDir)
	if err != nil {
		t.Fatalf("Failed to create SecureFS: %v", err)
	}

	pr := NewPreRenderer(ctx, settings, sfs, slog.Default())
	pr.Start()

	// Give workers time to start
	time.Sleep(50 * time.Millisecond)

	// Stop and verify graceful shutdown
	pr.Stop()

	// After stop, verify stats are accessible (no panics)
	_ = pr.GetStats()
}

// TestPreRenderer_Stats tests statistics tracking
func TestPreRenderer_Stats(t *testing.T) {
	pr := &PreRenderer{
		settings: &conf.Settings{},
		logger:   slog.Default(),
		jobs:     make(chan *Job, 10),
	}

	// Initial stats should be zero
	stats := pr.GetStats()
	if stats.Queued != 0 || stats.Completed != 0 || stats.Failed != 0 || stats.Skipped != 0 {
		t.Errorf("Initial stats not zero: %+v", stats)
	}

	// Queue a job
	job := &Job{
		PCMData:   []byte{0},
		ClipPath:  "test.wav",
		NoteID:    1,
		Timestamp: time.Now(),
	}
	_ = pr.Submit(job)

	// Stats should show queued
	stats = pr.GetStats()
	if stats.Queued != 1 {
		t.Errorf("Stats queued = %d, want 1", stats.Queued)
	}
}

// TestPreRenderer_Submit_AfterStop tests submit after stop (panic guard)
func TestPreRenderer_Submit_AfterStop(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tempDir := t.TempDir()
	settings := &conf.Settings{}
	settings.Realtime.Audio.Export.Path = tempDir
	settings.Realtime.Dashboard.Spectrogram.Size = "sm"

	sfs, err := securefs.New(tempDir)
	if err != nil {
		t.Fatalf("Failed to create SecureFS: %v", err)
	}

	pr := NewPreRenderer(ctx, settings, sfs, slog.Default())
	pr.Start()
	pr.Stop() // Closes channel

	job := &Job{
		PCMData:   []byte{0},
		ClipPath:  filepath.Join(tempDir, "test.wav"),
		NoteID:    1,
		Timestamp: time.Now(),
	}

	// Submit after Stop should return error (not panic)
	err = pr.Submit(job)
	if err == nil {
		t.Fatal("Expected error after Stop(), got nil")
	}
}
