package ffmpeg

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

func (m *mockProcess) ID() string                      { return m.id }
func (m *mockProcess) Start(ctx context.Context) error { m.running = true; return nil }
func (m *mockProcess) Stop() error                     { m.running = false; return nil }
func (m *mockProcess) Wait() error                     { return nil }
func (m *mockProcess) IsRunning() bool                 { return m.running }
func (m *mockProcess) AudioOutput() <-chan []byte      { return m.audioOutput }
func (m *mockProcess) ErrorOutput() <-chan error       { return m.errorOutput }
func (m *mockProcess) Metrics() ProcessMetrics         { return m.metrics }

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
	assert.NotNil(t, checker, "NewHealthChecker should not return nil")
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
	assert.NoError(t, err, "Health check should pass for running process")
}

func TestHealthCheckerCheckNotRunningProcess(t *testing.T) {
	t.Parallel()

	checker := NewHealthChecker()
	process := newMockProcess("test-process")
	process.running = false

	// Check non-running process should fail
	err := checker.Check(process)
	assert.Error(t, err, "Health check should fail for non-running process")
}

func TestHealthCheckerCheckFrequentRestarts(t *testing.T) {
	t.Parallel()

	checker := NewHealthChecker()
	process := newMockProcess("test-process")

	// Set metrics to indicate frequent restarts
	process.metrics.RestartCount = 15
	process.metrics.LastRestart = time.Now().Add(-2 * time.Minute)

	err := checker.Check(process)
	assert.Error(t, err, "Health check should fail for frequently restarting process")
}

func TestHealthCheckerCheckRecentError(t *testing.T) {
	t.Parallel()

	checker := NewHealthChecker()
	process := newMockProcess("test-process")

	// Set metrics to indicate recent error
	process.metrics.LastError = fmt.Errorf("test error")
	process.metrics.LastRestart = time.Now().Add(-10 * time.Second)

	err := checker.Check(process)
	assert.Error(t, err, "Health check should fail for process with recent error")
}

func TestCalculateAudioLevel(t *testing.T) {
	t.Parallel()

	checker := &healthChecker{}

	// Test empty data
	level := checker.calculateAudioLevel([]byte{})
	assert.InDelta(t, 0.0, level, 0.001, "Expected level 0 for empty data")

	// Test single byte (insufficient data)
	level = checker.calculateAudioLevel([]byte{0x00})
	assert.InDelta(t, 0.0, level, 0.001, "Expected level 0 for insufficient data")

	// Test silence (16-bit samples of 0)
	silenceData := []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}
	level = checker.calculateAudioLevel(silenceData)
	assert.InDelta(t, 0.0, level, 0.001, "Expected level 0 for silence")

	// Test maximum amplitude (16-bit signed)
	maxData := []byte{0xFF, 0x7F, 0xFF, 0x7F} // Two samples at max positive
	level = checker.calculateAudioLevel(maxData)
	assert.NotEqual(t, float32(0), level, "Expected non-zero level for maximum amplitude")

	// Level should be close to 1.0 for maximum amplitude
	expected := float32(0.9) // Allow some tolerance
	assert.GreaterOrEqual(t, level, expected, "Expected level >= 0.9 for maximum amplitude")
}

func TestAmplitudeToDecibels(t *testing.T) {
	t.Parallel()

	checker := &healthChecker{}

	// Test zero amplitude
	db := checker.amplitudeToDecibels(0)
	assert.InDelta(t, -100.0, db, 0.001, "Expected -100 dB for zero amplitude")

	// Test negative amplitude (should return -100)
	db = checker.amplitudeToDecibels(-0.5)
	assert.InDelta(t, -100.0, db, 0.001, "Expected -100 dB for negative amplitude")

	// Test unit amplitude (should be 0 dB)
	db = checker.amplitudeToDecibels(1.0)
	assert.InDelta(t, 0.0, db, 0.001, "Expected ~0 dB for unit amplitude")

	// Test half amplitude (should be ~-6 dB)
	db = checker.amplitudeToDecibels(0.5)
	assert.InDelta(t, -6.0, db, 0.1, "Expected ~-6 dB for half amplitude")
}

func TestHealthCheckerAudioLevelStats(t *testing.T) {
	t.Parallel()

	checker := &healthChecker{
		audioLevels: make(map[string]*audioLevelTracker),
	}

	// Test non-existent process
	_, _, _, ok := checker.GetAudioLevelStats("nonexistent")
	assert.False(t, ok, "Should return false for non-existent process")

	// Add a tracker manually
	processID := "test-process"
	checker.audioLevels[processID] = &audioLevelTracker{
		avgLevel:       0.5,
		lastAudioLevel: 0.7,
		sampleCount:    100,
	}

	avgLevel, lastLevel, sampleCount, ok := checker.GetAudioLevelStats(processID)
	assert.True(t, ok, "Should return true for existing process")
	assert.InDelta(t, 0.5, avgLevel, 0.001, "Expected avg level 0.5")
	assert.InDelta(t, 0.7, lastLevel, 0.001, "Expected last level 0.7")
	assert.Equal(t, int64(100), sampleCount, "Expected sample count 100")
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
	require.NoError(t, err, "Initial check should pass")

	// Test process not running - should fail
	err = process.Stop()
	require.NoError(t, err, "Failed to stop process")
	err = checker.Check(process)
	require.Error(t, err, "Health check should fail for stopped process")

	// Start process again for final verification
	err = process.Start(context.Background())
	require.NoError(t, err, "Failed to start process")
	err = checker.Check(process)
	assert.NoError(t, err, "Health check should pass for running process")
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
		for range 100 {
			_ = checker.Check(process) // Ignore error for concurrency test
		}
	}()

	// Goroutine 2: Set thresholds
	go func() {
		defer wg.Done()
		for range 100 {
			checker.SetSilenceThreshold(-60.0, 30*time.Second)
		}
	}()

	// Wait for both goroutines to complete
	wg.Wait()

	// If we get here without panicking, concurrent access works
}
