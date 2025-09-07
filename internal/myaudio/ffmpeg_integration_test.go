package myaudio

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestGetAudioDurationIntegration(t *testing.T) {
	// Skip if ffprobe is not available
	if !isFFprobeAvailable() {
		t.Skip("ffprobe not available, skipping integration test")
	}

	// Look for a real audio file in clips directory
	clipsDir := filepath.Join("..", "..", "clips")
	var testFile string

	// Try to find any audio file
	err := filepath.Walk(clipsDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			// Return the error to stop walking, not nil
			return err
		}
		ext := filepath.Ext(path)
		if ext == ".m4a" || ext == ".mp3" || ext == ".wav" || ext == ".flac" {
			testFile = path
			return filepath.SkipAll // Stop walking once we find a file
		}
		return nil
	})

	// Check if walk failed (but ignore SkipAll which is expected)
	if err != nil && !errors.Is(err, filepath.SkipAll) {
		t.Logf("Warning: Error walking clips directory: %v", err)
	}

	if testFile == "" {
		t.Skip("No audio files found in clips directory, skipping integration test")
	}

	t.Logf("Testing with file: %s", testFile)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	start := time.Now()
	duration, err := GetAudioDuration(ctx, testFile)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("GetAudioDuration() failed: %v", err)
	}

	t.Logf("Duration: %.2f seconds", duration)
	t.Logf("Time taken: %v", elapsed)

	// Basic sanity checks
	if duration <= 0 {
		t.Errorf("Duration should be positive, got %f", duration)
	}

	if duration > 3600 { // More than 1 hour seems unlikely for test clips
		t.Errorf("Duration seems unreasonably long: %f seconds", duration)
	}

	// Performance check - should be fast
	if elapsed > 100*time.Millisecond {
		t.Logf("Warning: GetAudioDuration took longer than expected: %v", elapsed)
	}
}
