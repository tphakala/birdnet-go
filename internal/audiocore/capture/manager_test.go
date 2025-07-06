package capture

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/tphakala/birdnet-go/internal/audiocore"
	"github.com/tphakala/birdnet-go/internal/audiocore/export"
)

func TestNewManager(t *testing.T) {
	bufferPool := audiocore.NewBufferPool(audiocore.BufferPoolConfig{
		SmallBufferSize:   4096,
		MediumBufferSize:  65536,
		LargeBufferSize:   1048576,
		MaxBuffersPerSize: 10,
	})

	exportManager := export.NewManager()
	exportManager.RegisterExporter(export.FormatWAV, export.NewWAVExporter())

	manager := NewManager(bufferPool, exportManager)
	if manager == nil {
		t.Fatal("NewManager returned nil")
	}

	// Type assertion to access implementation
	impl, ok := manager.(*ManagerImpl)
	if !ok {
		t.Fatal("Manager is not *ManagerImpl")
	}

	if impl.bufferPool == nil {
		t.Error("Buffer pool not set")
	}

	if impl.exportManager == nil {
		t.Error("Export manager not set")
	}

	if impl.logger == nil {
		t.Error("Logger not set")
	}
}

func TestManager_EnableDisableCapture(t *testing.T) {
	manager := createTestManager(t)

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

	// Test enable capture
	err := manager.EnableCapture("source1", config)
	if err != nil {
		t.Errorf("EnableCapture failed: %v", err)
	}

	// Verify capture is enabled
	if !manager.IsCaptureEnabled("source1") {
		t.Error("Capture should be enabled for source1")
	}

	// Test enabling again (should fail)
	err = manager.EnableCapture("source1", config)
	if err == nil {
		t.Error("Expected error when enabling capture twice")
	}

	// Test disable capture
	err = manager.DisableCapture("source1")
	if err != nil {
		t.Errorf("DisableCapture failed: %v", err)
	}

	// Verify capture is disabled
	if manager.IsCaptureEnabled("source1") {
		t.Error("Capture should be disabled for source1")
	}

	// Test disabling non-existent capture
	err = manager.DisableCapture("nonexistent")
	if err == nil {
		t.Error("Expected error when disabling non-existent capture")
	}
}

func TestManager_Write(t *testing.T) {
	manager := createTestManager(t)

	config := Config{
		Duration: 5 * time.Second,
		Format: audiocore.AudioFormat{
			SampleRate: 48000,
			Channels:   1,
			BitDepth:   16,
		},
	}

	// Enable capture
	err := manager.EnableCapture("source1", config)
	if err != nil {
		t.Fatalf("EnableCapture failed: %v", err)
	}

	// Write data
	audioData := &audiocore.AudioData{
		Buffer:    []byte{1, 2, 3, 4, 5, 6, 7, 8},
		Format:    config.Format,
		Timestamp: time.Now(),
		Duration:  100 * time.Millisecond,
		SourceID:  "source1",
	}

	err = manager.Write("source1", audioData)
	if err != nil {
		t.Errorf("Write failed: %v", err)
	}

	// Write to non-enabled source (should not error)
	err = manager.Write("nonexistent", audioData)
	if err != nil {
		t.Errorf("Write to non-enabled source should not error: %v", err)
	}
}

func TestManager_SaveClip(t *testing.T) {
	manager := createTestManager(t)

	format := audiocore.AudioFormat{
		SampleRate: 1000, // Low sample rate for easier testing
		Channels:   1,
		BitDepth:   16,
	}

	config := Config{
		Duration:   10 * time.Second,
		Format:     format,
		PreBuffer:  1 * time.Second,
		PostBuffer: 2 * time.Second,
	}

	// Enable capture
	err := manager.EnableCapture("source1", config)
	if err != nil {
		t.Fatalf("EnableCapture failed: %v", err)
	}

	// Write some test data
	// 1 second of data at 1000Hz, 16-bit = 2000 bytes
	testData := make([]byte, 2000)
	for i := range testData {
		testData[i] = byte(i % 256)
	}

	audioData := &audiocore.AudioData{
		Buffer:    testData,
		Format:    format,
		Timestamp: time.Now(),
		Duration:  1 * time.Second,
		SourceID:  "source1",
	}

	err = manager.Write("source1", audioData)
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	// Save clip
	triggerTime := time.Now()
	detectionDuration := 500 * time.Millisecond

	clip, err := manager.SaveClip("source1", triggerTime, detectionDuration)
	if err != nil {
		t.Errorf("SaveClip failed: %v", err)
	}

	if clip == nil {
		t.Fatal("SaveClip returned nil clip")
	}

	// Verify clip properties
	expectedDuration := config.PreBuffer + detectionDuration + config.PostBuffer
	if clip.Duration != expectedDuration {
		t.Errorf("Expected clip duration %v, got %v", expectedDuration, clip.Duration)
	}

	if clip.SourceID != "source1" {
		t.Errorf("Expected source ID 'source1', got %s", clip.SourceID)
	}

	// Test save clip for non-enabled source
	_, err = manager.SaveClip("nonexistent", triggerTime, detectionDuration)
	if err == nil {
		t.Error("Expected error saving clip for non-enabled source")
	}
}

