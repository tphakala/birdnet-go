package myaudio

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestGetAudioDuration(t *testing.T) {
	// Check if ffprobe is available
	if !isFFprobeAvailable() {
		t.Skip("ffprobe not available, skipping test")
	}

	// Create a test WAV file with known duration (1 second of silence)
	testFile := t.TempDir() + "/test.wav"
	if err := createTestWAVFile(testFile, 1.0); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}
	defer func() {
		if err := os.Remove(testFile); err != nil {
			t.Logf("Failed to remove test file: %v", err)
		}
	}()

	tests := []struct {
		name         string
		audioPath    string
		wantDuration float64
		wantErr      bool
		tolerance    float64
	}{
		{
			name:         "valid WAV file",
			audioPath:    testFile,
			wantDuration: 1.0,
			wantErr:      false,
			tolerance:    0.1,
		},
		{
			name:      "non-existent file",
			audioPath: "/non/existent/file.wav",
			wantErr:   true,
		},
		{
			name:      "empty path",
			audioPath: "",
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			duration, err := GetAudioDuration(ctx, tt.audioPath)
			if (err != nil) != tt.wantErr {
				t.Errorf("GetAudioDuration() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr {
				if duration < tt.wantDuration-tt.tolerance || duration > tt.wantDuration+tt.tolerance {
					t.Errorf("GetAudioDuration() = %v, want %v (±%v)", duration, tt.wantDuration, tt.tolerance)
				}
			}
		})
	}
}

func TestGetAudioDurationTimeout(t *testing.T) {
	// Check if ffprobe is available
	if !isFFprobeAvailable() {
		t.Skip("ffprobe not available, skipping test")
	}

	// Test with very short timeout to trigger timeout
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Nanosecond)
	defer cancel()

	// Sleep to ensure context expires
	time.Sleep(10 * time.Millisecond)

	_, err := GetAudioDuration(ctx, "/dev/zero")
	if err == nil {
		t.Error("Expected timeout error, got nil")
	}
}

// Helper function to check if ffprobe is available
func isFFprobeAvailable() bool {
	// Try to run ffprobe -version
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err := GetAudioDuration(ctx, "/dev/null")
	// If ffprobe is not found, the error will contain "executable file not found"
	// We consider it available if we get any other error (like invalid file)
	return err != nil && !os.IsNotExist(err)
}

// Helper function to create a test WAV file with specified duration
func createTestWAVFile(path string, durationSec float64) error {
	// WAV header for 1 second of silence at 44100Hz, 16-bit, mono
	sampleRate := 44100
	numSamples := int(float64(sampleRate) * durationSec)
	dataSize := numSamples * 2 // 16-bit = 2 bytes per sample

	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() {
		if err := file.Close(); err != nil {
			// Log error but don't fail - this is a test helper
			_ = err
		}
	}()

	// Write WAV header
	header := []byte{
		'R', 'I', 'F', 'F',
		byte(dataSize + 36), byte((dataSize + 36) >> 8), byte((dataSize + 36) >> 16), byte((dataSize + 36) >> 24),
		'W', 'A', 'V', 'E',
		'f', 'm', 't', ' ',
		16, 0, 0, 0, // Subchunk1Size
		1, 0, // AudioFormat (PCM)
		1, 0, // NumChannels (mono)
		byte(sampleRate), byte(sampleRate >> 8), byte(sampleRate >> 16), byte(sampleRate >> 24), // SampleRate
		byte(sampleRate * 2), byte((sampleRate * 2) >> 8), byte((sampleRate * 2) >> 16), byte((sampleRate * 2) >> 24), // ByteRate
		2, 0, // BlockAlign
		16, 0, // BitsPerSample
		'd', 'a', 't', 'a',
		byte(dataSize), byte(dataSize >> 8), byte(dataSize >> 16), byte(dataSize >> 24),
	}

	if _, err := file.Write(header); err != nil {
		return err
	}

	// Write silence (zeros)
	silence := make([]byte, dataSize)
	_, err = file.Write(silence)
	return err
}
