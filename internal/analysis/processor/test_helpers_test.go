// test_helpers_test.go - Shared test helpers for processor package
// These helpers reduce duplication across test files and ensure consistent test setup.
package processor

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/tphakala/birdnet-go/internal/analysis/jobqueue"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/detection"
	"github.com/tphakala/birdnet-go/internal/testutil"
)

// --- Audio Source Helpers ---

// testAudioSource returns a standard test audio source to avoid duplication.
func testAudioSource() datastore.AudioSource {
	return datastore.AudioSource{
		ID:          "test-source",
		SafeString:  "test-source",
		DisplayName: "test-source",
	}
}

// --- Detection Helpers ---

// testDetection creates a basic detection with sensible defaults for testing.
func testDetection() Detections {
	now := time.Now()
	return Detections{
		Result: detection.Result{
			Timestamp:  now,
			SourceNode: "test-node",
			AudioSource: detection.AudioSource{
				ID:          "test-source",
				SafeString:  "test-source",
				DisplayName: "test-source",
			},
			BeginTime: now,
			EndTime:   now.Add(15 * time.Second),
			Species: detection.Species{
				ScientificName: "Testus birdus",
				CommonName:     "Test Bird",
			},
			Confidence: 0.95,
			Model:      detection.DefaultModelInfo(),
		},
		Results: []detection.AdditionalResult{
			{Species: detection.Species{ScientificName: "Testus birdus", CommonName: "Test Bird"}, Confidence: 0.95},
		},
	}
}

// createSimpleDetection is an alias for testDetection for backward compatibility.
func createSimpleDetection() Detections {
	return testDetection()
}

// testDetectionWithSpecies creates a detection for a specific species.
func testDetectionWithSpecies(commonName, scientificName string, confidence float64) Detections {
	now := time.Now()
	return Detections{
		Result: detection.Result{
			Timestamp:  now,
			SourceNode: "test-node",
			AudioSource: detection.AudioSource{
				ID:          "test-source",
				SafeString:  "test-source",
				DisplayName: "test-source",
			},
			BeginTime: now,
			EndTime:   now.Add(15 * time.Second),
			Species: detection.Species{
				ScientificName: scientificName,
				CommonName:     commonName,
			},
			Confidence: confidence,
			Model:      detection.DefaultModelInfo(),
		},
		Results: []detection.AdditionalResult{
			{Species: detection.Species{ScientificName: scientificName, CommonName: commonName}, Confidence: confidence},
		},
	}
}

// --- Mock Job Queue ---

// MockJobQueue is a mock implementation of the job queue interface for testing.
type MockJobQueue struct {
	mu              sync.Mutex
	startCalled     bool
	startCount      int
	startCtx        context.Context
	stopCalled      bool
	stopCount       int
	stopTimeout     time.Duration
	enqueueErr      error
	enqueueJob      *jobqueue.Job
	enqueueCalls    []mockEnqueueCall
	maxJobs         int
	processingTime  time.Duration
	getStatsCount   int
	getMaxJobsCount int
}

// mockEnqueueCall tracks the arguments passed to Enqueue.
type mockEnqueueCall struct {
	ctx    context.Context
	action jobqueue.Action
	data   any
	config jobqueue.RetryConfig
}

func (m *MockJobQueue) Start() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.startCalled = true
	m.startCount++
}

func (m *MockJobQueue) StartWithContext(ctx context.Context) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.startCalled = true
	m.startCount++
	m.startCtx = ctx
}

func (m *MockJobQueue) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stopCalled = true
	m.stopCount++
	return nil
}

func (m *MockJobQueue) StopWithTimeout(timeout time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stopCalled = true
	m.stopCount++
	m.stopTimeout = timeout
	return nil
}

func (m *MockJobQueue) Enqueue(ctx context.Context, action jobqueue.Action, data any, config jobqueue.RetryConfig) (*jobqueue.Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.enqueueCalls = append(m.enqueueCalls, mockEnqueueCall{
		ctx:    ctx,
		action: action,
		data:   data,
		config: config,
	})

	if m.enqueueErr != nil {
		return nil, m.enqueueErr
	}

	if m.enqueueJob != nil {
		return m.enqueueJob, nil
	}

	return &jobqueue.Job{ID: fmt.Sprintf("mock-job-%d", len(m.enqueueCalls))}, nil
}

func (m *MockJobQueue) GetMaxJobs() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.getMaxJobsCount++
	if m.maxJobs > 0 {
		return m.maxJobs
	}
	return 100
}

func (m *MockJobQueue) GetStats() jobqueue.JobStatsSnapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.getStatsCount++
	return jobqueue.JobStatsSnapshot{}
}

func (m *MockJobQueue) SetProcessingInterval(interval time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.processingTime = interval
}

// EnqueueCallCount returns the number of times Enqueue was called.
func (m *MockJobQueue) EnqueueCallCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.enqueueCalls)
}

// GetEnqueueCall returns the Enqueue call at the given index.
func (m *MockJobQueue) GetEnqueueCall(index int) (mockEnqueueCall, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if index < 0 || index >= len(m.enqueueCalls) {
		return mockEnqueueCall{}, fmt.Errorf("index out of range: %d", index)
	}
	return m.enqueueCalls[index], nil
}

