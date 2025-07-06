package audiocore

import (
	"context"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/errors"
)

// mockAnalyzer is a test analyzer implementation shared across tests
type mockAnalyzer struct {
	id             string
	requiredFormat AudioFormat
	analyzeFunc    func(ctx context.Context, data *AudioData) (AnalysisResult, error)
	closed         atomic.Bool
}

func (m *mockAnalyzer) ID() string {
	if m.id == "" {
		return "mock-analyzer"
	}
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
	return AnalyzerConfig{Type: "mock"}
}

func (m *mockAnalyzer) Close() error {
	m.closed.Store(true)
	return nil
}

// TestAnalyzerTimeout tests timeout handling
func TestAnalyzerTimeout(t *testing.T) {
	t.Parallel()

	// Create a mock analyzer that takes too long
	mock := &mockAnalyzer{
		id: "test-analyzer",
		requiredFormat: AudioFormat{
			SampleRate: 48000,
			Channels:   1,
			BitDepth:   16,
			Encoding:   "pcm_s16le",
		},
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

	require.Error(t, err, "expected timeout error")

	// Check that we timed out quickly (more flexible for CI environments)
	assert.LessOrEqual(t, duration, 500*time.Millisecond, "timeout took unexpectedly long: %v", duration)

	// Check timeout counter
	assert.Equal(t, int64(1), wrapper.timeoutCount.Load(), "expected timeout count 1")
}

// TestAnalyzerContextCancellation tests context cancellation handling
func TestAnalyzerContextCancellation(t *testing.T) {
	t.Parallel()

	// Create a mock analyzer
	mock := &mockAnalyzer{
		id: "test-analyzer",
		requiredFormat: AudioFormat{
			SampleRate: 48000,
			Channels:   1,
			BitDepth:   16,
			Encoding:   "pcm_s16le",
		},
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

	// Give analysis time to start
	time.Sleep(10 * time.Millisecond)

	// Cancel context
	cancel()

	// Wait for analysis to complete
	wg.Wait()

	// Should have error
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
		requiredFormat: AudioFormat{
			SampleRate: 48000,
			Channels:   1,
			BitDepth:   16,
			Encoding:   "pcm_s16le",
		},
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
		t.Error("expected error from panic recovery, got nil")
	}

	// Check error count
	if wrapper.errorCount.Load() != 1 {
		t.Errorf("expected error count 1, got %d", wrapper.errorCount.Load())
	}
}

// TestAnalyzerConcurrentLimit tests concurrent analysis limit
func TestAnalyzerConcurrentLimit(t *testing.T) {
	t.Parallel()

	// Track concurrent executions
	var activeCount atomic.Int32
	maxActive := atomic.Int32{}

	// Create a mock analyzer that tracks concurrency
	mock := &mockAnalyzer{
		id: "test-analyzer",
		requiredFormat: AudioFormat{
			SampleRate: 48000,
			Channels:   1,
			BitDepth:   16,
			Encoding:   "pcm_s16le",
		},
		analyzeFunc: func(ctx context.Context, data *AudioData) (AnalysisResult, error) {
			current := activeCount.Add(1)
			defer activeCount.Add(-1)

			// Track max concurrent
			for {
				maxVal := maxActive.Load()
				if current <= maxVal || maxActive.CompareAndSwap(maxVal, current) {
					break
				}
			}

			// Simulate work
			time.Sleep(100 * time.Millisecond)
			return AnalysisResult{}, nil
		},
	}

	// Create wrapper with limit of 2 concurrent analyses
	wrapper := NewSafeAnalyzerWrapper(&SafeAnalyzerConfig{
		Analyzer:              mock,
		Timeout:               1 * time.Second,
		Workers: 2,
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
		requiredFormat: AudioFormat{
			SampleRate: 48000,
			Channels:   1,
			BitDepth:   16,
			Encoding:   "pcm_s16le",
		},
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

	// Wait for circuit to transition to half-open
	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		if wrapper.circuitBreaker.GetState() == "half-open" {
			break
		}
		runtime.Gosched()
	}

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
		requiredFormat: AudioFormat{
			SampleRate: 48000,
			Channels:   1,
			BitDepth:   16,
			Encoding:   "pcm_s16le",
		},
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
		// Small delay to ensure timing is captured
		time.Sleep(5 * time.Millisecond)
	}

	// Check metrics
	metrics := wrapper.GetMetrics()
	
	if total, ok := metrics["total_analyses"].(int64); !ok || total != 10 {
		t.Errorf("expected 10 total analyses, got %v", metrics["total_analyses"])
	}

	if errorCount, ok := metrics["error_count"].(int64); !ok || errorCount != 5 {
		t.Errorf("expected 5 errors, got %v", metrics["error_count"])
	}

	// Check timing percentiles instead of average
	if percentiles, ok := metrics["timing_percentiles"].(map[string]int64); ok {
		if percentiles["p50"] == 0 {
			t.Error("expected non-zero p50 timing")
		}
	} else {
		t.Error("expected timing_percentiles in metrics")
	}
}

// TestAnalyzerResourceCleanup tests proper resource cleanup
func TestAnalyzerResourceCleanup(t *testing.T) {
	t.Parallel()

	// Create resource tracker
	tracker := NewResourceTracker()
	defer func() { _ = tracker.Close() }()

	// Create mock analyzer
	mock := &mockAnalyzer{
		id: "test-analyzer",
		requiredFormat: AudioFormat{
			SampleRate: 48000,
			Channels:   1,
			BitDepth:   16,
			Encoding:   "pcm_s16le",
		},
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

// TestAnalyzerWorkerPool tests the worker pool functionality
func TestAnalyzerWorkerPool(t *testing.T) {
	t.Parallel()

	// Create a mock analyzer that tracks concurrent executions
	var activeCount atomic.Int32
	maxActive := atomic.Int32{}
	
	mock := &mockAnalyzer{
		id: "test-analyzer",
		requiredFormat: AudioFormat{
			SampleRate: 48000,
			Channels:   1,
			BitDepth:   16,
			Encoding:   "pcm_s16le",
		},
		analyzeFunc: func(ctx context.Context, data *AudioData) (AnalysisResult, error) {
			current := activeCount.Add(1)
			defer activeCount.Add(-1)
			
			// Track max concurrent
			for {
				maxVal := maxActive.Load()
				if current <= maxVal || maxActive.CompareAndSwap(maxVal, current) {
					break
				}
			}
			
			// Simulate work
			time.Sleep(50 * time.Millisecond)
			return AnalysisResult{}, nil
		},
	}

	// Create wrapper with 3 workers
	wrapper := NewSafeAnalyzerWrapper(&SafeAnalyzerConfig{
		Analyzer: mock,
		Timeout:  1 * time.Second,
		Workers:  3,
	})
	defer func() { _ = wrapper.Close() }()

	// Submit 10 concurrent requests
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = wrapper.Analyze(context.Background(), &AudioData{Buffer: make([]byte, 1024)})
		}()
	}

	// Wait for all to complete
	wg.Wait()

	// Check that we never exceeded the worker limit
	if maxActive.Load() > 3 {
		t.Errorf("exceeded worker limit: max active was %d, expected <= 3", maxActive.Load())
	}

	// Check metrics
	metrics := wrapper.GetMetrics()
	if total, ok := metrics["total_analyses"].(int64); !ok || total != 10 {
		t.Errorf("expected 10 total analyses, got %v", metrics["total_analyses"])
	}

	// Check worker pool metrics
	if workerPool, ok := metrics["worker_pool"].(map[string]any); ok {
		if workers, ok := workerPool["workers"].(int); !ok || workers != 3 {
			t.Errorf("expected 3 workers, got %v", workerPool["workers"])
		}
	} else {
		t.Error("expected worker_pool metrics")
	}
}

// TestAnalyzerPercentileMetrics tests percentile tracking
func TestAnalyzerPercentileMetrics(t *testing.T) {
	t.Parallel()

	// Create a mock analyzer with predictable timing
	analysisTimes := []time.Duration{
		10 * time.Millisecond,
		20 * time.Millisecond,
		30 * time.Millisecond,
		40 * time.Millisecond,
		50 * time.Millisecond,
	}
	
	callCount := 0
	mock := &mockAnalyzer{
		id: "test-analyzer",
		requiredFormat: AudioFormat{
			SampleRate: 48000,
			Channels:   1,
			BitDepth:   16,
			Encoding:   "pcm_s16le",
		},
		analyzeFunc: func(ctx context.Context, data *AudioData) (AnalysisResult, error) {
			if callCount < len(analysisTimes) {
				time.Sleep(analysisTimes[callCount])
				callCount++
			}
			return AnalysisResult{}, nil
		},
	}

	wrapper := NewSafeAnalyzerWrapper(&SafeAnalyzerConfig{
		Analyzer:         mock,
		Timeout:          1 * time.Second,
		TimingBufferSize: 100,
	})
	defer func() { _ = wrapper.Close() }()

	// Run analyses
	for i := 0; i < 5; i++ {
		_, _ = wrapper.Analyze(context.Background(), &AudioData{Buffer: make([]byte, 1024)})
	}

	// Check percentiles
	metrics := wrapper.GetMetrics()
	if percentiles, ok := metrics["timing_percentiles"].(map[string]int64); ok {
		// P50 should be around 30ms (middle value)
		p50Ms := percentiles["p50"] / 1000
		if p50Ms < 25 || p50Ms > 35 {
			t.Errorf("expected p50 around 30ms, got %dms", p50Ms)
		}
		
		// P90 should be around 50ms
		p90Ms := percentiles["p90"] / 1000
		if p90Ms < 45 || p90Ms > 55 {
			t.Errorf("expected p90 around 50ms, got %dms", p90Ms)
		}
	} else {
		t.Error("expected timing_percentiles in metrics")
	}
}

// TestEnhancedCircuitBreaker tests enhanced circuit breaker features
func TestEnhancedCircuitBreaker(t *testing.T) {
	t.Parallel()

	// Test per-type failure thresholds
	t.Run("PerTypeThresholds", func(t *testing.T) {
		t.Parallel()
		
		errorCount := 0
		mock := &mockAnalyzer{
			id: "test-analyzer",
			requiredFormat: AudioFormat{
				SampleRate: 48000,
				Channels:   1,
				BitDepth:   16,
				Encoding:   "pcm_s16le",
			},
			analyzeFunc: func(ctx context.Context, data *AudioData) (AnalysisResult, error) {
				errorCount++
				if errorCount <= 2 {
					// First 2 calls timeout
					<-ctx.Done()
					return AnalysisResult{}, ctx.Err()
				}
				// Rest succeed
				return AnalysisResult{}, nil
			},
		}

		wrapper := NewSafeAnalyzerWrapper(&SafeAnalyzerConfig{
			Analyzer: mock,
			Timeout:  50 * time.Millisecond,
			CircuitBreakerConfig: &CircuitBreakerConfig{
				MaxFailures:      10, // High general limit
				ResetTimeout:     100 * time.Millisecond,
				HalfOpenRequests: 2,
				FailureThresholds: map[string]int{
					"timeout": 2, // Low timeout threshold
					"error":   5,
				},
			},
		})
		defer func() { _ = wrapper.Close() }()

		// First two requests should timeout and open circuit
		for i := 0; i < 2; i++ {
			_, _ = wrapper.Analyze(context.Background(), &AudioData{Buffer: make([]byte, 1024)})
		}

		// Circuit should be open now
		cbMetrics := wrapper.circuitBreaker.GetMetrics()
		if state, ok := cbMetrics["state"].(string); !ok || state != "open" {
			t.Errorf("expected circuit breaker state 'open', got %v", state)
		}

		// Check failure breakdown
		if failures, ok := cbMetrics["failures_by_type"].(map[string]int); ok {
			if failures["timeout"] != 2 {
				t.Errorf("expected 2 timeout failures, got %d", failures["timeout"])
			}
		} else {
			t.Error("expected failures_by_type in circuit breaker metrics")
		}
	})

	// Test gradual recovery
	t.Run("GradualRecovery", func(t *testing.T) {
		t.Parallel()
		
		failCount := 0
		mock := &mockAnalyzer{
			id: "test-analyzer",
			requiredFormat: AudioFormat{
				SampleRate: 48000,
				Channels:   1,
				BitDepth:   16,
				Encoding:   "pcm_s16le",
			},
			analyzeFunc: func(ctx context.Context, data *AudioData) (AnalysisResult, error) {
				failCount++
				if failCount <= 3 {
					// First 3 calls fail to open circuit
					return AnalysisResult{}, errors.New(nil).
						Component(ComponentAudioCore).
						Category(errors.CategoryProcessing).
						Context("error", "simulated failure").
						Build()
				}
				// Rest succeed
				return AnalysisResult{}, nil
			},
		}

		wrapper := NewSafeAnalyzerWrapper(&SafeAnalyzerConfig{
			Analyzer: mock,
			Timeout:  1 * time.Second,
			CircuitBreakerConfig: &CircuitBreakerConfig{
				MaxFailures:      3,
				ResetTimeout:     50 * time.Millisecond,
				HalfOpenRequests: 6,
				RecoverySteps:    3, // 3-step recovery
			},
		})
		defer func() { _ = wrapper.Close() }()

		// Open the circuit
		for i := 0; i < 3; i++ {
			_, _ = wrapper.Analyze(context.Background(), &AudioData{Buffer: make([]byte, 1024)})
		}

		// Wait for reset timeout
		time.Sleep(60 * time.Millisecond)

		// Should allow gradual recovery
		successCount := 0
		for i := 0; i < 10; i++ {
			_, err := wrapper.Analyze(context.Background(), &AudioData{Buffer: make([]byte, 1024)})
			if err == nil {
				successCount++
			}
		}

		// Should have allowed at least 6 requests (configured HalfOpenRequests)
		// Once recovery completes, circuit closes and allows all requests
		if successCount < 6 {
			t.Errorf("expected at least 6 successful requests during recovery, got %d", successCount)
		}

		// Check recovery progress
		cbMetrics := wrapper.circuitBreaker.GetMetrics()
		if recovery, ok := cbMetrics["recovery_progress"].(map[string]any); ok {
			if steps, ok := recovery["total_steps"].(int); !ok || steps != 3 {
				t.Errorf("expected 3 recovery steps, got %v", recovery["total_steps"])
			}
		}
	})
}

// FuzzAnalyzerWrapper tests the analyzer wrapper with fuzzing
func FuzzAnalyzerWrapper(f *testing.F) {
	// Add seed corpus
	f.Add([]byte("test data"), 100, 5, 1000)
	f.Add([]byte(""), 50, 10, 500)
	f.Add(make([]byte, 1024), 200, 3, 2000)
	f.Add(make([]byte, 1024*1024), 1000, 1, 5000)

	f.Fuzz(func(t *testing.T, data []byte, timeoutMs int, workers int, bufferSize int) {
		// Sanitize inputs
		if timeoutMs < 1 {
			timeoutMs = 1
		}
		if timeoutMs > 5000 {
			timeoutMs = 5000
		}
		if workers < 1 {
			workers = 1
		}
		if workers > 100 {
			workers = 100
		}
		if bufferSize < 1 {
			bufferSize = 1
		}
		if bufferSize > 10000 {
			bufferSize = 10000
		}

		// Create mock analyzer
		mock := &mockAnalyzer{
			id: "fuzz-analyzer",
			requiredFormat: AudioFormat{
				SampleRate: 48000,
				Channels:   1,
				BitDepth:   16,
				Encoding:   "pcm_s16le",
			},
			analyzeFunc: func(ctx context.Context, audioData *AudioData) (AnalysisResult, error) {
				// Simulate processing
				if len(audioData.Buffer) > 1024*1024 {
					return AnalysisResult{}, errors.New(nil).
						Component(ComponentAudioCore).
						Category(errors.CategoryValidation).
						Context("error", "buffer too large").
						Build()
				}
				return AnalysisResult{
					Detections: []Detection{{Label: "fuzz", Confidence: 0.5}},
				}, nil
			},
		}

		// Create wrapper with fuzzed config
		wrapper := NewSafeAnalyzerWrapper(&SafeAnalyzerConfig{
			Analyzer:         mock,
			Timeout:          time.Duration(timeoutMs) * time.Millisecond,
			Workers:          workers,
			TimingBufferSize: bufferSize,
		})
		defer func() { _ = wrapper.Close() }()

		// Test with fuzzed data
		audioData := &AudioData{
			Buffer:    data,
			Format:    mock.requiredFormat,
			Timestamp: time.Now(),
			Duration:  100 * time.Millisecond,
			SourceID:  "fuzz-source",
		}

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		// Should not panic
		_, _ = wrapper.Analyze(ctx, audioData)

		// Check metrics are valid
		metrics := wrapper.GetMetrics()
		assert.NotNil(t, metrics)
		assert.Contains(t, metrics, "total_analyses")
		assert.Contains(t, metrics, "worker_pool")
	})
}