package capture

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/tphakala/birdnet-go/internal/audiocore"
	"github.com/tphakala/birdnet-go/internal/audiocore/export"
)

func TestExportManager_EnableDisableCapture(t *testing.T) {
	// Setup
	bufferPool := audiocore.NewBufferPool(audiocore.BufferPoolConfig{
		SmallBufferSize:   4096,
		MediumBufferSize:  65536,
		LargeBufferSize:   1048576,
		MaxBuffersPerSize: 10,
	})

	exportManager := export.NewManager()
	exportManager.RegisterExporter(export.FormatWAV, export.NewWAVExporter())

	manager := NewManager(bufferPool, exportManager)

	// Test enable capture
	config := Config{
		Duration: 10 * time.Second,
		Format: audiocore.AudioFormat{
			SampleRate: 48000,
			Channels:   1,
			BitDepth:   16,
		},
		PreBuffer:  2 * time.Second,
		PostBuffer: 3 * time.Second,
	}

	err := manager.EnableCapture("test_source", config)
	if err != nil {
		t.Fatalf("EnableCapture failed: %v", err)
	}

	// Check if enabled
	if !manager.IsCaptureEnabled("test_source") {
		t.Error("Expected capture to be enabled")
	}

	// Test disable
	err = manager.DisableCapture("test_source")
	if err != nil {
		t.Fatalf("DisableCapture failed: %v", err)
	}

	// Check if disabled
	if manager.IsCaptureEnabled("test_source") {
		t.Error("Expected capture to be disabled")
	}
}

func TestExportManager_WriteAndSaveClip(t *testing.T) {
	// Setup
	bufferPool := audiocore.NewBufferPool(audiocore.BufferPoolConfig{
		SmallBufferSize:   4096,
		MediumBufferSize:  65536,
		LargeBufferSize:   1048576,
		MaxBuffersPerSize: 10,
	})

	exportManager := export.NewManager()
	manager := NewManager(bufferPool, exportManager)

	// Enable capture
	config := Config{
		Duration: 5 * time.Second,
		Format: audiocore.AudioFormat{
			SampleRate: 48000,
			Channels:   1,
			BitDepth:   16,
		},
		PreBuffer:  1 * time.Second,
		PostBuffer: 1 * time.Second,
	}

	sourceID := "test_source"
	err := manager.EnableCapture(sourceID, config)
	if err != nil {
		t.Fatalf("EnableCapture failed: %v", err)
	}

	// Write test data
	testData := make([]byte, 48000*2) // 1 second of audio
	for i := range testData {
		testData[i] = byte(i % 256)
	}

	audioData := &audiocore.AudioData{
		Buffer:    testData,
		Format:    config.Format,
		Timestamp: time.Now(),
		Duration:  1 * time.Second,
		SourceID:  sourceID,
	}

	err = manager.Write(sourceID, audioData)
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	// Save clip
	triggerTime := time.Now()
	clip, err := manager.SaveClip(sourceID, triggerTime, 500*time.Millisecond)
	if err != nil {
		t.Fatalf("SaveClip failed: %v", err)
	}

	if clip == nil {
		t.Fatal("Expected non-nil clip")
	}

	// Check clip duration (should include pre/post buffers)
	expectedDuration := 500*time.Millisecond + config.PreBuffer + config.PostBuffer
	if clip.Duration != expectedDuration {
		t.Errorf("Expected duration %v, got %v", expectedDuration, clip.Duration)
	}
}

func TestExportManager_ExportClip(t *testing.T) {
	// Create temp directory
	tempDir, err := os.MkdirTemp("", "export_manager_test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	// Setup
	bufferPool := audiocore.NewBufferPool(audiocore.BufferPoolConfig{
		SmallBufferSize:   4096,
		MediumBufferSize:  65536,
		LargeBufferSize:   1048576,
		MaxBuffersPerSize: 10,
	})

	exportMgr := export.NewManager()
	exportMgr.RegisterExporter(export.FormatWAV, export.NewWAVExporter())

	manager := NewManager(bufferPool, exportMgr)

	// Enable capture with export config
	config := Config{
		Duration: 5 * time.Second,
		Format: audiocore.AudioFormat{
			SampleRate: 48000,
			Channels:   1,
			BitDepth:   16,
		},
		PreBuffer:  1 * time.Second,
		PostBuffer: 1 * time.Second,
		ExportConfig: &export.Config{
			Format:           export.FormatWAV,
			OutputPath:       tempDir,
			FileNameTemplate: "test_export",
			Timeout:          5 * time.Second,
		},
	}

	sourceID := "test_source"
	err = manager.EnableCapture(sourceID, config)
	if err != nil {
		t.Fatalf("EnableCapture failed: %v", err)
	}

	// Write some test data
	testData := make([]byte, 48000*4) // 2 seconds of audio
	audioData := &audiocore.AudioData{
		Buffer:    testData,
		Format:    config.Format,
		Timestamp: time.Now(),
		Duration:  2 * time.Second,
		SourceID:  sourceID,
	}

	err = manager.Write(sourceID, audioData)
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	// Export clip
	ctx := context.Background()
	result, err := manager.ExportClip(ctx, sourceID, time.Now(), 500*time.Millisecond)
	if err != nil {
		t.Fatalf("ExportClip failed: %v", err)
	}

	if !result.Success {
		t.Error("Export result indicates failure")
	}

	// Verify file exists
	if _, err := os.Stat(result.FilePath); os.IsNotExist(err) {
		t.Errorf("Expected file not created: %s", result.FilePath)
	}
}