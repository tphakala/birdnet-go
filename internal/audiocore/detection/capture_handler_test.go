package detection

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/tphakala/birdnet-go/internal/audiocore"
	"github.com/tphakala/birdnet-go/internal/audiocore/capture"
	"github.com/tphakala/birdnet-go/internal/audiocore/export"
)

// Mock capture manager for testing
type mockCaptureManager struct {
	exportClipFunc func(ctx context.Context, sourceID string, triggerTime time.Time, duration time.Duration) (*export.ExportResult, error)
	clipsSaved     int
}

func (m *mockCaptureManager) EnableCapture(sourceID string, config capture.Config) error {
	return nil
}

func (m *mockCaptureManager) DisableCapture(sourceID string) error {
	return nil
}

func (m *mockCaptureManager) IsCaptureEnabled(sourceID string) bool {
	return true
}

func (m *mockCaptureManager) Write(sourceID string, audioData *audiocore.AudioData) error {
	return nil
}

func (m *mockCaptureManager) SaveClip(sourceID string, triggerTime time.Time, duration time.Duration) (*audiocore.AudioData, error) {
	return nil, nil
}

func (m *mockCaptureManager) ExportClip(ctx context.Context, sourceID string, triggerTime time.Time, duration time.Duration) (*export.ExportResult, error) {
	m.clipsSaved++
	if m.exportClipFunc != nil {
		return m.exportClipFunc(ctx, sourceID, triggerTime, duration)
	}
	return &export.ExportResult{
		Success:  true,
		FilePath: "/tmp/test_clip.wav",
		Duration: duration,
		Metadata: &export.Metadata{
			SourceID:  sourceID,
			Timestamp: triggerTime,
			Duration:  duration,
			Format:   export.FormatWAV,
		},
	}, nil
}

func (m *mockCaptureManager) GetBuffer(sourceID string) (capture.Buffer, bool) {
	return nil, false
}

func (m *mockCaptureManager) Close() error {
	return nil
}

func TestNewCaptureHandler(t *testing.T) {
	mockManager := &mockCaptureManager{}
	minConfidence := float32(0.8)

	handler := NewCaptureHandler("test-capture", mockManager, minConfidence)
	if handler == nil {
		t.Fatal("NewCaptureHandler returned nil")
	}

	// Verify handler ID
	if handler.ID() != "test-capture" {
		t.Errorf("Expected ID 'test-capture', got %s", handler.ID())
	}
}

func TestCaptureHandler_HandleAnalysisResult(t *testing.T) {
	mockManager := &mockCaptureManager{}
	handler := NewCaptureHandler("test-capture", mockManager, 0.7)

	// Create test result with detections
	testResult := &AnalysisResult{
		SourceID:  "source1",
		Timestamp: time.Now(),
		Detections: []Detection{
			{
				SourceID:   "source1",
				Species:    "Test Bird",
				Confidence: 0.95,
				StartTime:  1.0,
				EndTime:    2.5,
			},
			{
				SourceID:   "source1",
				Species:    "Another Bird",
				Confidence: 0.85,
				StartTime:  3.0,
				EndTime:    4.0,
			},
		},
	}

	ctx := context.Background()
	err := handler.HandleAnalysisResult(ctx, testResult)
	if err != nil {
		t.Errorf("HandleAnalysisResult failed: %v", err)
	}

	// Verify clips were saved
	if mockManager.clipsSaved != 2 {
		t.Errorf("Expected 2 clips saved, got %d", mockManager.clipsSaved)
	}
}

func TestCaptureHandler_HandleBelowThreshold(t *testing.T) {
	mockManager := &mockCaptureManager{}
	handler := NewCaptureHandler("test-capture", mockManager, 0.9)

	// Create test result with low confidence detections
	testResult := &AnalysisResult{
		SourceID:  "source1",
		Timestamp: time.Now(),
		Detections: []Detection{
			{
				SourceID:   "source1",
				Species:    "Test Bird",
				Confidence: 0.6, // Below threshold
				StartTime:  1.0,
				EndTime:    2.0,
			},
			{
				SourceID:   "source1",
				Species:    "Another Bird",
				Confidence: 0.7, // Below threshold
				StartTime:  3.0,
				EndTime:    4.0,
			},
		},
	}

	ctx := context.Background()
	err := handler.HandleAnalysisResult(ctx, testResult)
	if err != nil {
		t.Errorf("HandleAnalysisResult failed: %v", err)
	}

	// Verify no clips were saved
	if mockManager.clipsSaved != 0 {
		t.Errorf("Expected 0 clips saved, got %d", mockManager.clipsSaved)
	}
}

func TestCaptureHandler_HandleWithError(t *testing.T) {
	mockManager := &mockCaptureManager{
		exportClipFunc: func(ctx context.Context, sourceID string, triggerTime time.Time, duration time.Duration) (*export.ExportResult, error) {
			return nil, errors.New("export failed")
		},
	}

	handler := NewCaptureHandler("test-capture", mockManager, 0.7)

	testResult := &AnalysisResult{
		SourceID:  "source1",
		Timestamp: time.Now(),
		Detections: []Detection{
			{
				SourceID:   "source1",
				Species:    "Test Bird",
				Confidence: 0.95,
				StartTime:  1.0,
				EndTime:    2.0,
			},
		},
	}

	ctx := context.Background()
	err := handler.HandleAnalysisResult(ctx, testResult)
	// The current implementation returns the error
	if err == nil {
		t.Error("Expected error from export failure")
	}
}

