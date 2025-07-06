package audiocore

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"sort"
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

// analyzeTask represents a work item for the worker pool
type analyzeTask struct {
	ctx        context.Context
	data       *AudioData
	resultChan chan *analyzeResult
	startTime  time.Time
}

// SafeAnalyzerWrapper wraps an analyzer with timeout and deadlock prevention
type SafeAnalyzerWrapper struct {
	analyzer         Analyzer
	timeout          time.Duration
	logger          *slog.Logger
	resourceTracker *ResourceTracker
	
	// Worker pool
	workers          int
	taskQueue        chan *analyzeTask
	workerWg         sync.WaitGroup
	
	// Enhanced Metrics
	totalAnalyses    atomic.Int64
	timeoutCount     atomic.Int64
	errorCount       atomic.Int64
	successCount     atomic.Int64
	
	// Percentile tracking for analysis times
	timingMutex      sync.RWMutex
	analysisTimes    []int64 // microseconds, circular buffer
	timingIndex      int
	timingSize       int
	
	// Enhanced circuit breaker
	circuitBreaker   *CircuitBreaker
	
	// Resource management
	closed            atomic.Bool
	closeOnce         sync.Once
	closeChan         chan struct{}
}

// SafeAnalyzerConfig contains configuration for the safe analyzer wrapper
type SafeAnalyzerConfig struct {
	Analyzer              Analyzer
	Timeout               time.Duration
	Workers               int // Number of worker goroutines (replaces MaxConcurrentAnalyses)
	TimingBufferSize      int // Size of timing history for percentiles
	ResourceTracker       *ResourceTracker
	CircuitBreakerConfig  *CircuitBreakerConfig
}

// CircuitBreakerConfig configures the enhanced circuit breaker
type CircuitBreakerConfig struct {
	MaxFailures       int
	ResetTimeout      time.Duration
	HalfOpenRequests  int
	FailureThresholds map[string]int // Per-type thresholds
	RecoverySteps     int            // Number of steps for gradual recovery
}

