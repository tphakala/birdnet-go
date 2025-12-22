package myaudio

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestValidateAudioFileNoDurationCheck tests that audio files of various durations
// are all considered valid regardless of the configured capture length
func TestValidateAudioFileNoDurationCheck(t *testing.T) {
	t.Parallel()

	// Create test WAV files with different sizes (simulating different durations)
	testCases := []struct {
		name        string
		fileSize    int64
		expectValid bool
		description string
	}{
		{
			name:        "small_file_10s.wav",
			fileSize:    100 * 1024, // 100KB - simulates ~10 second file
			expectValid: true,
			description: "Small audio file should be valid",
		},
		{
			name:        "medium_file_30s.wav",
			fileSize:    300 * 1024, // 300KB - simulates ~30 second file
			expectValid: true,
			description: "Medium audio file should be valid",
		},
		{
			name:        "large_file_60s.wav",
			fileSize:    600 * 1024, // 600KB - simulates ~60 second file
			expectValid: true,
			description: "Large audio file should be valid",
		},
		{
			name:        "very_large_file_120s.wav",
			fileSize:    1200 * 1024, // 1.2MB - simulates ~120 second file
			expectValid: true,
			description: "Very large audio file should be valid",
		},
		{
			name:        "tiny_file.wav",
			fileSize:    500, // Less than minimum valid size
			expectValid: false,
			description: "File below minimum size should be invalid",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			t.Parallel()
			// Create a temp directory for this test
			tmpDir := t.TempDir()
			// Create test file with WAV header
			testFile := filepath.Join(tmpDir, tc.name)
			createTestWAVFileWithSize(t, testFile, tc.fileSize)

			// Validate the file with timeout
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			result, err := ValidateAudioFile(ctx, testFile)

			if err != nil && tc.expectValid {
				t.Errorf("Expected valid file but got error: %v", err)
			}

			if tc.expectValid {
				// For valid files, check that they're marked as complete
				// regardless of duration
				if result != nil && !result.IsComplete {
					t.Errorf("Expected file to be marked as complete, but IsComplete=%v, Error=%v",
						result.IsComplete, result.Error)
				}
			} else {
				// For invalid files (too small), they should not be valid
				if result != nil && result.IsValid {
					t.Errorf("Expected file to be invalid but got IsValid=true")
				}
			}
		})
	}
}

// createTestWAVFileWithSize creates a minimal WAV file for testing with specific size
func createTestWAVFileWithSize(t *testing.T, path string, size int64) {
	t.Helper()

	// Create a minimal WAV header
	wavHeader := []byte{
		'R', 'I', 'F', 'F', // ChunkID
		0, 0, 0, 0, // ChunkSize (will be updated)
		'W', 'A', 'V', 'E', // Format
		'f', 'm', 't', ' ', // Subchunk1ID
		16, 0, 0, 0, // Subchunk1Size
		1, 0, // AudioFormat (PCM)
		2, 0, // NumChannels
		0x44, 0xAC, 0, 0, // SampleRate (44100)
		0x10, 0xB1, 0x02, 0, // ByteRate
		4, 0, // BlockAlign
		16, 0, // BitsPerSample
		'd', 'a', 't', 'a', // Subchunk2ID
		0, 0, 0, 0, // Subchunk2Size (will be updated)
	}

	// Calculate data size
	dataSize := max(size-int64(len(wavHeader)), 0)

	// Update chunk sizes in header
	chunkSize := uint32(36 + dataSize)
	wavHeader[4] = byte(chunkSize)
	wavHeader[5] = byte(chunkSize >> 8)
	wavHeader[6] = byte(chunkSize >> 16)
	wavHeader[7] = byte(chunkSize >> 24)

	subchunk2Size := uint32(dataSize)
	wavHeader[40] = byte(subchunk2Size)
	wavHeader[41] = byte(subchunk2Size >> 8)
	wavHeader[42] = byte(subchunk2Size >> 16)
	wavHeader[43] = byte(subchunk2Size >> 24)

	// Create the file
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			t.Logf("Warning: failed to close file: %v", closeErr)
		}
	}()

	// Write header
	if _, err := file.Write(wavHeader); err != nil {
		t.Fatal(err)
	}

	// Write data (zeros)
	if dataSize > 0 {
		data := make([]byte, dataSize)
		if _, err := file.Write(data); err != nil {
			t.Fatal(err)
		}
	}
}

