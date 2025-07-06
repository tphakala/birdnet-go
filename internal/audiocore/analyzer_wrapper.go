package audiocore

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logging"
)

// analyzeResult is used to pass results from the analysis goroutine
type analyzeResult struct {
	result AnalysisResult
	err    error
}

// analyzeResultPool reduces allocations for analyzeResult structs
var analyzeResultPool = sync.Pool{
	New: func() any {
		return &analyzeResult{}
	},
}

// SafeAnalyzerWrapper wraps an analyzer with timeout and deadlock prevention
type SafeAnalyzerWrapper struct {
	analyzer         Analyzer
	timeout          time.Duration
	logger          *slog.Logger
	resourceTracker *ResourceTracker
	
	// Metrics
	totalAnalyses    atomic.Int64
	timeoutCount     atomic.Int64
	errorCount       atomic.Int64
	avgAnalysisTime  atomic.Int64 // in microseconds
	
	// Circuit breaker
	circuitBreaker   *CircuitBreaker
	
	// Resource management
	analysisSemaphore chan struct{} // Limits concurrent analyses
	closed            atomic.Bool
	closeOnce         sync.Once
}

// SafeAnalyzerConfig contains configuration for the safe analyzer wrapper
type SafeAnalyzerConfig struct {
	Analyzer              Analyzer
	Timeout               time.Duration
	MaxConcurrentAnalyses int
	ResourceTracker       *ResourceTracker
	CircuitBreakerConfig  *CircuitBreakerConfig
}

// CircuitBreakerConfig configures the circuit breaker
type CircuitBreakerConfig struct {
	MaxFailures      int
	ResetTimeout     time.Duration
	HalfOpenRequests int
}

// NewSafeAnalyzerWrapper creates a new analyzer wrapper with safety features
func NewSafeAnalyzerWrapper(config *SafeAnalyzerConfig) *SafeAnalyzerWrapper {
	logger := logging.ForService("audiocore")
	if logger == nil {
		logger = slog.Default()
	}
	
	// Default timeout if not specified
	timeout := config.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	
	// Default max concurrent analyses
	maxConcurrent := config.MaxConcurrentAnalyses
	if maxConcurrent == 0 {
		maxConcurrent = 5
	}
	
	// Create circuit breaker
	var cbConfig *CircuitBreakerConfig
	if config.CircuitBreakerConfig != nil {
		cbConfig = config.CircuitBreakerConfig
	} else {
		cbConfig = &CircuitBreakerConfig{
			MaxFailures:      5,
			ResetTimeout:     30 * time.Second,
			HalfOpenRequests: 2,
		}
	}
	
	wrapper := &SafeAnalyzerWrapper{
		analyzer:          config.Analyzer,
		timeout:           timeout,
		logger:            logger.With("component", "safe_analyzer", "analyzer_id", config.Analyzer.ID()),
		resourceTracker:   config.ResourceTracker,
		analysisSemaphore: make(chan struct{}, maxConcurrent),
		circuitBreaker:    NewCircuitBreaker(cbConfig),
	}
	
	// Initialize semaphore
	for i := 0; i < maxConcurrent; i++ {
		wrapper.analysisSemaphore <- struct{}{}
	}
	
	// Track this wrapper if resource tracker is available
	if wrapper.resourceTracker != nil {
		wrapper.resourceTracker.Track(
			fmt.Sprintf("safe_analyzer_%s", config.Analyzer.ID()),
			"SafeAnalyzerWrapper",
			func() {
				_ = wrapper.Close()
			},
		)
	}
	
	return wrapper
}

// ID returns the wrapped analyzer's ID
func (w *SafeAnalyzerWrapper) ID() string {
	return w.analyzer.ID()
}