// NewSafeAnalyzerWrapper creates a new analyzer wrapper with safety features
func NewSafeAnalyzerWrapper(config *SafeAnalyzerConfig) *SafeAnalyzerWrapper {
	logger := logging.ForService("audiocore")
	if logger == nil {
		logger = slog.Default()
	}
	
	// Defaults
	if config.Timeout == 0 {
		config.Timeout = 30 * time.Second
	}
	if config.Workers == 0 {
		config.Workers = 5
	}
	if config.TimingBufferSize == 0 {
		config.TimingBufferSize = 1000
	}
	
	// Default circuit breaker config
	cbConfig := config.CircuitBreakerConfig
	if cbConfig == nil {
		cbConfig = &CircuitBreakerConfig{
			MaxFailures:      5,
			ResetTimeout:     30 * time.Second,
			HalfOpenRequests: 2,
			FailureThresholds: map[string]int{
				"timeout": 3,
				"error":   5,
				"panic":   1,
			},
			RecoverySteps: 3,
		}
	}
	
	wrapper := &SafeAnalyzerWrapper{
		analyzer:        config.Analyzer,
		timeout:         config.Timeout,
		logger:          logger.With("component", "safe_analyzer", "analyzer_id", config.Analyzer.ID()),
		resourceTracker: config.ResourceTracker,
		workers:         config.Workers,
		taskQueue:       make(chan *analyzeTask, config.Workers*2),
		analysisTimes:   make([]int64, config.TimingBufferSize),
		timingSize:      config.TimingBufferSize,
		circuitBreaker:  NewCircuitBreaker(cbConfig),
		closeChan:       make(chan struct{}),
	}
	
	// Start worker pool
	wrapper.startWorkers()
	
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

// startWorkers starts the worker pool
func (w *SafeAnalyzerWrapper) startWorkers() {
	for i := 0; i < w.workers; i++ {
		w.workerWg.Add(1)
		go w.worker(i)
	}
	
	w.logger.Info("started analyzer worker pool", "workers", w.workers)
}

// worker is the main worker goroutine
func (w *SafeAnalyzerWrapper) worker(id int) {
	defer w.workerWg.Done()
	
	for {
		select {
		case task := <-w.taskQueue:
			w.processTask(task)
			
		case <-w.closeChan:
			w.logger.Debug("worker shutting down", "worker_id", id)
			return
		}
	}
}

// processTask handles a single analysis task
func (w *SafeAnalyzerWrapper) processTask(task *analyzeTask) {
	// Get result struct from pool
	res := analyzeResultPool.Get().(*analyzeResult)
	
	// Ensure we always send a result
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
			w.circuitBreaker.RecordFailure("panic")
		}
		
		// Always try to send result
		select {
		case task.resultChan <- res:
		case <-task.ctx.Done():
			// Context cancelled, return to pool
			res.result = AnalysisResult{}
			res.err = nil
			analyzeResultPool.Put(res)
		}
	}()
	
	// Run the analysis
	res.result, res.err = w.analyzer.Analyze(task.ctx, task.data)
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
	
	// Create timeout context
	analyzeCtx, cancel := context.WithTimeout(ctx, w.timeout)
	defer cancel()
	
	// Create task
	task := &analyzeTask{
		ctx:        analyzeCtx,
		data:       data,
		resultChan: make(chan *analyzeResult, 1),
		startTime:  time.Now(),
	}
	
	// Submit task to worker pool
	select {
	case w.taskQueue <- task:
		// Task submitted
	case <-ctx.Done():
		return AnalysisResult{}, errors.New(ctx.Err()).
			Component(ComponentAudioCore).
			Category(errors.CategorySystem).
			Context("analyzer_id", w.ID()).
			Context("error", "context cancelled before task submission").
			Build()
	}
	
	// Wait for result
	select {
	case result := <-task.resultChan:
		// Analysis completed
		duration := time.Since(task.startTime)
		w.recordTiming(duration)
		
		// Extract values before returning to pool
		analysisResult := result.result
		analysisErr := result.err
		
		// Clear and return to pool
		result.result = AnalysisResult{}
		result.err = nil
		analyzeResultPool.Put(result)
		
		// Update metrics
		w.totalAnalyses.Add(1)
		if analysisErr != nil {
			w.errorCount.Add(1)
			w.circuitBreaker.RecordFailure("error")
			return AnalysisResult{}, analysisErr
		}
		
		w.successCount.Add(1)
		w.circuitBreaker.RecordSuccess()
		return analysisResult, nil
		
	case <-analyzeCtx.Done():
		// Timeout
		w.timeoutCount.Add(1)
		w.totalAnalyses.Add(1)
		w.circuitBreaker.RecordFailure("timeout")
		
		w.logger.Error("analysis timeout",
			"analyzer_id", w.ID(),
			"timeout", w.timeout,
			"data_duration", data.Duration)
		
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

// Close releases resources and shuts down worker pool
func (w *SafeAnalyzerWrapper) Close() error {
	var closeErr error
	
	w.closeOnce.Do(func() {
		w.closed.Store(true)
		
		// Signal workers to stop
		close(w.closeChan)
		
		// Wait for workers to finish
		w.workerWg.Wait()
		
		// Close task queue
		close(w.taskQueue)
		
		// Close the wrapped analyzer
		closeErr = w.analyzer.Close()
		
		// Log final metrics
		w.logger.Info("analyzer wrapper closing",
			"metrics", w.GetMetrics())
		
		// Release from resource tracker
		if w.resourceTracker != nil {
			_ = w.resourceTracker.Release(fmt.Sprintf("safe_analyzer_%s", w.ID()))
		}
	})
	
	return closeErr
}

// recordTiming records analysis duration for percentile calculation
func (w *SafeAnalyzerWrapper) recordTiming(duration time.Duration) {
	w.timingMutex.Lock()
	defer w.timingMutex.Unlock()
	
	w.analysisTimes[w.timingIndex] = duration.Microseconds()
	w.timingIndex = (w.timingIndex + 1) % w.timingSize
}

// getPercentiles calculates timing percentiles
func (w *SafeAnalyzerWrapper) getPercentiles() map[string]int64 {
	w.timingMutex.RLock()
	defer w.timingMutex.RUnlock()
	
	// Copy non-zero times
	var times []int64
	for _, t := range w.analysisTimes {
		if t > 0 {
			times = append(times, t)
		}
	}
	
	if len(times) == 0 {
		return map[string]int64{
			"p50": 0,
			"p90": 0,
			"p95": 0,
			"p99": 0,
		}
	}
	
	// Sort times
	sort.Slice(times, func(i, j int) bool {
		return times[i] < times[j]
	})
	
	// Calculate percentiles
	p50Idx := int(math.Ceil(float64(len(times))*0.5)) - 1
	p90Idx := int(math.Ceil(float64(len(times))*0.9)) - 1
	p95Idx := int(math.Ceil(float64(len(times))*0.95)) - 1
	p99Idx := int(math.Ceil(float64(len(times))*0.99)) - 1
	
	return map[string]int64{
		"p50": times[p50Idx],
		"p90": times[p90Idx],
		"p95": times[p95Idx],
		"p99": times[p99Idx],
	}
}

// GetMetrics returns enhanced metrics with percentiles and worker pool status
func (w *SafeAnalyzerWrapper) GetMetrics() map[string]any {
	total := w.totalAnalyses.Load()
	timeouts := w.timeoutCount.Load()
	errorCount := w.errorCount.Load()
	successes := w.successCount.Load()
	
	// Calculate rates
	var timeoutRate, errorRate, successRate float64
	if total > 0 {
		timeoutRate = float64(timeouts) / float64(total)
		errorRate = float64(errorCount) / float64(total)
		successRate = float64(successes) / float64(total)
	}
	
	// Get timing percentiles
	percentiles := w.getPercentiles()
	
	return map[string]any{
		"total_analyses":     total,
		"success_count":      successes,
		"timeout_count":      timeouts,
		"error_count":        errorCount,
		"success_rate":       successRate,
		"timeout_rate":       timeoutRate,
		"error_rate":         errorRate,
		"timing_percentiles": percentiles,
		"circuit_breaker":    w.circuitBreaker.GetMetrics(),
		"worker_pool": map[string]any{
			"workers":        w.workers,
			"queue_size":     len(w.taskQueue),
			"queue_capacity": cap(w.taskQueue),
		},
	}
}

// CircuitBreaker implements an enhanced circuit breaker with failure type tracking
type CircuitBreaker struct {
	maxFailures      int
	resetTimeout     time.Duration
	halfOpenRequests int
	failureThresholds map[string]int // Per-type failure thresholds
	
	mu               sync.RWMutex
	state            string // "closed", "open", "half-open"
	failures         map[string]int // Failures by type
	totalFailures    int
	lastFailTime     time.Time
	halfOpenAttempts int
	
	// Metrics
	stateTransitions  atomic.Int64
	successCount      atomic.Int64
	failureCount      atomic.Int64
	rejectedCount     atomic.Int64
	lastStateChange   time.Time
	
	// Gradual recovery
	recoverySteps     int
	currentStep       int
}

// NewCircuitBreaker creates an enhanced circuit breaker
func NewCircuitBreaker(config *CircuitBreakerConfig) *CircuitBreaker {
	// Set defaults
	recoverySteps := config.RecoverySteps
	if recoverySteps == 0 {
		recoverySteps = 3
	}
	
	failureThresholds := config.FailureThresholds
	if failureThresholds == nil {
		failureThresholds = make(map[string]int)
	}
	
	return &CircuitBreaker{
		maxFailures:       config.MaxFailures,
		resetTimeout:      config.ResetTimeout,
		halfOpenRequests:  config.HalfOpenRequests,
		failureThresholds: failureThresholds,
		recoverySteps:     recoverySteps,
		state:             "closed",
		failures:          make(map[string]int),
		lastStateChange:   time.Now(),
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
			cb.transitionToHalfOpen()
			return true
		}
		cb.rejectedCount.Add(1)
		return false
		
	case "half-open":
		// Gradual recovery - allow requests per step
		currentStepRequests := cb.halfOpenRequests / cb.recoverySteps
		if cb.halfOpenAttempts < currentStepRequests {
			cb.halfOpenAttempts++
			return true
		}
		cb.rejectedCount.Add(1)
		return false
		
	default:
		return false
	}
}

