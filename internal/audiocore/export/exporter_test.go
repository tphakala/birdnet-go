package export

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/tphakala/birdnet-go/internal/audiocore"
)

func TestManager_Export(t *testing.T) {
	// Create temp directory
	tempDir, err := os.MkdirTemp("", "export_manager_test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	// Create manager with WAV exporter
	manager := NewManager()
	manager.RegisterExporter(FormatWAV, NewWAVExporter())

	// Test audio data
	audioData := &audiocore.AudioData{
		Buffer: []byte{0, 1, 2, 3, 4, 5, 6, 7},
		Format: audiocore.AudioFormat{
			SampleRate: 48000,
			Channels:   1,
			BitDepth:   16,
			Encoding:   "pcm_s16le",
		},
		Timestamp: time.Now(),
		Duration:  100 * time.Millisecond,
		SourceID:  "test_source",
	}

	config := &Config{
		Format:           FormatWAV,
		OutputPath:       tempDir,
		FileNameTemplate: "{source}_export",
		Timeout:          5 * time.Second,
	}

	// Test successful export
	result, err := manager.Export(context.Background(), audioData, config)
	if err != nil {
		t.Fatalf("Export failed: %v", err)
	}

	if !result.Success {
		t.Error("Export result indicates failure")
	}

	if result.FilePath == "" {
		t.Error("Export result missing file path")
	}

	if result.Metadata == nil {
		t.Error("Export result missing metadata")
	}

	// Verify file exists
	if _, err := os.Stat(result.FilePath); os.IsNotExist(err) {
		t.Errorf("Expected file not created: %s", result.FilePath)
	}

	// Clean up
	_ = os.Remove(result.FilePath)
}

func TestManager_UnsupportedFormat(t *testing.T) {
	manager := NewManager()
	// Don't register any exporters

	audioData := &audiocore.AudioData{
		Buffer: []byte{0, 1, 2, 3},
		Format: audiocore.AudioFormat{
			SampleRate: 48000,
			Channels:   1,
			BitDepth:   16,
		},
		SourceID: "test",
	}

	config := &Config{
		Format:           FormatMP3,
		OutputPath:       "test/",
		FileNameTemplate: "test",
		FFmpegPath:       "/usr/bin/ffmpeg",
		Timeout:          5 * time.Second,
	}

	result, err := manager.Export(context.Background(), audioData, config)
	if err == nil {
		t.Error("Expected error for unsupported format")
	}

	if result.Success {
		t.Error("Expected failure for unsupported format")
	}

	if result.Error == nil {
		t.Error("Expected error in result for unsupported format")
	}
}

func TestManager_IsFormatSupported(t *testing.T) {
	manager := NewManager()
	manager.RegisterExporter(FormatWAV, NewWAVExporter())

	if !manager.IsFormatSupported(FormatWAV) {
		t.Error("Expected WAV to be supported")
	}

	if manager.IsFormatSupported(FormatMP3) {
		t.Error("Expected MP3 to not be supported")
	}
}

func TestManager_SupportedFormats(t *testing.T) {
	manager := NewManager()
	manager.RegisterExporter(FormatWAV, NewWAVExporter())
	manager.RegisterExporter(FormatMP3, NewFFmpegExporter(FormatMP3))

	formats := manager.SupportedFormats()
	if len(formats) != 2 {
		t.Errorf("Expected 2 supported formats, got %d", len(formats))
	}

	// Check that both formats are present
	hasWAV := false
	hasMP3 := false
	for _, f := range formats {
		if f == FormatWAV {
			hasWAV = true
		}
		if f == FormatMP3 {
			hasMP3 = true
		}
	}

	if !hasWAV {
		t.Error("Expected WAV in supported formats")
	}
	if !hasMP3 {
		t.Error("Expected MP3 in supported formats")
	}
}

func TestDefaultManager(t *testing.T) {
	// Test with FFmpeg path
	manager := DefaultManager("/usr/bin/ffmpeg")
	
	// Should always support WAV
	if !manager.IsFormatSupported(FormatWAV) {
		t.Error("Expected WAV to be supported")
	}

	// Should support other formats when FFmpeg is provided
	if !manager.IsFormatSupported(FormatMP3) {
		t.Error("Expected MP3 to be supported with FFmpeg")
	}
	if !manager.IsFormatSupported(FormatFLAC) {
		t.Error("Expected FLAC to be supported with FFmpeg")
	}

	// Test without FFmpeg
	managerNoFFmpeg := DefaultManager("")
	
	// Should still support WAV
	if !managerNoFFmpeg.IsFormatSupported(FormatWAV) {
		t.Error("Expected WAV to be supported without FFmpeg")
	}

	// Should not support other formats
	if managerNoFFmpeg.IsFormatSupported(FormatMP3) {
		t.Error("Expected MP3 to not be supported without FFmpeg")
	}
}

func TestManager_ExportClip(t *testing.T) {
	// Create temp directory
	tempDir, err := os.MkdirTemp("", "export_clip_test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	manager := NewManager()
	manager.RegisterExporter(FormatWAV, NewWAVExporter())

	audioData := &audiocore.AudioData{
		Buffer: []byte{0, 1, 2, 3, 4, 5, 6, 7},
		Format: audiocore.AudioFormat{
			SampleRate: 48000,
			Channels:   1,
			BitDepth:   16,
		},
		Timestamp: time.Now(),
		Duration:  3 * time.Second,
		SourceID:  "test_source",
	}

	config := &Config{
		Format:           FormatWAV,
		OutputPath:       tempDir,
		FileNameTemplate: "{source}_clip",
		Timeout:          5 * time.Second,
	}

	startTime := audioData.Timestamp
	endTime := startTime.Add(1 * time.Second)

	result, err := manager.ExportClip(context.Background(), audioData, startTime, endTime, config)
	if err != nil {
		t.Fatalf("ExportClip failed: %v", err)
	}

	if !result.Success {
		t.Error("ExportClip result indicates failure")
	}

	// Verify metadata reflects clip timing
	if result.Metadata.Duration != 1*time.Second {
		t.Errorf("Expected clip duration 1s, got %v", result.Metadata.Duration)
	}

	// Clean up
	if result.FilePath != "" {
		_ = os.Remove(result.FilePath)
	}
}

func BenchmarkManager_Export_WAV(b *testing.B) {
	// Create temp directory
	tempDir, err := os.MkdirTemp("", "export_benchmark")
	if err != nil {
		b.Fatalf("Failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	manager := DefaultManager("")

	// Create 1 second of audio data
	audioData := &audiocore.AudioData{
		Buffer: make([]byte, 48000*2), // 1 second at 48kHz, 16-bit
		Format: audiocore.AudioFormat{
			SampleRate: 48000,
			Channels:   1,
			BitDepth:   16,
		},
		Timestamp: time.Now(),
		Duration:  1 * time.Second,
		SourceID:  "benchmark",
	}

	config := &Config{
		Format:           FormatWAV,
		OutputPath:       tempDir,
		FileNameTemplate: "benchmark_{timestamp}",
		Timeout:          10 * time.Second,
	}

	b.ResetTimer()
	b.ReportAllocs()

	for b.Loop() {
		result, err := manager.Export(context.Background(), audioData, config)
		if err != nil {
			b.Fatalf("Export failed: %v", err)
		}
		// Clean up file
		_ = os.Remove(result.FilePath)
	}
}