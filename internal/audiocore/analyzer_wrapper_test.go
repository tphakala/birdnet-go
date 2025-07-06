package audiocore

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tphakala/birdnet-go/internal/errors"
)

// mockAnalyzer implements Analyzer for testing
type mockAnalyzer struct {
	id            string
	analyzeFunc   func(ctx context.Context, data *AudioData) (AnalysisResult, error)
	requiredFormat AudioFormat
	config        AnalyzerConfig
	closed        atomic.Bool
}

func (m *mockAnalyzer) ID() string {
	return m.id
}

func (m *mockAnalyzer) Analyze(ctx context.Context, data *AudioData) (AnalysisResult, error) {
	if m.analyzeFunc != nil {
		return m.analyzeFunc(ctx, data)
	}
	return AnalysisResult{}, nil
}

func (m *mockAnalyzer) GetRequiredFormat() AudioFormat {
	return m.requiredFormat
}

func (m *mockAnalyzer) GetConfiguration() AnalyzerConfig {
	return m.config
}

func (m *mockAnalyzer) Close() error {
	m.closed.Store(true)
	return nil
}

// TestAnalyzerTimeout tests that the wrapper properly times out long-running analyses
func TestAnalyzerTimeout(t *testing.T) {
	t.Parallel()

	// Create a mock analyzer that takes too long
	mock := &mockAnalyzer{
		id: "test-analyzer",
		analyzeFunc: func(ctx context.Context, data *AudioData) (AnalysisResult, error) {
			// Simulate a long-running analysis
			select {
			case <-time.After(5 * time.Second):
				return AnalysisResult{}, nil
			case <-ctx.Done():
				return AnalysisResult{}, ctx.Err()
			}
		},
	}

	// Create wrapper with short timeout
	wrapper := NewSafeAnalyzerWrapper(&SafeAnalyzerConfig{
		Analyzer: mock,
		Timeout:  100 * time.Millisecond,
	})
	defer func() { _ = wrapper.Close() }()

	// Test data
	data := &AudioData{
		Buffer: make([]byte, 1024),
	}

	// Analyze should timeout
	ctx := context.Background()
	start := time.Now()
	_, err := wrapper.Analyze(ctx, data)
	duration := time.Since(start)

	if err == nil {
		t.Error("expected timeout error, got nil")
	}

	// Check that we timed out quickly
	if duration > 200*time.Millisecond {
		t.Errorf("timeout took too long: %v", duration)
	}

	// Check timeout counter
	if wrapper.timeoutCount.Load() != 1 {
		t.Errorf("expected timeout count 1, got %d", wrapper.timeoutCount.Load())
	}
}

// TestAnalyzerContextCancellation tests context cancellation handling
func TestAnalyzerContextCancellation(t *testing.T) {
	t.Parallel()

	// Create a mock analyzer
	mock := &mockAnalyzer{
		id: "test-analyzer",
		analyzeFunc: func(ctx context.Context, data *AudioData) (AnalysisResult, error) {
			// Wait for context cancellation
			<-ctx.Done()
			return AnalysisResult{}, ctx.Err()
		},
	}

	wrapper := NewSafeAnalyzerWrapper(&SafeAnalyzerConfig{
		Analyzer: mock,
		Timeout:  1 * time.Second,
	})
	defer func() { _ = wrapper.Close() }()

	// Create cancellable context
	ctx, cancel := context.WithCancel(context.Background())

	// Start analysis in goroutine
	var wg sync.WaitGroup
	wg.Add(1)
	
	var analyzeErr error
	go func() {
		defer wg.Done()
		_, analyzeErr = wrapper.Analyze(ctx, &AudioData{Buffer: make([]byte, 1024)})
	}()

	// Cancel context after short delay
	time.Sleep(50 * time.Millisecond)
	cancel()

	// Wait for analysis to complete
	wg.Wait()

	// Should have received cancellation error
	if analyzeErr == nil {
		t.Error("expected cancellation error, got nil")
	}
}

