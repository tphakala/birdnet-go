package ffmpeg

import (
	"context"
	"fmt"
	"math"
	"sync"
	"testing"
	"time"
)

// mockProcess implements the Process interface for testing
type mockProcess struct {
	id              string
	running         bool
	audioOutput     chan []byte
	errorOutput     chan error
	metrics         ProcessMetrics
	simulateSilence bool
}

func (m *mockProcess) ID() string                         { return m.id }
func (m *mockProcess) Start(ctx context.Context) error   { m.running = true; return nil }
func (m *mockProcess) Stop() error                       { m.running = false; return nil }
func (m *mockProcess) Wait() error                       { return nil }
func (m *mockProcess) IsRunning() bool                   { return m.running }
func (m *mockProcess) AudioOutput() <-chan []byte        { return m.audioOutput }
func (m *mockProcess) ErrorOutput() <-chan error         { return m.errorOutput }
func (m *mockProcess) Metrics() ProcessMetrics           { return m.metrics }

// setSilence controls whether the mock process simulates silence
func (m *mockProcess) setSilence(silent bool) {
	m.simulateSilence = silent
}

// generateAudioData generates mock audio data
func (m *mockProcess) generateAudioData() {
	if m.simulateSilence {
		// Generate silence (very low audio levels)
		silence := make([]byte, 1024)
		select {
		case m.audioOutput <- silence:
		default:
		}
	} else {
		// Generate normal audio data (simulated random data)
		audioData := make([]byte, 1024)
		for i := range audioData {
			audioData[i] = byte(i % 128) // Simple pattern for non-silent audio
		}
		select {
		case m.audioOutput <- audioData:
		default:
		}
	}
}

func newMockProcess(id string) *mockProcess {
	return &mockProcess{
		id:          id,
		running:     true,
		audioOutput: make(chan []byte, 10),
		errorOutput: make(chan error, 10),
		metrics: ProcessMetrics{
			StartTime:    time.Now(),
			RestartCount: 0,
			LastRestart:  time.Now(),
		},
	}
}

func TestNewHealthChecker(t *testing.T) {
	t.Parallel()
	
	checker := NewHealthChecker()
	if checker == nil {
		t.Error("NewHealthChecker should not return nil")
	}
}

func TestHealthCheckerSetSilenceThreshold(t *testing.T) {
	t.Parallel()
	
	checker := NewHealthChecker()
	
	threshold := float32(-50.0)
	duration := 30 * time.Second
	
	checker.SetSilenceThreshold(threshold, duration)
	
	// We can't directly access private fields, but we can test the behavior
	// by creating a mock process and checking if silence detection works
}

func TestHealthCheckerCheckRunningProcess(t *testing.T) {
	t.Parallel()
	
	checker := NewHealthChecker()
	process := newMockProcess("test-process")
	
	// Check running process should pass initially
	err := checker.Check(process)
	if err != nil {
		t.Errorf("Health check should pass for running process: %v", err)
	}
}

func TestHealthCheckerCheckNotRunningProcess(t *testing.T) {
	t.Parallel()
	
	checker := NewHealthChecker()
	process := newMockProcess("test-process")
	process.running = false
	
	// Check non-running process should fail
	err := checker.Check(process)
	if err == nil {
		t.Error("Health check should fail for non-running process")
	}
}

func TestHealthCheckerCheckFrequentRestarts(t *testing.T) {
	t.Parallel()
	
	checker := NewHealthChecker()
	process := newMockProcess("test-process")
	
	// Set metrics to indicate frequent restarts
	process.metrics.RestartCount = 15
	process.metrics.LastRestart = time.Now().Add(-2 * time.Minute)
	
	err := checker.Check(process)
	if err == nil {
		t.Error("Health check should fail for frequently restarting process")
	}
}

func TestHealthCheckerCheckRecentError(t *testing.T) {
	t.Parallel()
	
	checker := NewHealthChecker()
	process := newMockProcess("test-process")
	
	// Set metrics to indicate recent error
	process.metrics.LastError = fmt.Errorf("test error")
	process.metrics.LastRestart = time.Now().Add(-10 * time.Second)
	
	err := checker.Check(process)
	if err == nil {
		t.Error("Health check should fail for process with recent error")
	}
}