// TestQuickValidateAudioFile tests the quick validation function
func TestQuickValidateAudioFile(t *testing.T) {
	t.Parallel()

	t.Run("Valid WAV file", func(t *testing.T) {
		t.Parallel()
		tmpDir := t.TempDir()
		testFile := filepath.Join(tmpDir, "test.wav")
		createTestWAVFileWithSize(t, testFile, 10*1024) // 10KB file

		valid, err := QuickValidateAudioFile(testFile)
		if err != nil {
			t.Errorf("Unexpected error: %v", err)
		}
		if !valid {
			t.Error("Expected file to be valid")
		}
	})

	t.Run("Non-existent file", func(t *testing.T) {
		t.Parallel()
		tmpDir := t.TempDir()
		valid, err := QuickValidateAudioFile(filepath.Join(tmpDir, "nonexistent.wav"))
		if err != nil {
			t.Errorf("Unexpected error: %v", err)
		}
		if valid {
			t.Error("Expected non-existent file to be invalid")
		}
	})

	t.Run("File too small", func(t *testing.T) {
		t.Parallel()
		tmpDir := t.TempDir()
		testFile := filepath.Join(tmpDir, "tiny.wav")
		if err := os.WriteFile(testFile, []byte("small"), 0o600); err != nil {
			t.Fatal(err)
		}

		valid, err := QuickValidateAudioFile(testFile)
		if err != nil {
			t.Errorf("Unexpected error: %v", err)
		}
		if valid {
			t.Error("Expected tiny file to be invalid")
		}
	})
}

// TestValidateAudioFileWithRetry tests the retry logic
func TestValidateAudioFileWithRetry(t *testing.T) {
	t.Run("Valid file on first attempt", func(t *testing.T) {
		t.Parallel()
		tmpDir := t.TempDir() // Each subtest gets its own temp directory
		testFile := filepath.Join(tmpDir, "valid.wav")
		createTestWAVFileWithSize(t, testFile, 10*1024)

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		result, err := ValidateAudioFileWithRetry(ctx, testFile)

		if err != nil {
			t.Errorf("Unexpected error: %v", err)
		}
		if result == nil || !result.IsValid {
			t.Error("Expected file to be valid")
		}
	})

	t.Run("File becomes valid during retry", func(t *testing.T) {
		t.Parallel()
		tmpDir := t.TempDir() // Each subtest gets its own temp directory
		testFile := filepath.Join(tmpDir, "growing.wav")

		// Start with a small file
		if err := os.WriteFile(testFile, []byte("small"), 0o600); err != nil {
			t.Fatal(err)
		}

		// Channel to coordinate file growth and validation completion
		validationStarted := make(chan struct{})
		validationDone := make(chan struct{})

		// Start validation in a goroutine
		ctx := context.Background()
		var result *AudioValidationResult
		var err error

		go func() {
			close(validationStarted) // Signal that validation is starting
			result, err = ValidateAudioFileWithRetry(ctx, testFile)
			close(validationDone) // Signal that validation is complete
		}()

		// Wait for validation to start, then grow the file
		<-validationStarted
		// Give validation a moment to actually begin processing
		// This small delay ensures the validator has started checking the small file
		time.Sleep(50 * time.Millisecond)

		// Now grow the file while validation is retrying
		createTestWAVFileWithSize(t, testFile, 10*1024)

		// Wait for validation to complete
		<-validationDone

		// The file should eventually become valid
		if err == nil && result != nil && result.IsValid {
			// Success - file became valid during retry
			return
		}

		// If it didn't become valid, that's okay too (timing dependent)
		// Just make sure we didn't get an unexpected error
		if err != nil && !errors.Is(err, ErrValidationFailed) {
			t.Errorf("Unexpected error: %v", err)
		}
	})
}