// Reset resets the mock job queue state.
func (m *MockJobQueue) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.startCalled = false
	m.startCount = 0
	m.startCtx = nil
	m.stopCalled = false
	m.stopCount = 0
	m.stopTimeout = 0
	m.enqueueCalls = nil
	m.getStatsCount = 0
	m.getMaxJobsCount = 0
}

// --- Mock Action ---

// MockAction is a mock implementation of the Action interface for testing.
type MockAction struct {
	mu           sync.Mutex
	ExecuteFunc  func(data any) error // Legacy callback without context
	ExecuteCount int
	ExecuteData  []any
}

func (m *MockAction) Execute(ctx context.Context, data any) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ExecuteCount++
	m.ExecuteData = append(m.ExecuteData, data)
	if m.ExecuteFunc != nil {
		return m.ExecuteFunc(data)
	}
	return nil
}

// GetDescription implements the Action interface.
func (m *MockAction) GetDescription() string {
	return "Mock Action for testing"
}

// Reset resets the mock action state.
func (m *MockAction) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ExecuteCount = 0
	m.ExecuteData = nil
}

// --- Mock BirdWeather Action ---

// MockBirdWeatherAction is a mock implementation of BirdWeatherAction for testing.
type MockBirdWeatherAction struct {
	MockAction
	RetryConfig jobqueue.RetryConfig
}

// GetDescription returns a description for the MockBirdWeatherAction.
func (m *MockBirdWeatherAction) GetDescription() string {
	return "Mock BirdWeather Action for testing"
}

// --- Mock MQTT Action ---

// MockMqttAction is a mock implementation of MqttAction for testing.
type MockMqttAction struct {
	MockAction
	RetryConfig jobqueue.RetryConfig
}

// GetDescription returns a description for the MockMqttAction.
func (m *MockMqttAction) GetDescription() string {
	return "Mock MQTT Action for testing"
}

// --- Simple Action (for race condition tests) ---

// SimpleAction tracks execution timing for race condition tests.
type SimpleAction struct {
	name         string
	executeDelay time.Duration
	executedAt   time.Time
	executed     bool
	executeMutex sync.Mutex
	onExecute    func() // Callback for additional behavior
}

func (a *SimpleAction) Execute(ctx context.Context, data any) error {
	return a.ExecuteContext(ctx, data)
}

// ExecuteContext implements the ContextAction interface for proper context propagation.
func (a *SimpleAction) ExecuteContext(ctx context.Context, data any) error {
	a.executeMutex.Lock()
	defer a.executeMutex.Unlock()

	if a.executeDelay > 0 {
		if err := a.interruptibleSleep(ctx, a.executeDelay); err != nil {
			return err
		}
	}

	a.executedAt = time.Now()
	a.executed = true

	if a.onExecute != nil {
		a.onExecute()
	}

	return nil
}

// interruptibleSleep performs a sleep that can be interrupted by context cancellation.
func (a *SimpleAction) interruptibleSleep(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()

	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (a *SimpleAction) GetDescription() string {
	return a.name
}

func (a *SimpleAction) GetExecutionTime() time.Time {
	a.executeMutex.Lock()
	defer a.executeMutex.Unlock()
	return a.executedAt
}

func (a *SimpleAction) WasExecuted() bool {
	a.executeMutex.Lock()
	defer a.executeMutex.Unlock()
	return a.executed
}

// --- Mock Settings ---

// MockSettings implements the necessary methods from conf.Settings for testing.
type MockSettings struct {
	Debug bool
}

func (s *MockSettings) IsDebug() bool {
	return s.Debug
}

// --- Processor Setup Helpers ---

// setupTestProcessor creates a processor with a real job queue and registers cleanup.
// The queue is automatically stopped when the test completes.
func setupTestProcessor(t *testing.T) *Processor {
	t.Helper()
	queue := jobqueue.NewJobQueue()
	queue.Start()
	t.Cleanup(func() {
		assert.NoError(t, queue.Stop(), "Failed to stop queue")
	})

	return &Processor{
		JobQueue: queue,
		Settings: &conf.Settings{Debug: true},
	}
}

// setupTestProcessorWithInterval creates a processor with a custom processing interval.
func setupTestProcessorWithInterval(t *testing.T, interval time.Duration) *Processor {
	t.Helper()
	queue := jobqueue.NewJobQueue()
	queue.SetProcessingInterval(interval)
	queue.Start()
	t.Cleanup(func() {
		assert.NoError(t, queue.Stop(), "Failed to stop queue")
	})

	return &Processor{
		JobQueue: queue,
		Settings: &conf.Settings{Debug: true},
	}
}

// --- Channel Wait Helpers ---

// waitForChannel waits for a signal on the channel or fails after timeout.
func waitForChannel(t *testing.T, ch <-chan struct{}, timeout time.Duration, msg string) {
	t.Helper()
	testutil.WaitForChannel(t, ch, timeout, msg)
}

// waitForChannelWithLog waits for a signal and logs on success.
func waitForChannelWithLog(t *testing.T, ch <-chan struct{}, timeout time.Duration, failMsg, successMsg string) {
	t.Helper()
	testutil.WaitForChannelWithLog(t, ch, timeout, failMsg, successMsg)
}