func TestManager_ExportClip(t *testing.T) {
	// Create temp directory for exports
	tempDir, err := os.MkdirTemp("", "capture_export_test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	manager := createTestManager(t)

	format := audiocore.AudioFormat{
		SampleRate: 48000,
		Channels:   1,
		BitDepth:   16,
	}

	config := Config{
		Duration:   10 * time.Second,
		Format:     format,
		PreBuffer:  1 * time.Second,
		PostBuffer: 1 * time.Second,
		ExportConfig: &export.Config{
			Format:           export.FormatWAV,
			OutputPath:       tempDir,
			FileNameTemplate: "{source}_test",
			Timeout:          5 * time.Second,
		},
	}

	// Enable capture
	err = manager.EnableCapture("source1", config)
	if err != nil {
		t.Fatalf("EnableCapture failed: %v", err)
	}

	// Write test data
	testData := make([]byte, 48000*2) // 1 second of audio
	audioData := &audiocore.AudioData{
		Buffer:    testData,
		Format:    format,
		Timestamp: time.Now(),
		Duration:  1 * time.Second,
		SourceID:  "source1",
	}

	err = manager.Write("source1", audioData)
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	// Export clip
	ctx := context.Background()
	triggerTime := time.Now()
	detectionDuration := 200 * time.Millisecond

	result, err := manager.ExportClip(ctx, "source1", triggerTime, detectionDuration)
	if err != nil {
		t.Errorf("ExportClip failed: %v", err)
	}

	if result == nil {
		t.Fatal("ExportClip returned nil result")
	}

	if !result.Success {
		t.Error("Export result indicates failure")
	}

	// Verify file was created
	if result.FilePath != "" {
		if _, err := os.Stat(result.FilePath); os.IsNotExist(err) {
			t.Errorf("Expected file not created: %s", result.FilePath)
		}
		// Clean up
		_ = os.Remove(result.FilePath)
	}

	// Test export without export config
	config.ExportConfig = nil
	err = manager.EnableCapture("source2", config)
	if err != nil {
		t.Fatalf("EnableCapture for source2 failed: %v", err)
	}

	_, err = manager.ExportClip(ctx, "source2", triggerTime, detectionDuration)
	if err == nil {
		t.Error("Expected error exporting without export config")
	}
}

func TestManager_GetBuffer(t *testing.T) {
	manager := createTestManager(t)

	config := Config{
		Duration: 5 * time.Second,
		Format: audiocore.AudioFormat{
			SampleRate: 48000,
			Channels:   1,
			BitDepth:   16,
		},
	}

	// Get buffer for non-existent source
	buffer, exists := manager.GetBuffer("nonexistent")
	if exists {
		t.Error("GetBuffer should return false for non-existent source")
	}
	if buffer != nil {
		t.Error("GetBuffer should return nil for non-existent source")
	}

	// Enable capture and get buffer
	err := manager.EnableCapture("source1", config)
	if err != nil {
		t.Fatalf("EnableCapture failed: %v", err)
	}

	buffer, exists = manager.GetBuffer("source1")
	if !exists {
		t.Error("GetBuffer should return true for enabled source")
	}
	if buffer == nil {
		t.Error("GetBuffer should return non-nil buffer for enabled source")
	}
}

func TestManager_Close(t *testing.T) {
	manager := createTestManager(t)

	// Enable multiple captures
	config := Config{
		Duration: 5 * time.Second,
		Format: audiocore.AudioFormat{
			SampleRate: 48000,
			Channels:   1,
			BitDepth:   16,
		},
	}

	for i := 0; i < 3; i++ {
		sourceID := string(rune('a' + i))
		err := manager.EnableCapture(sourceID, config)
		if err != nil {
			t.Fatalf("EnableCapture failed for %s: %v", sourceID, err)
		}
	}

	// Close manager
	err := manager.Close()
	if err != nil {
		t.Errorf("Close failed: %v", err)
	}

	// Verify all captures are disabled
	for i := 0; i < 3; i++ {
		sourceID := string(rune('a' + i))
		if manager.IsCaptureEnabled(sourceID) {
			t.Errorf("Capture still enabled for %s after close", sourceID)
		}
	}
}

func TestManager_ValidationErrors(t *testing.T) {
	manager := createTestManager(t)

	tests := []struct {
		name   string
		config Config
	}{
		{
			name: "zero duration",
			config: Config{
				Duration: 0,
				Format: audiocore.AudioFormat{
					SampleRate: 48000,
					Channels:   1,
					BitDepth:   16,
				},
			},
		},
		{
			name: "negative duration",
			config: Config{
				Duration: -1 * time.Second,
				Format: audiocore.AudioFormat{
					SampleRate: 48000,
					Channels:   1,
					BitDepth:   16,
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := manager.EnableCapture("test", tt.config)
			if err == nil {
				t.Error("Expected validation error")
			}
		})
	}
}

// Helper function to create a test manager
func createTestManager(t *testing.T) Manager {
	bufferPool := audiocore.NewBufferPool(audiocore.BufferPoolConfig{
		SmallBufferSize:   4096,
		MediumBufferSize:  65536,
		LargeBufferSize:   1048576,
		MaxBuffersPerSize: 10,
	})

	exportManager := export.NewManager()
	exportManager.RegisterExporter(export.FormatWAV, export.NewWAVExporter())

	return NewManager(bufferPool, exportManager)
}