// TestAnalyzerPanicRecovery tests panic recovery
func TestAnalyzerPanicRecovery(t *testing.T) {
	t.Parallel()

	// Create a mock analyzer that panics
	mock := &mockAnalyzer{
		id: "test-analyzer",
		analyzeFunc: func(ctx context.Context, data *AudioData) (AnalysisResult, error) {
			panic("test panic")
		},
	}

	wrapper := NewSafeAnalyzerWrapper(&SafeAnalyzerConfig{
		Analyzer: mock,
		Timeout:  1 * time.Second,
	})
	defer func() { _ = wrapper.Close() }()

	// Analyze should recover from panic
	_, err := wrapper.Analyze(context.Background(), &AudioData{Buffer: make([]byte, 1024)})

	if err == nil {
		t.Error("expected error from panic, got nil")
	}

	// Check that error mentions panic
	if errStr := err.Error(); !strings.Contains(errStr, "panic") {
		t.Error("error should mention panic")
	}
}

// TestAnalyzerConcurrentLimit tests concurrent analysis limiting
func TestAnalyzerConcurrentLimit(t *testing.T) {
	t.Parallel()

	activeCount := atomic.Int32{}
	maxActive := atomic.Int32{}

	// Create a mock analyzer that tracks concurrent executions
	mock := &mockAnalyzer{
		id: "test-analyzer",
		analyzeFunc: func(ctx context.Context, data *AudioData) (AnalysisResult, error) {
			// Increment active count
			current := activeCount.Add(1)
			
			// Update max if needed
			for {
				maxVal := maxActive.Load()
				if current <= maxVal || maxActive.CompareAndSwap(maxVal, current) {
					break
				}
			}

			// Simulate some work
			time.Sleep(100 * time.Millisecond)

			// Decrement active count
			activeCount.Add(-1)

			return AnalysisResult{}, nil
		},
	}

	// Create wrapper with limit of 2 concurrent analyses
	wrapper := NewSafeAnalyzerWrapper(&SafeAnalyzerConfig{
		Analyzer:              mock,
		Timeout:               1 * time.Second,
		MaxConcurrentAnalyses: 2,
	})
	defer func() { _ = wrapper.Close() }()

	// Start 5 concurrent analyses
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = wrapper.Analyze(context.Background(), &AudioData{Buffer: make([]byte, 1024)})
		}()
	}

	// Wait for all to complete
	wg.Wait()

	// Check that we never exceeded the limit
	if maxActive.Load() > 2 {
		t.Errorf("exceeded concurrent limit: max active was %d, expected <= 2", maxActive.Load())
	}
}

// TestCircuitBreaker tests circuit breaker functionality
func TestCircuitBreaker(t *testing.T) {
	t.Parallel()

	failureCount := atomic.Int32{}

	// Create a mock analyzer that fails initially
	mock := &mockAnalyzer{
		id: "test-analyzer",
		analyzeFunc: func(ctx context.Context, data *AudioData) (AnalysisResult, error) {
			count := failureCount.Add(1)
			if count <= 5 {
				return AnalysisResult{}, errors.New(nil).
					Component(ComponentAudioCore).
					Category(errors.CategoryProcessing).
					Context("error", "simulated failure").
					Build()
			}
			// Succeed after 5 failures
			return AnalysisResult{}, nil
		},
	}

	// Create wrapper with circuit breaker
	wrapper := NewSafeAnalyzerWrapper(&SafeAnalyzerConfig{
		Analyzer: mock,
		Timeout:  1 * time.Second,
		CircuitBreakerConfig: &CircuitBreakerConfig{
			MaxFailures:      3,
			ResetTimeout:     100 * time.Millisecond,
			HalfOpenRequests: 2,
		},
	})
	defer func() { _ = wrapper.Close() }()

	ctx := context.Background()
	data := &AudioData{Buffer: make([]byte, 1024)}

	// First 3 requests should fail and open the circuit
	for i := 0; i < 3; i++ {
		_, err := wrapper.Analyze(ctx, data)
		if err == nil {
			t.Errorf("expected failure %d, got success", i+1)
		}
	}

	// Circuit should be open now
	if wrapper.circuitBreaker.GetState() != "open" {
		t.Errorf("expected circuit breaker to be open, got %s", wrapper.circuitBreaker.GetState())
	}

	// Next request should be rejected immediately
	_, err := wrapper.Analyze(ctx, data)
	if err == nil {
		t.Error("expected circuit breaker rejection, got success")
	}

	// Wait for reset timeout
	time.Sleep(150 * time.Millisecond)

	// Circuit should transition to half-open and allow limited requests
	_, err = wrapper.Analyze(ctx, data)
	if err == nil {
		t.Error("expected failure in half-open state")
	}

	// Circuit should be open again after failure in half-open
	if wrapper.circuitBreaker.GetState() != "open" {
		t.Errorf("expected circuit breaker to be open after half-open failure, got %s", wrapper.circuitBreaker.GetState())
	}
}