func TestCaptureHandler_HandleEmptyResults(t *testing.T) {
	mockManager := &mockCaptureManager{}
	handler := NewCaptureHandler("test-capture", mockManager, 0.7)

	// Test with nil result
	ctx := context.Background()
	err := handler.HandleAnalysisResult(ctx, nil)
	if err != nil {
		t.Errorf("HandleAnalysisResult with nil result failed: %v", err)
	}

	// Test with result but no detections
	testResult := &AnalysisResult{
		SourceID:   "source1",
		Timestamp:  time.Now(),
		Detections: []Detection{},
	}
	err = handler.HandleAnalysisResult(ctx, testResult)
	if err != nil {
		t.Errorf("HandleAnalysisResult with no detections failed: %v", err)
	}

	// Verify no clips were saved
	if mockManager.clipsSaved != 0 {
		t.Errorf("Expected 0 clips saved, got %d", mockManager.clipsSaved)
	}
}

func TestCaptureHandler_HandleDetection(t *testing.T) {
	mockManager := &mockCaptureManager{}
	handler := NewCaptureHandler("test-capture", mockManager, 0.7)

	// Test single detection
	ctx := context.Background()
	detection := &Detection{
		SourceID:   "source1",
		Timestamp:  time.Now(),
		Species:    "Test Bird",
		Confidence: 0.95,
		StartTime:  1.0,
		EndTime:    2.0,
	}

	err := handler.HandleDetection(ctx, detection)
	if err != nil {
		t.Errorf("HandleDetection failed: %v", err)
	}

	// Verify clip was saved
	if mockManager.clipsSaved != 1 {
		t.Errorf("Expected 1 clip saved, got %d", mockManager.clipsSaved)
	}
}

func TestCaptureHandler_ContextCancellation(t *testing.T) {
	mockManager := &mockCaptureManager{
		exportClipFunc: func(ctx context.Context, sourceID string, triggerTime time.Time, duration time.Duration) (*export.ExportResult, error) {
			// Check if context is cancelled
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			default:
				return &export.ExportResult{Success: true}, nil
			}
		},
	}

	handler := NewCaptureHandler("test-capture", mockManager, 0.7)

	testResult := &AnalysisResult{
		SourceID:  "source1",
		Timestamp: time.Now(),
		Detections: []Detection{
			{
				SourceID:   "source1",
				Species:    "Test Bird",
				Confidence: 0.95,
				StartTime:  1.0,
				EndTime:    2.0,
			},
		},
	}

	// Create cancelled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := handler.HandleAnalysisResult(ctx, testResult)
	if err == nil {
		t.Error("Expected error for cancelled context")
	}
}

func TestCaptureHandler_MultipleDetections(t *testing.T) {
	mockManager := &mockCaptureManager{}
	handler := NewCaptureHandler("test-capture", mockManager, 0.7)

	// Create results with multiple detections
	now := time.Now()
	testResults := []*AnalysisResult{
		{
			SourceID:  "source1",
			Timestamp: now,
			Detections: []Detection{
				{SourceID: "source1", Species: "Bird1", Confidence: 0.95, StartTime: 1.0, EndTime: 2.0},
				{SourceID: "source1", Species: "Bird2", Confidence: 0.85, StartTime: 3.0, EndTime: 4.0},
			},
		},
		{
			SourceID:  "source2",
			Timestamp: now.Add(1 * time.Second),
			Detections: []Detection{
				{SourceID: "source2", Species: "Bird3", Confidence: 0.90, StartTime: 0.5, EndTime: 1.5},
			},
		},
		{
			SourceID:  "source3",
			Timestamp: now.Add(2 * time.Second),
			Detections: []Detection{
				{SourceID: "source3", Species: "Bird4", Confidence: 0.65, StartTime: 2.0, EndTime: 3.0}, // Below threshold
				{SourceID: "source3", Species: "Bird5", Confidence: 0.92, StartTime: 4.0, EndTime: 5.0},
			},
		},
	}

	ctx := context.Background()
	for _, result := range testResults {
		err := handler.HandleAnalysisResult(ctx, result)
		if err != nil {
			t.Errorf("HandleAnalysisResult failed: %v", err)
		}
	}

	// Should save 4 clips (2 from source1, 1 from source2, 1 from source3)
	if mockManager.clipsSaved != 4 {
		t.Errorf("Expected 4 clips saved, got %d", mockManager.clipsSaved)
	}
}

func TestCaptureHandler_Close(t *testing.T) {
	mockManager := &mockCaptureManager{}
	handler := NewCaptureHandler("test-capture", mockManager, 0.7)

	// Close should not error
	err := handler.Close()
	if err != nil {
		t.Errorf("Close failed: %v", err)
	}
}

func BenchmarkCaptureHandler_HandleAnalysisResult(b *testing.B) {
	// Create temp dir for benchmark
	tempDir, err := os.MkdirTemp("", "capture_handler_bench")
	if err != nil {
		b.Fatalf("Failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	mockManager := &mockCaptureManager{}
	handler := NewCaptureHandler("bench-capture", mockManager, 0.7)

	// Create test data
	testResult := &AnalysisResult{
		SourceID:  "bench_source",
		Timestamp: time.Now(),
		Detections: []Detection{
			{SourceID: "bench_source", Species: "Bird1", Confidence: 0.95, StartTime: 1.0, EndTime: 2.0},
			{SourceID: "bench_source", Species: "Bird2", Confidence: 0.85, StartTime: 3.0, EndTime: 4.0},
			{SourceID: "bench_source", Species: "Bird3", Confidence: 0.75, StartTime: 5.0, EndTime: 6.0},
		},
	}

	ctx := context.Background()

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_ = handler.HandleAnalysisResult(ctx, testResult)
	}
}