// Analyze processes audio data with timeout and deadlock prevention
func (w *SafeAnalyzerWrapper) Analyze(ctx context.Context, data *AudioData) (AnalysisResult, error) {
	if w.closed.Load() {
		return AnalysisResult{}, errors.New(nil).
			Component(ComponentAudioCore).
			Category(errors.CategoryState).
			Context("analyzer_id", w.ID()).
			Context("error", "analyzer is closed").
			Build()
	}
	
	// Check circuit breaker
	if !w.circuitBreaker.CanExecute() {
		w.logger.Warn("circuit breaker open, rejecting analysis request")
		return AnalysisResult{}, errors.New(nil).
			Component(ComponentAudioCore).
			Category(errors.CategoryLimit).
			Context("analyzer_id", w.ID()).
			Context("error", "circuit breaker open").
			Build()
	}
	
	// Acquire semaphore (with context)
	select {
	case <-w.analysisSemaphore:
		// Acquired
		defer func() {
			// Return semaphore
			w.analysisSemaphore <- struct{}{}
		}()
	case <-ctx.Done():
		return AnalysisResult{}, errors.New(ctx.Err()).
			Component(ComponentAudioCore).
			Category(errors.CategorySystem).
			Context("analyzer_id", w.ID()).
			Context("error", "context cancelled while waiting for analysis slot").
			Build()
	}
	
	// Create timeout context
	analyzeCtx, cancel := context.WithTimeout(ctx, w.timeout)
	defer cancel()
	
	// Create channel for result - using pointer to enable pooling
	resultChan := make(chan *analyzeResult, 1)
	
	// Track analysis time
	startTime := time.Now()
	
	// Run analysis in goroutine
	go func() {
		// Get result struct from pool
		res := analyzeResultPool.Get().(*analyzeResult)
		// Note: We don't defer Put here because the receiver needs to do it
		
		defer func() {
			if r := recover(); r != nil {
				res.err = errors.New(nil).
					Component(ComponentAudioCore).
					Category(errors.CategoryProcessing).
					Context("analyzer_id", w.ID()).
					Context("panic", fmt.Sprintf("%v", r)).
					Context("error", fmt.Sprintf("analyzer panic: %v", r)).
					Build()
				res.result = AnalysisResult{}
				
				resultChan <- res
			}
		}()
		
		res.result, res.err = w.analyzer.Analyze(analyzeCtx, data)
		resultChan <- res
	}()
	
	// Wait for result or timeout
	select {
	case result := <-resultChan:
		// Analysis completed
		duration := time.Since(startTime)
		w.updateMetrics(duration, result.err == nil)
		
		// Extract values before returning to pool
		analysisResult := result.result
		analysisErr := result.err
		
		// Clear and return to pool
		result.result = AnalysisResult{}
		result.err = nil
		analyzeResultPool.Put(result)
		
		if analysisErr != nil {
			w.circuitBreaker.RecordFailure()
			w.errorCount.Add(1)
			return AnalysisResult{}, analysisErr
		}
		
		w.circuitBreaker.RecordSuccess()
		return analysisResult, nil
		
	case <-analyzeCtx.Done():
		// Timeout or cancellation
		w.timeoutCount.Add(1)
		w.circuitBreaker.RecordFailure()
		
		// Log the timeout
		w.logger.Error("analysis timeout",
			"analyzer_id", w.ID(),
			"timeout", w.timeout,
			"data_duration", data.Duration)
		
		// Note: The goroutine might still be running, but we're abandoning it
		// This is why we use a buffered channel - to prevent goroutine leak
		
		return AnalysisResult{}, errors.New(analyzeCtx.Err()).
			Component(ComponentAudioCore).
			Category(errors.CategorySystem).
			Context("analyzer_id", w.ID()).
			Context("timeout", w.timeout).
			Context("error", "analysis timeout exceeded").
			Build()
	}
}

// GetRequiredFormat returns the wrapped analyzer's required format
func (w *SafeAnalyzerWrapper) GetRequiredFormat() AudioFormat {
	return w.analyzer.GetRequiredFormat()
}

// GetConfiguration returns the wrapped analyzer's configuration
func (w *SafeAnalyzerWrapper) GetConfiguration() AnalyzerConfig {
	return w.analyzer.GetConfiguration()
}

// Close releases resources
func (w *SafeAnalyzerWrapper) Close() error {
	var closeErr error
	
	w.closeOnce.Do(func() {
		w.closed.Store(true)
		
		// Close the wrapped analyzer
		closeErr = w.analyzer.Close()
		
		// Log final metrics
		w.logger.Info("analyzer wrapper closing",
			"total_analyses", w.totalAnalyses.Load(),
			"timeout_count", w.timeoutCount.Load(),
			"error_count", w.errorCount.Load(),
			"avg_analysis_time_us", w.avgAnalysisTime.Load())
		
		// Release from resource tracker
		if w.resourceTracker != nil {
			_ = w.resourceTracker.Release(fmt.Sprintf("safe_analyzer_%s", w.ID()))
		}
	})
	
	return closeErr
}