// TestAnalyzerMetrics tests metric collection
func TestAnalyzerMetrics(t *testing.T) {
	t.Parallel()

	successCount := atomic.Int32{}

	// Create a mock analyzer that alternates between success and failure
	mock := &mockAnalyzer{
		id: "test-analyzer",
		analyzeFunc: func(ctx context.Context, data *AudioData) (AnalysisResult, error) {
			count := successCount.Add(1)
			if count%2 == 0 {
				return AnalysisResult{}, errors.New(nil).
					Component(ComponentAudioCore).
					Category(errors.CategoryProcessing).
					Context("error", "simulated failure").
					Build()
			}
			return AnalysisResult{
				Detections: []Detection{{Label: "test", Confidence: 0.9}},
			}, nil
		},
	}

	wrapper := NewSafeAnalyzerWrapper(&SafeAnalyzerConfig{
		Analyzer: mock,
		Timeout:  1 * time.Second,
	})
	defer func() { _ = wrapper.Close() }()

	// Run several analyses
	ctx := context.Background()
	data := &AudioData{Buffer: make([]byte, 1024)}

	for i := 0; i < 10; i++ {
		_, _ = wrapper.Analyze(ctx, data)
	}

	// Check metrics
	metrics := wrapper.GetMetrics()
	
	if total, ok := metrics["total_analyses"].(int64); !ok || total != 10 {
		t.Errorf("expected 10 total analyses, got %v", metrics["total_analyses"])
	}

	if errorCount, ok := metrics["error_count"].(int64); !ok || errorCount != 5 {
		t.Errorf("expected 5 errors, got %v", metrics["error_count"])
	}

	if avgTime, ok := metrics["avg_analysis_time_us"].(int64); !ok || avgTime == 0 {
		t.Errorf("expected non-zero average analysis time, got %v", metrics["avg_analysis_time_us"])
	}
}

// TestAnalyzerResourceCleanup tests proper resource cleanup
func TestAnalyzerResourceCleanup(t *testing.T) {
	t.Parallel()

	// Create resource tracker
	tracker := NewResourceTracker()

	// Create mock analyzer
	mock := &mockAnalyzer{
		id: "test-analyzer",
		analyzeFunc: func(ctx context.Context, data *AudioData) (AnalysisResult, error) {
			return AnalysisResult{}, nil
		},
	}

	// Create wrapper with resource tracking
	wrapper := NewSafeAnalyzerWrapper(&SafeAnalyzerConfig{
		Analyzer:        mock,
		Timeout:         1 * time.Second,
		ResourceTracker: tracker,
	})

	// Run some analyses
	ctx := context.Background()
	data := &AudioData{Buffer: make([]byte, 1024)}

	for i := 0; i < 5; i++ {
		_, _ = wrapper.Analyze(ctx, data)
	}

	// Close wrapper
	err := wrapper.Close()
	if err != nil {
		t.Errorf("unexpected error closing wrapper: %v", err)
	}

	// Verify mock analyzer was closed
	if !mock.closed.Load() {
		t.Error("mock analyzer was not closed")
	}

	// Verify wrapper rejects new analyses
	_, err = wrapper.Analyze(ctx, data)
	if err == nil {
		t.Error("expected error analyzing after close, got nil")
	}

	// Check resource tracker stats
	stats := tracker.Stats()
	if released, ok := stats["total_released"].(int64); !ok || released == 0 {
		t.Error("resource was not properly released")
	}
}

