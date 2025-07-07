package processor

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/tphakala/birdnet-go/internal/audiocore/export"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/observability"
)

// Mock CaptureManager for testing
type mockCaptureManager struct {
	exportClipFunc     func(ctx context.Context, sourceID string, triggerTime time.Time, duration time.Duration) (*export.ExportResult, error)
	captureEnabledFunc func(sourceID string) bool
}

func (m *mockCaptureManager) ExportClip(ctx context.Context, sourceID string, triggerTime time.Time, duration time.Duration) (*export.ExportResult, error) {
	if m.exportClipFunc != nil {
		return m.exportClipFunc(ctx, sourceID, triggerTime, duration)
	}
	return &export.ExportResult{
		Success:  true,
		FilePath: "/tmp/test.wav",
		Duration: 1 * time.Second,
	}, nil
}

func (m *mockCaptureManager) IsCaptureEnabled(sourceID string) bool {
	if m.captureEnabledFunc != nil {
		return m.captureEnabledFunc(sourceID)
	}
	return true
}

func TestProcessor_SetGetCaptureManager(t *testing.T) {
	// Create a minimal processor for testing
	settings := &conf.Settings{}
	proc := &Processor{
		Settings: settings,
		Metrics:  &observability.Metrics{},
	}

	// Initially, capture manager should be nil
	if proc.GetCaptureManager() != nil {
		t.Error("Expected capture manager to be nil initially")
	}

	// Create mock capture manager
	mockManager := &mockCaptureManager{}

	// Set capture manager
	proc.SetCaptureManager(mockManager)

	// Get capture manager and verify it's the same instance
	retrievedManager := proc.GetCaptureManager()
	if retrievedManager != mockManager {
		t.Error("Retrieved capture manager does not match the set manager")
	}

	// Test that it implements the interface correctly
	ctx := context.Background()
	result, err := retrievedManager.ExportClip(ctx, "test", time.Now(), 5*time.Second)
	if err != nil {
		t.Errorf("ExportClip failed: %v", err)
	}
	if !result.Success {
		t.Error("Expected successful export")
	}

	// Test IsCaptureEnabled
	if !retrievedManager.IsCaptureEnabled("test") {
		t.Error("Expected capture to be enabled")
	}
}

func TestProcessor_CaptureManagerConcurrency(t *testing.T) {
	// Create a minimal processor for testing
	settings := &conf.Settings{}
	proc := &Processor{
		Settings: settings,
		Metrics:  &observability.Metrics{},
	}

	// Create multiple mock managers
	managers := make([]*mockCaptureManager, 10)
	for i := range managers {
		managers[i] = &mockCaptureManager{}
	}

	// Test concurrent access
	var wg sync.WaitGroup
	iterations := 100

	// Start multiple goroutines setting managers
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				proc.SetCaptureManager(managers[idx])
			}
		}(i)
	}

	// Start multiple goroutines getting managers
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				_ = proc.GetCaptureManager()
			}
		}()
	}

	// Wait for all goroutines to complete
	wg.Wait()

	// Final check - should have a valid manager
	finalManager := proc.GetCaptureManager()
	if finalManager == nil {
		t.Error("Expected a non-nil capture manager after concurrent operations")
	}
}

func TestProcessor_SetCaptureManagerNil(t *testing.T) {
	// Create a minimal processor for testing
	settings := &conf.Settings{}
	proc := &Processor{
		Settings: settings,
		Metrics:  &observability.Metrics{},
	}

	// Set a manager first
	mockManager := &mockCaptureManager{}
	proc.SetCaptureManager(mockManager)

	// Verify it's set
	if proc.GetCaptureManager() == nil {
		t.Error("Expected capture manager to be set")
	}

	// Set to nil
	proc.SetCaptureManager(nil)

	// Verify it's nil
	if proc.GetCaptureManager() != nil {
		t.Error("Expected capture manager to be nil after setting to nil")
	}
}

func TestProcessor_CaptureManagerTimeout(t *testing.T) {
	// Create a minimal processor for testing
	settings := &conf.Settings{}
	proc := &Processor{
		Settings: settings,
		Metrics:  &observability.Metrics{},
	}

	// Create mock manager that simulates a slow export
	mockManager := &mockCaptureManager{
		exportClipFunc: func(ctx context.Context, sourceID string, triggerTime time.Time, duration time.Duration) (*export.ExportResult, error) {
			// Simulate long-running export
			select {
			case <-time.After(5 * time.Second):
				return &export.ExportResult{Success: true}, nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		},
	}

	proc.SetCaptureManager(mockManager)

	// Test with timeout context
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := proc.GetCaptureManager().ExportClip(ctx, "test", time.Now(), 5*time.Second)
	elapsed := time.Since(start)

	// Should timeout quickly
	if err == nil {
		t.Error("Expected timeout error")
	}
	if elapsed > 200*time.Millisecond {
		t.Errorf("Operation took too long: %v", elapsed)
	}
}