// RecordSuccess records a successful execution
func (cb *CircuitBreaker) RecordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	
	cb.successCount.Add(1)
	
	if cb.state == "half-open" {
		// Check if we've completed enough requests for current step
		currentStepRequests := cb.halfOpenRequests / cb.recoverySteps
		if cb.halfOpenAttempts >= currentStepRequests {
			// Progress through recovery steps
			cb.currentStep++
			if cb.currentStep >= cb.recoverySteps {
				// Fully recovered, close the circuit
				cb.transitionToClosed()
			} else {
				// Reset attempts for next recovery step
				cb.halfOpenAttempts = 0
			}
		}
	}
}

// RecordFailure records a failed execution with failure type
func (cb *CircuitBreaker) RecordFailure(failureType string) {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	
	cb.failureCount.Add(1)
	cb.failures[failureType]++
	cb.totalFailures++
	cb.lastFailTime = time.Now()
	
	switch cb.state {
	case "closed":
		// Check if we should open based on type-specific thresholds
		shouldOpen := false
		
		// Check type-specific threshold
		if threshold, exists := cb.failureThresholds[failureType]; exists {
			if cb.failures[failureType] >= threshold {
				shouldOpen = true
			}
		}
		
		// Check total threshold
		if cb.totalFailures >= cb.maxFailures {
			shouldOpen = true
		}
		
		if shouldOpen {
			cb.transitionToOpen()
		}
		
	case "half-open":
		// Failure in half-open state, reopen the circuit
		cb.transitionToOpen()
	}
}

