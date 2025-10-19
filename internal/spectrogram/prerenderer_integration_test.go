//go:build integration
// +build integration

package spectrogram

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/securefs"
)

const (
	// Test audio parameters
	testSampleRate = 48000        // 48kHz PCM
	testDuration   = 1            // 1 second clips
	testFrequency  = 440.0        // 440Hz (A4 note)
	testAmplitude  = int16(10000) // Moderate volume

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
	// Check if Sox is available
	soxPath, err := exec.LookPath("sox")
	if err != nil {
		t.Skip("Sox binary not found in PATH; skipping integration test")
	}

	// Create temp directory
	tempDir := t.TempDir()

	// Create settings
	settings := &conf.Settings{}
	settings.Realtime.Audio.SoxPath = soxPath
	settings.Realtime.Audio.Export.Path = tempDir
	settings.Realtime.Dashboard.Spectrogram.Enabled = true
	settings.Realtime.Dashboard.Spectrogram.Size = "sm"
	settings.Realtime.Dashboard.Spectrogram.Raw = true

	sfs, err := securefs.New(tempDir)
	if err != nil {
		t.Fatalf("Failed to create SecureFS: %v", err)
	}

	// Create PreRenderer
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pr := NewPreRenderer(ctx, settings, sfs, slog.Default())
	pr.Start()
	defer pr.Stop()

	// Generate synthetic PCM data (1 second at 48kHz, 16-bit, mono)
	// PCM format: s16le (signed 16-bit little-endian)
	pcmData := make([]byte, testSampleRate*testDuration*2) // 2 bytes per sample (16-bit)

	// Generate 440Hz sine wave (A4 note) for realistic audio data
	for i := 0; i < testSampleRate*testDuration; i++ {
		// Calculate sine wave sample value
		sample := int16(float64(testAmplitude) * math.Sin(2*math.Pi*testFrequency*float64(i)/float64(testSampleRate)))
		// Convert to little-endian bytes
		pcmData[i*2] = byte(sample & 0xFF)
		pcmData[i*2+1] = byte((sample >> 8) & 0xFF)
	}

	// Create output directory
	audioDir := filepath.Join(tempDir, "test")
	if err := os.MkdirAll(audioDir, 0o755); err != nil {
		t.Fatalf("Failed to create audio directory: %v", err)
	}

	// Submit job
	clipPath := filepath.Join(audioDir, "test.wav")
	job := &Job{
		PCMData:   pcmData,
		ClipPath:  clipPath,
		NoteID:    1,
		Timestamp: time.Now(),
	}

	if err := pr.Submit(job); err != nil {
		t.Fatalf("Failed to submit job: %v", err)
	}

	// Wait for processing with timeout
	timeout := time.After(testShortTimeout)
	ticker := time.NewTicker(testPollInterval)
	defer ticker.Stop()

	spectrogramPath := filepath.Join(audioDir, "test.png")

	for {
		select {
		case <-timeout:
			stats := pr.GetStats()
			t.Fatalf("Timeout waiting for spectrogram generation. Stats: %+v", stats)
		case <-ticker.C:
			if _, err := os.Stat(spectrogramPath); err == nil {
				// File exists, verify it's a valid PNG
				f, err := os.Open(spectrogramPath)
				if err != nil {
					t.Fatalf("Failed to open spectrogram: %v", err)
				}
				defer f.Close()

				// Check PNG magic number (first 8 bytes: 89 50 4E 47 0D 0A 1A 0A)
				hdr := make([]byte, 8)
				if _, err := io.ReadFull(f, hdr); err != nil {
					t.Fatalf("Failed to read PNG header: %v", err)
				}

				pngMagic := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}
				for i, b := range pngMagic {
					if hdr[i] != b {
						t.Fatalf("Invalid PNG magic number at byte %d: got 0x%02X, want 0x%02X", i, hdr[i], b)
					}
				}

				// Verify stats
				stats := pr.GetStats()
				if stats.Completed != 1 {
					t.Errorf("Expected 1 completed job, got %d", stats.Completed)
				}
				if stats.Failed != 0 {
					t.Errorf("Expected 0 failed jobs, got %d", stats.Failed)
				}

				// Log file size
				if fi, err := f.Stat(); err == nil {
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
	// Check if Sox is available
	soxPath, err := exec.LookPath("sox")
	if err != nil {
		t.Skip("Sox binary not found in PATH; skipping integration test")
	}

	// Create temp directory
	tempDir := t.TempDir()

	// Create settings
	settings := &conf.Settings{}
	settings.Realtime.Audio.SoxPath = soxPath
	settings.Realtime.Audio.Export.Path = tempDir
	settings.Realtime.Dashboard.Spectrogram.Enabled = true
	settings.Realtime.Dashboard.Spectrogram.Size = "sm"
	settings.Realtime.Dashboard.Spectrogram.Raw = true

	sfs, err := securefs.New(tempDir)
	if err != nil {
		t.Fatalf("Failed to create SecureFS: %v", err)
	}

	// Create PreRenderer
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pr := NewPreRenderer(ctx, settings, sfs, slog.Default())
	pr.Start()
	defer pr.Stop()

	// Generate synthetic PCM data
	pcmData := make([]byte, testSampleRate*testDuration*2)

	// Generate 440Hz sine wave
	for i := 0; i < testSampleRate*testDuration; i++ {
		sample := int16(float64(testAmplitude) * math.Sin(2*math.Pi*testFrequency*float64(i)/float64(testSampleRate)))
		pcmData[i*2] = byte(sample & 0xFF)
		pcmData[i*2+1] = byte((sample >> 8) & 0xFF)
	}

	// Create output directory
	audioDir := filepath.Join(tempDir, "concurrent")
	if err := os.MkdirAll(audioDir, 0o755); err != nil {
		t.Fatalf("Failed to create audio directory: %v", err)
	}

	// Submit multiple jobs concurrently
	numJobs := 5
	for i := 0; i < numJobs; i++ {
		clipPath := filepath.Join(audioDir, fmt.Sprintf("test-%d.wav", i))
		job := &Job{
			PCMData:   pcmData,
			ClipPath:  clipPath,
			NoteID:    uint(i + 1),
			Timestamp: time.Now(),
		}

		if err := pr.Submit(job); err != nil {
			t.Fatalf("Failed to submit job %d: %v", i, err)
		}
	}

	// Wait for all jobs to complete
	timeout := time.After(testMediumTimeout)
	ticker := time.NewTicker(testPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-timeout:
			stats := pr.GetStats()
			t.Fatalf("Timeout waiting for concurrent jobs. Stats: %+v", stats)
		case <-ticker.C:
			stats := pr.GetStats()
			if stats.Completed+stats.Skipped >= int64(numJobs) {
				// All jobs processed
				if stats.Failed > 0 {
					t.Errorf("Some jobs failed: %d", stats.Failed)
				}

				// Verify all spectrograms exist
				for i := 0; i < numJobs; i++ {
					spectrogramPath := filepath.Join(audioDir, fmt.Sprintf("test-%d.png", i))
					if _, err := os.Stat(spectrogramPath); err != nil {
						t.Errorf("Spectrogram %d not found: %v", i, err)
					}
				}

				t.Logf("Successfully processed %d concurrent jobs. Stats: %+v", numJobs, stats)
				return // Test passed
			}
		}
	}
}

// TestPreRenderer_GracefulShutdownUnderLoad tests shutdown with active jobs
func TestPreRenderer_GracefulShutdownUnderLoad(t *testing.T) {
	// Check if Sox is available
	soxPath, err := exec.LookPath("sox")
	if err != nil {
		t.Skip("Sox binary not found in PATH; skipping integration test")
	}

	// Create temp directory
	tempDir := t.TempDir()

	// Create settings
	settings := &conf.Settings{}
	settings.Realtime.Audio.SoxPath = soxPath
	settings.Realtime.Audio.Export.Path = tempDir
	settings.Realtime.Dashboard.Spectrogram.Enabled = true
	settings.Realtime.Dashboard.Spectrogram.Size = "sm"
	settings.Realtime.Dashboard.Spectrogram.Raw = true

	sfs, err := securefs.New(tempDir)
	if err != nil {
		t.Fatalf("Failed to create SecureFS: %v", err)
	}

	// Create PreRenderer
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pr := NewPreRenderer(ctx, settings, sfs, slog.Default())
	pr.Start()

	// Generate synthetic PCM data (no sine wave needed for shutdown test)
	pcmData := make([]byte, testSampleRate*testDuration*2)

	// Create output directory
	audioDir := filepath.Join(tempDir, "shutdown")
	if err := os.MkdirAll(audioDir, 0o755); err != nil {
		t.Fatalf("Failed to create audio directory: %v", err)
	}

	// Submit several jobs
	numJobs := 10
	for i := 0; i < numJobs; i++ {
		clipPath := filepath.Join(audioDir, fmt.Sprintf("test-%d.wav", i))
		job := &Job{
			PCMData:   pcmData,
			ClipPath:  clipPath,
			NoteID:    uint(i + 1),
			Timestamp: time.Now(),
		}

		_ = pr.Submit(job)
	}

	// Immediately trigger shutdown (jobs may still be in queue)
	pr.Stop()

	// Verify clean shutdown (no panics, no goroutine leaks)
	// This is verified by the test completing without hanging

	stats := pr.GetStats()
	t.Logf("Shutdown completed. Stats: queued=%d, completed=%d, failed=%d, skipped=%d",
		stats.Queued, stats.Completed, stats.Failed, stats.Skipped)

	// Some jobs may not have completed due to shutdown, but that's expected
	if stats.Queued > 0 && stats.Completed+stats.Failed+stats.Skipped == 0 {
		t.Error("No jobs were processed before shutdown")
	}
}

// TestPreRenderer_QueueOverflow tests behavior when submitting more jobs than queue size
func TestPreRenderer_QueueOverflow(t *testing.T) {
	// Check if Sox is available
	soxPath, err := exec.LookPath("sox")
	if err != nil {
		t.Skip("Sox binary not found in PATH; skipping integration test")
	}

	// Create temp directory
	tempDir := t.TempDir()

	// Create settings
	settings := &conf.Settings{}
	settings.Realtime.Audio.SoxPath = soxPath
	settings.Realtime.Audio.Export.Path = tempDir
	settings.Realtime.Dashboard.Spectrogram.Enabled = true
	settings.Realtime.Dashboard.Spectrogram.Size = "sm"
	settings.Realtime.Dashboard.Spectrogram.Raw = true

	sfs, err := securefs.New(tempDir)
	if err != nil {
		t.Fatalf("Failed to create SecureFS: %v", err)
	}

	// Create PreRenderer (queue size is 3)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pr := NewPreRenderer(ctx, settings, sfs, slog.Default())
	pr.Start()
	defer pr.Stop()

	// Generate synthetic PCM data (minimal size for fast processing)
	pcmData := make([]byte, testSampleRate*testDuration*2)

	// Create output directory
	audioDir := filepath.Join(tempDir, "overflow")
	if err := os.MkdirAll(audioDir, 0o755); err != nil {
		t.Fatalf("Failed to create audio directory: %v", err)
	}

	// Submit more jobs than queue size to trigger overflow
	// Queue size is 3 (2 workers + 1 waiting), submit 20 jobs rapidly
	numJobs := 20
	queueFull := 0
	submitted := 0

	for i := 0; i < numJobs; i++ {
		clipPath := filepath.Join(audioDir, fmt.Sprintf("test-%03d.wav", i))
		job := &Job{
			PCMData:   pcmData,
			ClipPath:  clipPath,
			NoteID:    uint(i + 1),
			Timestamp: time.Now(),
		}

		if err := pr.Submit(job); err != nil {
			if errors.Is(err, ErrQueueFull) {
				queueFull++
			} else {
				t.Errorf("Unexpected error submitting job %d: %v", i, err)
			}
		} else {
			submitted++
		}
	}

	// Verify that some jobs were rejected due to queue overflow
	if queueFull == 0 {
		t.Error("Expected some jobs to be rejected due to queue overflow, but none were")
	}

	t.Logf("Submitted %d jobs, %d rejected due to queue overflow", submitted, queueFull)

	// Wait for workers to drain the queue
	timeout := time.After(testLongTimeout)
	ticker := time.NewTicker(testPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-timeout:
			stats := pr.GetStats()
			t.Fatalf("Timeout waiting for queue to drain. Stats: %+v", stats)
		case <-ticker.C:
			stats := pr.GetStats()
			// Queue is drained when all submitted jobs are processed
			if stats.Completed+stats.Failed+stats.Skipped >= int64(submitted) {
				t.Logf("Queue drained. Final stats: queued=%d, completed=%d, failed=%d, skipped=%d",
					stats.Queued, stats.Completed, stats.Failed, stats.Skipped)

				// Verify stats consistency
				if stats.Queued != int64(submitted) {
					t.Errorf("Expected queued count to be %d, got %d", submitted, stats.Queued)
				}

				return // Test passed
			}
		}
	}
}