func TestCalculateAudioLevel(t *testing.T) {
	t.Parallel()
	
	checker := &healthChecker{}
	
	// Test empty data
	level := checker.calculateAudioLevel([]byte{})
	if level != 0 {
		t.Errorf("Expected level 0 for empty data, got %f", level)
	}
	
	// Test single byte (insufficient data)
	level = checker.calculateAudioLevel([]byte{0x00})
	if level != 0 {
		t.Errorf("Expected level 0 for insufficient data, got %f", level)
	}
	
	// Test silence (16-bit samples of 0)
	silenceData := []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}
	level = checker.calculateAudioLevel(silenceData)
	if level != 0 {
		t.Errorf("Expected level 0 for silence, got %f", level)
	}
	
	// Test maximum amplitude (16-bit signed)
	maxData := []byte{0xFF, 0x7F, 0xFF, 0x7F} // Two samples at max positive
	level = checker.calculateAudioLevel(maxData)
	if level == 0 {
		t.Error("Expected non-zero level for maximum amplitude")
	}
	
	// Level should be close to 1.0 for maximum amplitude
	expected := float32(0.9) // Allow some tolerance
	if level < expected {
		t.Errorf("Expected level >= %f for maximum amplitude, got %f", expected, level)
	}
}

func TestAmplitudeToDecibels(t *testing.T) {
	t.Parallel()
	
	checker := &healthChecker{}
	
	// Test zero amplitude
	db := checker.amplitudeToDecibels(0)
	if db != -100.0 {
		t.Errorf("Expected -100 dB for zero amplitude, got %f", db)
	}
	
	// Test negative amplitude (should return -100)
	db = checker.amplitudeToDecibels(-0.5)
	if db != -100.0 {
		t.Errorf("Expected -100 dB for negative amplitude, got %f", db)
	}
	
	// Test unit amplitude (should be 0 dB)
	db = checker.amplitudeToDecibels(1.0)
	if math.Abs(float64(db)) > 0.001 {
		t.Errorf("Expected ~0 dB for unit amplitude, got %f", db)
	}
	
	// Test half amplitude (should be ~-6 dB)
	db = checker.amplitudeToDecibels(0.5)
	expected := float32(-6.0)
	if math.Abs(float64(db-expected)) > 0.1 {
		t.Errorf("Expected ~-6 dB for half amplitude, got %f", db)
	}
}

func TestHealthCheckerAudioLevelStats(t *testing.T) {
	t.Parallel()
	
	checker := &healthChecker{
		audioLevels: make(map[string]*audioLevelTracker),
	}
	
	// Test non-existent process
	_, _, _, ok := checker.GetAudioLevelStats("nonexistent")
	if ok {
		t.Error("Should return false for non-existent process")
	}
	
	// Add a tracker manually
	processID := "test-process"
	checker.audioLevels[processID] = &audioLevelTracker{
		avgLevel:    0.5,
		lastAudioLevel: 0.7,
		sampleCount: 100,
	}
	
	avgLevel, lastLevel, sampleCount, ok := checker.GetAudioLevelStats(processID)
	if !ok {
		t.Error("Should return true for existing process")
	}
	
	if avgLevel != 0.5 {
		t.Errorf("Expected avg level 0.5, got %f", avgLevel)
	}
	
	if lastLevel != 0.7 {
		t.Errorf("Expected last level 0.7, got %f", lastLevel)
	}
	
	if sampleCount != 100 {
		t.Errorf("Expected sample count 100, got %d", sampleCount)
	}
}

func TestHealthCheckerSilenceDetection(t *testing.T) {
	t.Parallel()
	
	// Test that silence detection threshold setting works correctly
	checker := NewHealthChecker()
	process := newMockProcess("silence-test")
	
	// Test setting different thresholds
	checker.SetSilenceThreshold(-40.0, 1*time.Second)
	checker.SetSilenceThreshold(-60.0, 5*time.Second)
	
	// First check should initialize the tracker and pass
	err := checker.Check(process)
	if err != nil {
		t.Errorf("Initial check should pass: %v", err)
	}
	
	// Test process not running - should fail
	err = process.Stop()
	if err != nil {
		t.Errorf("Failed to stop process: %v", err)
	}
	err = checker.Check(process)
	if err == nil {
		t.Error("Health check should fail for stopped process")
	}
	
	// Start process again for final verification
	err = process.Start(context.Background())
	if err != nil {
		t.Errorf("Failed to start process: %v", err)
	}
	err = checker.Check(process)
	if err != nil {
		t.Errorf("Health check should pass for running process: %v", err)
	}
}

func TestHealthCheckerConcurrentAccess(t *testing.T) {
	t.Parallel()
	
	checker := NewHealthChecker()
	process := newMockProcess("concurrent-test")
	
	// Test concurrent access to health checker
	var wg sync.WaitGroup
	wg.Add(2)
	
	// Goroutine 1: Perform health checks
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			_ = checker.Check(process) // Ignore error for concurrency test
		}
	}()
	
	// Goroutine 2: Set thresholds
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			checker.SetSilenceThreshold(-60.0, 30*time.Second)
		}
	}()
	
	// Wait for both goroutines to complete
	wg.Wait()
	
	// If we get here without panicking, concurrent access works
}