// GetState returns the current state
func (cb *CircuitBreaker) GetState() string {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.state
}

// transitionToOpen transitions to open state
func (cb *CircuitBreaker) transitionToOpen() {
	cb.state = "open"
	cb.stateTransitions.Add(1)
	cb.lastStateChange = time.Now()
}

// transitionToHalfOpen transitions to half-open state
func (cb *CircuitBreaker) transitionToHalfOpen() {
	cb.state = "half-open"
	cb.halfOpenAttempts = 0
	cb.currentStep = 0
	cb.stateTransitions.Add(1)
	cb.lastStateChange = time.Now()
}

// transitionToClosed transitions to closed state
func (cb *CircuitBreaker) transitionToClosed() {
	cb.state = "closed"
	cb.failures = make(map[string]int)
	cb.totalFailures = 0
	cb.currentStep = 0
	cb.stateTransitions.Add(1)
	cb.lastStateChange = time.Now()
}

// GetMetrics returns detailed circuit breaker metrics
func (cb *CircuitBreaker) GetMetrics() map[string]any {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	
	metrics := map[string]any{
		"state":              cb.state,
		"state_transitions":  cb.stateTransitions.Load(),
		"success_count":      cb.successCount.Load(),
		"failure_count":      cb.failureCount.Load(),
		"rejected_count":     cb.rejectedCount.Load(),
		"total_failures":     cb.totalFailures,
		"failures_by_type":   cb.failures,
		"last_state_change":  cb.lastStateChange,
		"time_in_state":      time.Since(cb.lastStateChange).String(),
	}
	
	if cb.state == "half-open" {
		metrics["recovery_progress"] = map[string]any{
			"current_step":      cb.currentStep,
			"total_steps":       cb.recoverySteps,
			"attempts_in_step":  cb.halfOpenAttempts,
			"progress_percent":  float64(cb.currentStep) / float64(cb.recoverySteps) * 100,
		}
	}
	
	return metrics
}

// Reset resets the circuit breaker
func (cb *CircuitBreaker) Reset() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	
	cb.state = "closed"
	cb.failures = make(map[string]int)
	cb.totalFailures = 0
	cb.halfOpenAttempts = 0
	cb.currentStep = 0
	cb.lastStateChange = time.Now()
	cb.stateTransitions.Add(1)
}