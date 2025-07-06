package audiocore_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tphakala/birdnet-go/internal/audiocore"
	"github.com/tphakala/birdnet-go/internal/audiocore/capture"
	"github.com/tphakala/birdnet-go/internal/audiocore/detection"
	"github.com/tphakala/birdnet-go/internal/audiocore/export"
)

// TestIntegration_CaptureAndExport tests the full flow from audio capture to clip export
func TestIntegration_CaptureAndExport(t *testing.T) {
	// Create temp directory for exports
	tempDir, err := os.MkdirTemp("", "audiocore_integration_test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	// Setup buffer pool
	bufferPool := audiocore.NewBufferPool(audiocore.BufferPoolConfig{
		SmallBufferSize:   4096,
		MediumBufferSize:  65536,
		LargeBufferSize:   1048576,
		MaxBuffersPerSize: 10,
	})

	// Setup export manager
	exportManager := export.NewManager()
	exportManager.RegisterExporter(export.FormatWAV, export.NewWAVExporter())

	// Setup capture manager
	captureManager := capture.NewManager(bufferPool, exportManager)

	// Configure capture for test source
	captureConfig := capture.Config{
		Duration: 30 * time.Second, // 30 second ring buffer
		Format: audiocore.AudioFormat{
			SampleRate: 48000,
			Channels:   1,
			BitDepth:   16,
		},
		PreBuffer:  2 * time.Second,
		PostBuffer: 3 * time.Second,
		ExportConfig: &export.Config{
			Format:           export.FormatWAV,
			OutputPath:       tempDir,
			FileNameTemplate: "{source}_{timestamp}",
			Timeout:          5 * time.Second,
		},
	}

	sourceID := "test_source"
	err = captureManager.EnableCapture(sourceID, captureConfig)
	if err != nil {
		t.Fatalf("Failed to enable capture: %v", err)
	}

	// Generate test audio data (1 second of audio)
	sampleRate := captureConfig.Format.SampleRate
	duration := 1 * time.Second
	samples := int(float64(sampleRate) * duration.Seconds())
	audioData := make([]byte, samples*2) // 16-bit samples

	// Generate a simple tone pattern
	for i := 0; i < samples; i++ {
		// Simple alternating pattern for testing
		var value int16
		if i%100 < 50 {
			value = 10000
		} else {
			value = -10000
		}
		audioData[i*2] = byte(value)
		audioData[i*2+1] = byte(value >> 8)
	}

	// Write multiple chunks of audio data to build up buffer
	baseTime := time.Now()
	for i := 0; i < 5; i++ {
		audioChunk := &audiocore.AudioData{
			Buffer:    audioData,
			Format:    captureConfig.Format,
			Timestamp: baseTime.Add(time.Duration(i) * time.Second),
			Duration:  duration,
			SourceID:  sourceID,
		}

		err = captureManager.Write(sourceID, audioChunk)
		if err != nil {
			t.Fatalf("Failed to write audio data: %v", err)
		}
	}

	// Create detection handler chain
	handlerChain := detection.NewHandlerChain()

	// Add capture handler to the chain
	captureHandler := detection.NewCaptureHandler("capture-handler", captureManager, 0.7)
	_ = handlerChain.AddHandler(captureHandler)

	// Simulate detection results
	detectionTime := baseTime.Add(2500 * time.Millisecond) // Middle of our audio data
	analysisResult := &detection.AnalysisResult{
		SourceID:  sourceID,
		Timestamp: detectionTime,
		Duration:  1 * time.Second,
		AnalyzerID: "test-analyzer",
		Detections: []detection.Detection{
			{
				SourceID:   sourceID,
				Timestamp:  detectionTime,
				Species:    "Test Bird",
				Confidence: 0.95,
				StartTime:  0.0,
				EndTime:    1.0,
			},
		},
	}

	// Process detection through handler chain
	ctx := context.Background()
	err = handlerChain.HandleAnalysisResult(ctx, analysisResult)
	if err != nil {
		t.Fatalf("Failed to process detection: %v", err)
	}

	// Verify that a clip was exported
	files, err := filepath.Glob(filepath.Join(tempDir, "*.wav"))
	if err != nil {
		t.Fatalf("Failed to list exported files: %v", err)
	}

	if len(files) != 1 {
		t.Errorf("Expected 1 exported file, found %d", len(files))
	}

	if len(files) > 0 {
		// Verify file exists and has content
		fileInfo, err := os.Stat(files[0])
		if err != nil {
			t.Errorf("Failed to stat exported file: %v", err)
		}

		// WAV header is 44 bytes, plus we should have audio data
		if fileInfo.Size() <= 44 {
			t.Errorf("Exported file too small: %d bytes", fileInfo.Size())
		}

		t.Logf("Successfully exported clip: %s (size: %d bytes)", files[0], fileInfo.Size())
	}

	// Clean up
	err = captureManager.Close()
	if err != nil {
		t.Errorf("Failed to close capture manager: %v", err)
	}
}

// TestIntegration_MultipleSources tests capture and export with multiple audio sources
func TestIntegration_MultipleSources(t *testing.T) {
	// Create temp directory
	tempDir, err := os.MkdirTemp("", "audiocore_multi_source_test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	// Setup components
	bufferPool := audiocore.NewBufferPool(audiocore.BufferPoolConfig{
		SmallBufferSize:   4096,
		MediumBufferSize:  65536,
		LargeBufferSize:   1048576,
		MaxBuffersPerSize: 20,
	})

	exportManager := export.NewManager()
	exportManager.RegisterExporter(export.FormatWAV, export.NewWAVExporter())

	captureManager := capture.NewManager(bufferPool, exportManager)

	// Configure multiple sources
	sources := []string{"mic1", "mic2", "rtsp_stream"}
	for _, sourceID := range sources {
		config := capture.Config{
			Duration: 20 * time.Second,
			Format: audiocore.AudioFormat{
				SampleRate: 48000,
				Channels:   1,
				BitDepth:   16,
			},
			PreBuffer:  1 * time.Second,
			PostBuffer: 2 * time.Second,
			ExportConfig: &export.Config{
				Format:           export.FormatWAV,
				OutputPath:       tempDir,
				FileNameTemplate: "{source}_detection_{timestamp}",
				Timeout:          5 * time.Second,
			},
		}

		err = captureManager.EnableCapture(sourceID, config)
		if err != nil {
			t.Fatalf("Failed to enable capture for %s: %v", sourceID, err)
		}
	}

	// Write audio data to each source
	baseTime := time.Now()
	audioData := make([]byte, 48000*2) // 1 second of silence

	for _, sourceID := range sources {
		chunk := &audiocore.AudioData{
			Buffer:    audioData,
			Format:    audiocore.AudioFormat{SampleRate: 48000, Channels: 1, BitDepth: 16},
			Timestamp: baseTime,
			Duration:  1 * time.Second,
			SourceID:  sourceID,
		}

		err = captureManager.Write(sourceID, chunk)
		if err != nil {
			t.Fatalf("Failed to write to %s: %v", sourceID, err)
		}
	}

	// Create handler chain
	handlerChain := detection.NewHandlerChain()
	captureHandler := detection.NewCaptureHandler("capture-handler", captureManager, 0.6)
	_ = handlerChain.AddHandler(captureHandler)

	// Process detections for multiple sources
	ctx := context.Background()
	for i, sourceID := range sources {
		analysisResult := &detection.AnalysisResult{
			SourceID:  sourceID,
			Timestamp: baseTime.Add(time.Duration(i*100) * time.Millisecond),
			Duration:  500 * time.Millisecond,
			AnalyzerID: "test-analyzer",
			Detections: []detection.Detection{
				{
					SourceID:   sourceID,
					Timestamp:  baseTime.Add(time.Duration(i*100) * time.Millisecond),
					Species:    "Test Bird " + sourceID,
					Confidence: float32(0.8 + float64(i)*0.05),
					StartTime:  0.0,
					EndTime:    0.5,
				},
			},
		}

		err = handlerChain.HandleAnalysisResult(ctx, analysisResult)
		if err != nil {
			t.Fatalf("Failed to process detections: %v", err)
		}
	}

	// Verify clips were exported for each source
	for _, sourceID := range sources {
		pattern := filepath.Join(tempDir, sourceID+"_detection_*.wav")
		files, err := filepath.Glob(pattern)
		if err != nil {
			t.Errorf("Failed to glob files for %s: %v", sourceID, err)
		}

		if len(files) != 1 {
			t.Errorf("Expected 1 file for %s, found %d", sourceID, len(files))
		}
	}

	// Clean up
	_ = captureManager.Close()
}

// TestIntegration_ConcurrentDetections tests handling concurrent detections
func TestIntegration_ConcurrentDetections(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "audiocore_concurrent_test")
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

	exportManager := export.DefaultManager("")
	captureManager := capture.NewManager(bufferPool, exportManager)

	// Configure capture
	config := capture.Config{
		Duration: 60 * time.Second,
		Format: audiocore.AudioFormat{
			SampleRate: 48000,
			Channels:   1,
			BitDepth:   16,
		},
		PreBuffer:  2 * time.Second,
		PostBuffer: 3 * time.Second,
		ExportConfig: &export.Config{
			Format:           export.FormatWAV,
			OutputPath:       tempDir,
			FileNameTemplate: "{source}_{timestamp}",
			Timeout:          10 * time.Second,
		},
	}

	sourceID := "concurrent_test"
	err = captureManager.EnableCapture(sourceID, config)
	if err != nil {
		t.Fatalf("Failed to enable capture: %v", err)
	}

	// Fill buffer with audio data
	audioData := make([]byte, 48000*2*10) // 10 seconds
	chunk := &audiocore.AudioData{
		Buffer:    audioData,
		Format:    config.Format,
		Timestamp: time.Now(),
		Duration:  10 * time.Second,
		SourceID:  sourceID,
	}

	err = captureManager.Write(sourceID, chunk)
	if err != nil {
		t.Fatalf("Failed to write audio: %v", err)
	}

	// Create handler
	handlerChain := detection.NewHandlerChain()
	captureHandler := detection.NewCaptureHandler("capture-handler", captureManager, 0.5)
	_ = handlerChain.AddHandler(captureHandler)

	// Process multiple detections concurrently
	done := make(chan bool)
	detectionCount := 5

	for i := 0; i < detectionCount; i++ {
		go func(id int) {
			timestamp := time.Now().Add(time.Duration(id) * time.Second)
			analysisResult := &detection.AnalysisResult{
				SourceID:  sourceID,
				Timestamp: timestamp,
				Duration:  500 * time.Millisecond,
				AnalyzerID: "test-analyzer",
				Detections: []detection.Detection{
					{
						SourceID:   sourceID,
						Timestamp:  timestamp,
						Species:    "Concurrent Bird",
						Confidence: 0.9,
						StartTime:  float64(id),
						EndTime:    float64(id) + 0.5,
					},
				},
			}

			ctx := context.Background()
			_ = handlerChain.HandleAnalysisResult(ctx, analysisResult)
			done <- true
		}(i)
	}

	// Wait for all detections to be processed
	for i := 0; i < detectionCount; i++ {
		<-done
	}

	// Give some time for exports to complete
	time.Sleep(100 * time.Millisecond)

	// Verify exports
	files, err := filepath.Glob(filepath.Join(tempDir, "*.wav"))
	if err != nil {
		t.Fatalf("Failed to list files: %v", err)
	}

	if len(files) != detectionCount {
		t.Errorf("Expected %d exported files, found %d", detectionCount, len(files))
	}

	_ = captureManager.Close()
}