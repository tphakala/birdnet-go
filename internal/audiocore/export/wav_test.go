package export

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tphakala/birdnet-go/internal/audiocore"
)

func TestWAVExporter_ExportToWriter(t *testing.T) {
	exporter := NewWAVExporter()

	// Create test PCM data (1 second of 440Hz sine wave)
	sampleRate := 48000
	duration := 1.0
	samples := int(float64(sampleRate) * duration)
	pcmData := make([]byte, samples*2) // 16-bit samples

	// Generate simple test pattern (not a real sine wave, just test data)
	for i := 0; i < samples; i++ {
		value := int16((i % 100) * 327) // Simple pattern
		pcmData[i*2] = byte(value)
		pcmData[i*2+1] = byte(value >> 8)
	}

	audioData := &audiocore.AudioData{
		Buffer: pcmData,
		Format: audiocore.AudioFormat{
			SampleRate: sampleRate,
			Channels:   1,
			BitDepth:   16,
			Encoding:   "pcm_s16le",
		},
		Timestamp: time.Now(),
		Duration:  time.Duration(duration * float64(time.Second)),
		SourceID:  "test",
	}

	config := &Config{
		Format: FormatWAV,
	}

	// Export to buffer
	var buf bytes.Buffer
	err := exporter.ExportToWriter(context.Background(), audioData, &buf, config)
	if err != nil {
		t.Fatalf("ExportToWriter failed: %v", err)
	}

	// Verify WAV header
	wavData := buf.Bytes()
	if len(wavData) < 44 {
		t.Fatalf("WAV data too short: %d bytes", len(wavData))
	}

	// Check RIFF header
	if string(wavData[0:4]) != "RIFF" {
		t.Errorf("Invalid RIFF header: %s", string(wavData[0:4]))
	}

	// Check WAVE format
	if string(wavData[8:12]) != "WAVE" {
		t.Errorf("Invalid WAVE format: %s", string(wavData[8:12]))
	}

	// Check fmt chunk
	if string(wavData[12:16]) != "fmt " {
		t.Errorf("Invalid fmt chunk: %s", string(wavData[12:16]))
	}

	// Check data chunk
	if string(wavData[36:40]) != "data" {
		t.Errorf("Invalid data chunk: %s", string(wavData[36:40]))
	}

	// Verify data size
	expectedDataSize := len(pcmData)
	actualDataSize := len(wavData) - 44
	if actualDataSize != expectedDataSize {
		t.Errorf("Data size mismatch: expected %d, got %d", expectedDataSize, actualDataSize)
	}
}

func TestWAVExporter_ExportToFile(t *testing.T) {
	// Create temp directory
	tempDir, err := os.MkdirTemp("", "wav_export_test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	exporter := NewWAVExporter()

	// Create test audio data
	audioData := &audiocore.AudioData{
		Buffer: []byte{0, 1, 2, 3, 4, 5, 6, 7}, // Minimal test data
		Format: audiocore.AudioFormat{
			SampleRate: 48000,
			Channels:   1,
			BitDepth:   16,
			Encoding:   "pcm_s16le",
		},
		Timestamp: time.Now(),
		Duration:  time.Millisecond * 100,
		SourceID:  "test_source",
	}

	config := &Config{
		Format:           FormatWAV,
		OutputPath:       tempDir,
		FileNameTemplate: "{source}_test",
		Timeout:          5 * time.Second,
	}

	// Export to file
	filePath, err := exporter.ExportToFile(context.Background(), audioData, config)
	if err != nil {
		t.Fatalf("ExportToFile failed: %v", err)
	}

	// Verify file exists
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		t.Errorf("Expected file not created: %s", filePath)
	}

	// Verify file name
	expectedName := "test_source_test.wav"
	if filepath.Base(filePath) != expectedName {
		t.Errorf("Unexpected file name: got %s, want %s", filepath.Base(filePath), expectedName)
	}

	// Clean up
	_ = os.Remove(filePath)
}

func TestWAVExporter_ValidateConfig(t *testing.T) {
	exporter := NewWAVExporter()

	tests := []struct {
		name      string
		config    *Config
		wantError bool
	}{
		{
			name:      "nil config",
			config:    nil,
			wantError: true,
		},
		{
			name: "wrong format",
			config: &Config{
				Format: FormatMP3,
			},
			wantError: true,
		},
		{
			name: "valid config",
			config: &Config{
				Format:           FormatWAV,
				OutputPath:       "test/",
				FileNameTemplate: "test",
				Timeout:          10 * time.Second,
			},
			wantError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := exporter.ValidateConfig(tt.config)
			if (err != nil) != tt.wantError {
				t.Errorf("ValidateConfig() error = %v, wantError %v", err, tt.wantError)
			}
		})
	}
}

func TestWAVExporter_ContextCancellation(t *testing.T) {
	exporter := NewWAVExporter()

	audioData := &audiocore.AudioData{
		Buffer: make([]byte, 1000),
		Format: audiocore.AudioFormat{
			SampleRate: 48000,
			Channels:   1,
			BitDepth:   16,
		},
	}

	config := &Config{
		Format: FormatWAV,
	}

	// Create cancelled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var buf bytes.Buffer
	err := exporter.ExportToWriter(ctx, audioData, &buf, config)
	if err == nil {
		t.Error("Expected error for cancelled context")
	}
}

func TestWAVExporter_SupportedFormats(t *testing.T) {
	exporter := NewWAVExporter()
	formats := exporter.SupportedFormats()

	if len(formats) != 1 {
		t.Errorf("Expected 1 supported format, got %d", len(formats))
	}

	if formats[0] != FormatWAV {
		t.Errorf("Expected WAV format, got %s", formats[0])
	}
}