// updateMetrics updates analysis metrics
func (w *SafeAnalyzerWrapper) updateMetrics(duration time.Duration, success bool) {
	w.totalAnalyses.Add(1)
	
	// Update average analysis time (exponential moving average)
	newTime := duration.Microseconds()
	oldAvg := w.avgAnalysisTime.Load()
	if oldAvg == 0 {
		w.avgAnalysisTime.Store(newTime)
	} else {
		// EMA with alpha = 0.1
		newAvg := (oldAvg * 9 + newTime) / 10
		w.avgAnalysisTime.Store(newAvg)
	}
	
	// Track success/failure for future metrics
	_ = success // Currently unused but may be used for success rate tracking
}

// GetMetrics returns current metrics
func (w *SafeAnalyzerWrapper) GetMetrics() map[string]any {
	totalAnalyses := w.totalAnalyses.Load()
	timeoutCount := w.timeoutCount.Load()
	errorCount := w.errorCount.Load()
	
	// Calculate rates, avoiding division by zero
	var timeoutRate, errorRate float64
	if totalAnalyses > 0 {
		timeoutRate = float64(timeoutCount) / float64(totalAnalyses)
		errorRate = float64(errorCount) / float64(totalAnalyses)
	}
	
	return map[string]any{
		"total_analyses":       totalAnalyses,
		"timeout_count":        timeoutCount,
		"error_count":          errorCount,
		"avg_analysis_time_us": w.avgAnalysisTime.Load(),
		"timeout_rate":         timeoutRate,
		"error_rate":           errorRate,
		"circuit_breaker":      w.circuitBreaker.GetState(),
	}
}

// CircuitBreaker implements a simple circuit breaker pattern
type CircuitBreaker struct {
	maxFailures      int
	resetTimeout     time.Duration
	halfOpenRequests int
	
	mu               sync.RWMutex
	state            string // "closed", "open", "half-open"
	failures         int
	lastFailTime     time.Time
	halfOpenAttempts int
}

// NewCircuitBreaker creates a new circuit breaker
func NewCircuitBreaker(config *CircuitBreakerConfig) *CircuitBreaker {
	return &CircuitBreaker{
		maxFailures:      config.MaxFailures,
		resetTimeout:     config.ResetTimeout,
		halfOpenRequests: config.HalfOpenRequests,
		state:            "closed",
	}
}

// CanExecute checks if a request can be executed
func (cb *CircuitBreaker) CanExecute() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	
	switch cb.state {
	case "closed":
		return true
		
	case "open":
		// Check if we should transition to half-open
		if time.Since(cb.lastFailTime) >= cb.resetTimeout {
			cb.state = "half-open"
			cb.halfOpenAttempts = 0
			return true
		}
		return false
		
	case "half-open":
		// Allow limited requests in half-open state
		if cb.halfOpenAttempts < cb.halfOpenRequests {
			cb.halfOpenAttempts++
			return true
		}
		return false
		
	default:
		return false
	}
}

// RecordSuccess records a successful execution
func (cb *CircuitBreaker) RecordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	
	if cb.state == "half-open" {
		// Successful request in half-open state, close the circuit
		cb.state = "closed"
		cb.failures = 0
	}
}

// RecordFailure records a failed execution
func (cb *CircuitBreaker) RecordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	
	cb.failures++
	cb.lastFailTime = time.Now()
	
	switch cb.state {
	case "closed":
		if cb.failures >= cb.maxFailures {
			cb.state = "open"
		}
		
	case "half-open":
		// Failure in half-open state, reopen the circuit
		cb.state = "open"
	}
}

// GetState returns the current state
func (cb *CircuitBreaker) GetState() string {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.state
}

// Reset resets the circuit breaker
func (cb *CircuitBreaker) Reset() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	
	cb.state = "closed"
	cb.failures = 0
	cb.halfOpenAttempts = 0
}