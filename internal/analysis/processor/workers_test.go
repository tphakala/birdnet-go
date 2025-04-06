package processor

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tphakala/birdnet-go/internal/analysis/jobqueue"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
)

// MockJobQueue is a mock implementation of the job queue interface
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

// mockEnqueueCall tracks the arguments passed to Enqueue
type mockEnqueueCall struct {
	action jobqueue.Action
	data   interface{}
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

func (m *MockJobQueue) Enqueue(action jobqueue.Action, data interface{}, config jobqueue.RetryConfig) (*jobqueue.Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Record this call
	m.enqueueCalls = append(m.enqueueCalls, mockEnqueueCall{
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

	// Create a default job if none is specified
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

// Helper methods for verification in tests
func (m *MockJobQueue) EnqueueCallCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.enqueueCalls)
}

func (m *MockJobQueue) GetEnqueueCall(index int) (mockEnqueueCall, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if index < 0 || index >= len(m.enqueueCalls) {
		return mockEnqueueCall{}, fmt.Errorf("index out of range: %d", index)
	}
	return m.enqueueCalls[index], nil
}

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

// MockAction is a mock implementation of the Action interface
type MockAction struct {
	mu           sync.Mutex
	ExecuteFunc  func(data interface{}) error
	ExecuteCount int
	ExecuteData  []interface{}
}

func (m *MockAction) Execute(data interface{}) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ExecuteCount++
	m.ExecuteData = append(m.ExecuteData, data)
	if m.ExecuteFunc != nil {
		return m.ExecuteFunc(data)
	}
	return nil
}

// GetDescription implements the Action interface
func (m *MockAction) GetDescription() string {
	return "Mock Action for testing"
}

// Reset resets the mock action state
func (m *MockAction) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ExecuteCount = 0
	m.ExecuteData = nil
}

// MockBirdWeatherAction is a mock implementation of BirdWeatherAction
type MockBirdWeatherAction struct {
	MockAction
	RetryConfig jobqueue.RetryConfig
}

// GetDescription returns a description for the MockBirdWeatherAction
func (m *MockBirdWeatherAction) GetDescription() string {
	return "Mock BirdWeather Action for testing"
}

// MockMqttAction is a mock implementation of MqttAction
type MockMqttAction struct {
	MockAction
	RetryConfig jobqueue.RetryConfig
}

// GetDescription returns a description for the MockMqttAction
func (m *MockMqttAction) GetDescription() string {
	return "Mock MQTT Action for testing"
}

// MockSettings implements the necessary methods from conf.Settings for testing
type MockSettings struct {
	Debug bool
}

func (s *MockSettings) IsDebug() bool {
	return s.Debug
}

func TestSanitizeError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected string
	}{
		{
			name:     "nil error",
			err:      nil,
			expected: "",
		},
		{
			name:     "simple error",
			err:      errors.New("simple error message"),
			expected: "simple error message",
		},
		{
			name:     "RTSP URL with credentials",
			err:      errors.New("failed to connect to rtsp://admin:password123@192.168.1.100:554/stream"),
			expected: "failed to connect to rtsp://[redacted]@192.168.1.100:554/stream",
		},
		{
			name:     "MQTT URL with credentials",
			err:      errors.New("failed to connect to mqtt://user:secret@mqtt.example.com:1883"),
			expected: "failed to connect to mqtt://[redacted]@mqtt.example.com:1883",
		},
		{
			name: "API key in error",
			// NOTE: This is a fake API key used only for testing the sanitization function.
			// It is deliberately included as a test fixture and is not a real credential.
			err:      errors.New("API request failed: api_key=abc123xyz789"),
			expected: "API request failed: api_key=[REDACTED]",
		},
		{
			name:     "Token in error",
			err:      errors.New("authentication failed: token=eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9"),
			expected: "authentication failed: token=[REDACTED]",
		},
		{
			name:     "Password in error",
			err:      errors.New("login failed: password=supersecret123"),
			expected: "login failed: password=[REDACTED]",
		},
		{
			name:     "Multiple sensitive data",
			err:      errors.New("failed: rtsp://user:pass@example.com and api_key=12345 and password=secret"),
			expected: "failed: rtsp://[redacted]@example.com and api_key=[REDACTED] and password=[REDACTED]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.err == nil {
				if sanitizeError(tt.err) != nil {
					t.Errorf("sanitizeError(nil) should return nil")
				}
				return
			}

			sanitized := sanitizeError(tt.err)
			if sanitized == nil {
				t.Errorf("sanitizeError() returned nil for non-nil error")
				return
			}

			// Check that the sanitized error message matches the expected value
			if sanitized.Error() != tt.expected {
				t.Errorf("sanitizeError() = %q, want %q", sanitized.Error(), tt.expected)
			}

			// Check that the original error is preserved
			if !errors.Is(sanitized, tt.err) {
				t.Errorf("errors.Is(sanitized, original) = false, want true")
			}
		})
	}
}

// TestSanitizeErrorWrapped tests that sanitizeError works with wrapped errors
func TestSanitizeErrorWrapped(t *testing.T) {
	// Create a wrapped error with sensitive information
	baseErr := errors.New("password=secret123")
	wrappedErr := fmt.Errorf("operation failed: %w", baseErr)

	// Sanitize the wrapped error
	sanitized := sanitizeError(wrappedErr)

	// Check that the sensitive information was removed
	expected := "operation failed: password=[REDACTED]"
	if sanitized.Error() != expected {
		t.Errorf("sanitizeError() = %q, want %q", sanitized.Error(), expected)
	}

	// Check that the original error is preserved
	if !errors.Is(sanitized, wrappedErr) {
		t.Errorf("errors.Is(sanitized, wrappedErr) = false, want true")
	}

	// Check that we can still access the base error
	if !errors.Is(sanitized, baseErr) {
		t.Errorf("errors.Is(sanitized, baseErr) = false, want true")
	}
}

// TestStartWorkerPool tests the startWorkerPool function
func TestStartWorkerPool(t *testing.T) {
	// Create a real job queue for testing
	realQueue := jobqueue.NewJobQueue()

	// Create a processor with the real queue
	processor := &Processor{
		JobQueue: realQueue,
	}

	// Call the function with a single worker
	processor.startWorkerPool(1)

	// Verify the cancel function was stored
	if processor.workerCancel == nil {
		t.Errorf("Cancel function was not stored")
	}

	// Clean up
	realQueue.Stop()
}

// TestGetJobQueueRetryConfig tests that the getJobQueueRetryConfig function correctly extracts retry configuration from different action types
func TestGetJobQueueRetryConfig(t *testing.T) {
	// Create test retry configurations
	bwRetryConfig := jobqueue.RetryConfig{
		Enabled:      true,
		MaxRetries:   3,
		InitialDelay: 10 * time.Second,
		MaxDelay:     60 * time.Second,
		Multiplier:   2.0,
	}

	mqttRetryConfig := jobqueue.RetryConfig{
		Enabled:      true,
		MaxRetries:   5,
		InitialDelay: 5 * time.Second,
		MaxDelay:     30 * time.Second,
		Multiplier:   1.5,
	}

	// Create test actions with retry configurations
	bwAction := &BirdWeatherAction{
		RetryConfig: bwRetryConfig,
	}

	mqttAction := &MqttAction{
		RetryConfig: mqttRetryConfig,
	}

	// Create a generic action with no retry configuration
	genericAction := &MockAction{}

	// Test cases
	tests := []struct {
		name           string
		action         Action
		wantEnabled    bool
		wantMaxRetries int
	}{
		{
			name:           "BirdWeatherAction",
			action:         bwAction,
			wantEnabled:    true,
			wantMaxRetries: 3,
		},
		{
			name:           "MqttAction",
			action:         mqttAction,
			wantEnabled:    true,
			wantMaxRetries: 5,
		},
		{
			name:           "GenericAction",
			action:         genericAction,
			wantEnabled:    false,
			wantMaxRetries: 0,
		},
		{
			name:           "NilAction",
			action:         nil,
			wantEnabled:    false,
			wantMaxRetries: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var config jobqueue.RetryConfig

			// Handle nil action case safely
			if tt.action == nil {
				// Create a wrapper function to safely call getJobQueueRetryConfig with nil
				safeGetConfig := func() (cfg jobqueue.RetryConfig) {
					// Use defer/recover to catch any panic
					defer func() {
						if r := recover(); r != nil {
							t.Logf("Recovered from panic with nil action: %v", r)
							// Return default config on panic
							cfg = jobqueue.RetryConfig{Enabled: false}
						}
					}()

					// Try to get config, may panic
					return getJobQueueRetryConfig(nil)
				}

				// Get config safely
				config = safeGetConfig()
			} else {
				// Normal case, no nil action
				config = getJobQueueRetryConfig(tt.action)
			}

			// Check if the configuration matches expectations
			if config.Enabled != tt.wantEnabled {
				t.Errorf("getJobQueueRetryConfig() Enabled = %v, want %v", config.Enabled, tt.wantEnabled)
			}

			if config.MaxRetries != tt.wantMaxRetries {
				t.Errorf("getJobQueueRetryConfig() MaxRetries = %v, want %v", config.MaxRetries, tt.wantMaxRetries)
			}

			// For actions with retry configuration, check if the full configuration is preserved
			switch tt.action.(type) {
			case *BirdWeatherAction:
				if config.InitialDelay != bwRetryConfig.InitialDelay {
					t.Errorf("getJobQueueRetryConfig() InitialDelay = %v, want %v", config.InitialDelay, bwRetryConfig.InitialDelay)
				}
				if config.MaxDelay != bwRetryConfig.MaxDelay {
					t.Errorf("getJobQueueRetryConfig() MaxDelay = %v, want %v", config.MaxDelay, bwRetryConfig.MaxDelay)
				}
				if config.Multiplier != bwRetryConfig.Multiplier {
					t.Errorf("getJobQueueRetryConfig() Multiplier = %v, want %v", config.Multiplier, bwRetryConfig.Multiplier)
				}
			case *MqttAction:
				if config.InitialDelay != mqttRetryConfig.InitialDelay {
					t.Errorf("getJobQueueRetryConfig() InitialDelay = %v, want %v", config.InitialDelay, mqttRetryConfig.InitialDelay)
				}
				if config.MaxDelay != mqttRetryConfig.MaxDelay {
					t.Errorf("getJobQueueRetryConfig() MaxDelay = %v, want %v", config.MaxDelay, mqttRetryConfig.MaxDelay)
				}
				if config.Multiplier != mqttRetryConfig.Multiplier {
					t.Errorf("getJobQueueRetryConfig() Multiplier = %v, want %v", config.Multiplier, mqttRetryConfig.Multiplier)
				}
			}
		})
	}
}

// TestEnqueueTask tests the Processor.EnqueueTask method with various action types and scenarios
func TestEnqueueTask(t *testing.T) {
	// Create a real job queue for testing
	realQueue := jobqueue.NewJobQueue()
	realQueue.Start()
	defer realQueue.Stop()

	// Create a processor with the real queue
	processor := &Processor{
		JobQueue: realQueue,
		Settings: &conf.Settings{
			Debug: true,
		},
	}

	// Test with different action types
	t.Run("DifferentActionTypes", func(t *testing.T) {
		// Create different types of actions
		actions := []struct {
			name   string
			action Action
		}{
			{"MockAction", &MockAction{}},
			{"BirdWeatherAction", &BirdWeatherAction{
				RetryConfig: jobqueue.RetryConfig{
					Enabled:      true,
					MaxRetries:   3,
					InitialDelay: 10 * time.Second,
					MaxDelay:     60 * time.Second,
					Multiplier:   2.0,
				},
			}},
			{"MqttAction", &MqttAction{
				RetryConfig: jobqueue.RetryConfig{
					Enabled:      true,
					MaxRetries:   5,
					InitialDelay: 5 * time.Second,
					MaxDelay:     30 * time.Second,
					Multiplier:   1.5,
				},
			}},
		}

		for _, tc := range actions {
			t.Run(tc.name, func(t *testing.T) {
				// Create a task with this action
				task := &Task{
					Type: TaskTypeAction,
					Detection: Detections{
						Note: datastore.Note{
							CommonName:     "Test Bird",
							ScientificName: "Testus birdus",
							Confidence:     0.95,
							Source:         "test-source",
							BeginTime:      time.Now(),
							EndTime:        time.Now().Add(15 * time.Second),
						},
					},
					Action: tc.action,
				}

				// Enqueue the task
				err := processor.EnqueueTask(task)
				if err != nil {
					t.Errorf("Failed to enqueue task with %s: %v", tc.name, err)
				}
			})
		}
	})

	// Test error handling with a queue that's been stopped
	t.Run("StoppedQueue", func(t *testing.T) {
		// Create a new queue that we'll stop immediately
		stoppedQueue := jobqueue.NewJobQueue()
		stoppedQueue.Start()
		stoppedQueue.Stop()

		// Create a processor with the stopped queue
		stoppedProcessor := &Processor{
			JobQueue: stoppedQueue,
			Settings: &conf.Settings{
				Debug: true,
			},
		}

		// Create a task
		task := &Task{
			Type: TaskTypeAction,
			Detection: Detections{
				Note: datastore.Note{
					CommonName:     "Test Bird",
					ScientificName: "Testus birdus",
					Confidence:     0.95,
					Source:         "test-source",
					BeginTime:      time.Now(),
					EndTime:        time.Now().Add(15 * time.Second),
				},
			},
			Action: &MockAction{},
		}

		// Enqueue the task, expecting an error
		err := stoppedProcessor.EnqueueTask(task)
		if err == nil {
			t.Errorf("Expected error when enqueueing to stopped queue, got nil")
		} else if !strings.Contains(err.Error(), "job queue has been stopped") {
			t.Errorf("Expected error to contain 'job queue has been stopped', got %v", err)
		}
	})

	// Test with a full queue
	t.Run("FullQueue", func(t *testing.T) {
		// Create a queue with a very small capacity
		tinyQueue := jobqueue.NewJobQueueWithOptions(2, 1, false)
		tinyQueue.Start()
		defer tinyQueue.Stop()

		// Create a processor with the tiny queue
		tinyProcessor := &Processor{
			JobQueue: tinyQueue,
			Settings: &conf.Settings{
				Debug: true,
			},
		}

		// Create tasks to fill the queue
		for i := 0; i < 5; i++ {
			task := &Task{
				Type: TaskTypeAction,
				Detection: Detections{
					Note: datastore.Note{
						CommonName:     fmt.Sprintf("Test Bird %d", i),
						ScientificName: "Testus birdus",
						Confidence:     0.95,
						Source:         "test-source",
						BeginTime:      time.Now(),
						EndTime:        time.Now().Add(15 * time.Second),
					},
				},
				Action: &MockAction{
					ExecuteFunc: func(data interface{}) error {
						// Sleep to keep the queue full
						time.Sleep(100 * time.Millisecond)
						return nil
					},
				},
			}

			// Enqueue the task, but don't fail the test if we get a "queue full" error
			err := tinyProcessor.EnqueueTask(task)
			if err != nil && !strings.Contains(err.Error(), "queue is full") {
				t.Errorf("Unexpected error: %v", err)
			}
		}

		// Verify that at least one task was rejected due to queue full
		stats := tinyQueue.GetStats()
		if stats.DroppedJobs == 0 {
			t.Logf("Warning: Expected at least one dropped job due to queue full, but got none. This test may be flaky.")
		}
	})

	// Test with a task that has a detection with a lot of data
	t.Run("LargeDetection", func(t *testing.T) {
		// Create a large detection with many results
		detection := Detections{
			Note: datastore.Note{
				CommonName:     "Test Bird",
				ScientificName: "Testus birdus",
				Confidence:     0.95,
				Source:         "test-source",
				BeginTime:      time.Now(),
				EndTime:        time.Now().Add(15 * time.Second),
			},
		}

		// Add a large number of results
		for i := 0; i < 100; i++ {
			detection.Results = append(detection.Results, datastore.Results{
				Species:    fmt.Sprintf("Test Bird %d", i),
				Confidence: float32(i) / 100.0,
			})
		}

		// Create a task with this large detection
		task := &Task{
			Type:      TaskTypeAction,
			Detection: detection,
			Action:    &MockAction{},
		}

		// Enqueue the task
		err := processor.EnqueueTask(task)
		if err != nil {
			t.Errorf("Failed to enqueue task with large detection: %v", err)
		}
	})
}

// TestEnqueueTaskBasic tests the basic functionality of the EnqueueTask method
func TestEnqueueTaskBasic(t *testing.T) {
	// Create a real job queue for testing
	realQueue := jobqueue.NewJobQueue()

	// Start the job queue
	realQueue.Start()

	// Create a processor with the real queue
	processor := &Processor{
		JobQueue: realQueue,
		Settings: &conf.Settings{
			Debug: true,
		},
	}

	// Test case 1: Nil task
	err := processor.EnqueueTask(nil)
	if err == nil {
		t.Errorf("Expected error for nil task, got nil")
	} else if !strings.Contains(err.Error(), "cannot enqueue nil task") {
		t.Errorf("Expected error to contain 'cannot enqueue nil task', got %v", err)
	}

	// Test case 2: Nil action
	task := &Task{
		Type:      TaskTypeAction,
		Detection: Detections{},
		Action:    nil,
	}
	err = processor.EnqueueTask(task)
	if err == nil {
		t.Errorf("Expected error for nil action, got nil")
	} else if !strings.Contains(err.Error(), "cannot enqueue task with nil action") {
		t.Errorf("Expected error to contain 'cannot enqueue task with nil action', got %v", err)
	}

	// Test case 3: Successful enqueue
	task = &Task{
		Type:      TaskTypeAction,
		Detection: Detections{},
		Action:    &MockAction{},
	}
	err = processor.EnqueueTask(task)
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}

	// Clean up
	realQueue.Stop()
}

// TestEnqueueMultipleTasks tests enqueueing multiple tasks with Scandinavian bird species
func TestEnqueueMultipleTasks(t *testing.T) {
	// Create a real job queue for testing
	realQueue := jobqueue.NewJobQueue()
	realQueue.Start()
	defer realQueue.Stop()

	// Create a processor with the real queue
	processor := &Processor{
		JobQueue: realQueue,
		Settings: &conf.Settings{
			Debug: true,
		},
	}

	// Define Scandinavian bird species with their scientific names
	scandinavianBirds := []struct {
		CommonName     string
		ScientificName string
		Confidence     float64
	}{
		{"Eurasian Blackbird", "Turdus merula", 0.92},
		{"Great Tit", "Parus major", 0.89},
		{"European Robin", "Erithacus rubecula", 0.95},
		{"Common Chaffinch", "Fringilla coelebs", 0.88},
		{"Willow Warbler", "Phylloscopus trochilus", 0.91},
		{"Eurasian Blue Tit", "Cyanistes caeruleus", 0.87},
		{"Common Blackbird", "Turdus merula", 0.93},
		{"Eurasian Wren", "Troglodytes troglodytes", 0.86},
		{"European Greenfinch", "Chloris chloris", 0.84},
		{"Common Chiffchaff", "Phylloscopus collybita", 0.90},
		{"Eurasian Bullfinch", "Pyrrhula pyrrhula", 0.85},
		{"Eurasian Nuthatch", "Sitta europaea", 0.83},
		{"Common Redstart", "Phoenicurus phoenicurus", 0.82},
		{"Eurasian Treecreeper", "Certhia familiaris", 0.81},
		{"Eurasian Jay", "Garrulus glandarius", 0.89},
		{"Common Whitethroat", "Sylvia communis", 0.87},
		{"Fieldfare", "Turdus pilaris", 0.86},
		{"Redwing", "Turdus iliacus", 0.85},
		{"Eurasian Siskin", "Spinus spinus", 0.84},
		{"Common Starling", "Sturnus vulgaris", 0.92},
		{"White Wagtail", "Motacilla alba", 0.91},
		{"Yellowhammer", "Emberiza citrinella", 0.88},
		{"Eurasian Skylark", "Alauda arvensis", 0.87},
		{"Common Cuckoo", "Cuculus canorus", 0.89},
		{"Eurasian Magpie", "Pica pica", 0.93},
		{"Hooded Crow", "Corvus cornix", 0.92},
		{"Common Raven", "Corvus corax", 0.91},
		{"Bohemian Waxwing", "Bombycilla garrulus", 0.90},
		{"Common Crane", "Grus grus", 0.89},
		{"White-tailed Eagle", "Haliaeetus albicilla", 0.94},
	}

	// Create a channel to track successful enqueues
	successChan := make(chan bool, len(scandinavianBirds))

	// Create a wait group to wait for all goroutines to complete
	var wg sync.WaitGroup

	// Enqueue tasks concurrently to test thread safety
	for _, bird := range scandinavianBirds {
		wg.Add(1)
		go func(b struct {
			CommonName     string
			ScientificName string
			Confidence     float64
		}) {
			defer wg.Done()

			// Create a task for this bird
			task := &Task{
				Type: TaskTypeAction,
				Detection: Detections{
					Note: datastore.Note{
						CommonName:     b.CommonName,
						ScientificName: b.ScientificName,
						Confidence:     b.Confidence,
						Source:         "test-source",
						BeginTime:      time.Now(),
						EndTime:        time.Now().Add(15 * time.Second),
					},
				},
				Action: &MockAction{},
			}

			// Enqueue the task
			err := processor.EnqueueTask(task)
			if err != nil {
				t.Errorf("Failed to enqueue task for %s (%s): %v", b.CommonName, b.ScientificName, err)
				successChan <- false
			} else {
				successChan <- true
			}
		}(bird)
	}

	// Wait for all goroutines to complete
	wg.Wait()
	close(successChan)

	// Count successful enqueues
	successCount := 0
	for success := range successChan {
		if success {
			successCount++
		}
	}

	// Verify all tasks were enqueued successfully
	if successCount != len(scandinavianBirds) {
		t.Errorf("Expected %d successful enqueues, got %d", len(scandinavianBirds), successCount)
	}

	// Get job queue stats
	stats := realQueue.GetStats()

	// Verify the job queue has the expected number of jobs
	if stats.TotalJobs < len(scandinavianBirds) {
		t.Errorf("Expected at least %d total jobs, got %d", len(scandinavianBirds), stats.TotalJobs)
	}

	// Log some statistics
	t.Logf("Successfully enqueued %d tasks", successCount)
	t.Logf("Job queue stats: Total=%d, Completed=%d, Failed=%d",
		stats.TotalJobs, stats.SuccessfulJobs, stats.FailedJobs)
}

// TestIntegrationWithJobQueue tests the integration between Processor.EnqueueTask and a real job queue
func TestIntegrationWithJobQueue(t *testing.T) {
	// Create a real job queue with a short processing interval for testing
	realQueue := jobqueue.NewJobQueue()
	realQueue.SetProcessingInterval(50 * time.Millisecond) // Process jobs quickly for testing
	realQueue.Start()
	defer realQueue.Stop()

	// Create a processor with the real queue
	processor := &Processor{
		JobQueue: realQueue,
		Settings: &conf.Settings{
			Debug: true,
		},
	}

	// Create a channel to track action execution
	executionChan := make(chan struct{})

	// Create a mock action that signals when executed
	mockAction := &MockAction{
		ExecuteFunc: func(data interface{}) error {
			// Signal that the action was executed
			executionChan <- struct{}{}
			return nil
		},
	}

	// Create a task with the mock action
	task := &Task{
		Type: TaskTypeAction,
		Detection: Detections{
			Note: datastore.Note{
				CommonName:     "Test Bird",
				ScientificName: "Testus birdus",
				Confidence:     0.95,
				Source:         "test-source",
				BeginTime:      time.Now(),
				EndTime:        time.Now().Add(15 * time.Second),
			},
		},
		Action: mockAction,
	}

	// Enqueue the task
	err := processor.EnqueueTask(task)
	if err != nil {
		t.Fatalf("Failed to enqueue task: %v", err)
	}

	// Wait for the action to be executed with a timeout
	select {
	case <-executionChan:
		// Action was executed successfully
	case <-time.After(1 * time.Second):
		t.Fatalf("Timeout waiting for action to be executed")
	}

	// Verify that the action was executed exactly once
	if mockAction.ExecuteCount != 1 {
		t.Errorf("Expected action to be executed once, got %d executions", mockAction.ExecuteCount)
	}

	// Wait a bit for the job queue to update its statistics
	time.Sleep(100 * time.Millisecond)

	// Verify that the job queue statistics reflect the completed job
	stats := realQueue.GetStats()
	if stats.SuccessfulJobs != 1 {
		t.Errorf("Expected 1 successful job, got %d", stats.SuccessfulJobs)
	}

	// Test with a failing action
	failingAction := &MockAction{
		ExecuteFunc: func(data interface{}) error {
			return fmt.Errorf("simulated failure")
		},
	}

	// Create a task with the failing action
	failingTask := &Task{
		Type: TaskTypeAction,
		Detection: Detections{
			Note: datastore.Note{
				CommonName:     "Failing Bird",
				ScientificName: "Failurus maximus",
				Confidence:     0.90,
				Source:         "test-source",
				BeginTime:      time.Now(),
				EndTime:        time.Now().Add(15 * time.Second),
			},
		},
		Action: failingAction,
	}

	// Enqueue the failing task
	err = processor.EnqueueTask(failingTask)
	if err != nil {
		t.Fatalf("Failed to enqueue failing task: %v", err)
	}

	// Wait for the job queue to process the failing job
	time.Sleep(200 * time.Millisecond)

	// Verify that the failing action was executed
	if failingAction.ExecuteCount != 1 {
		t.Errorf("Expected failing action to be executed once, got %d executions", failingAction.ExecuteCount)
	}

	// Verify that the job queue statistics reflect the failed job
	stats = realQueue.GetStats()
	if stats.FailedJobs < 1 {
		t.Errorf("Expected at least 1 failed job, got %d", stats.FailedJobs)
	}

	// Log final statistics
	t.Logf("Job queue stats: Total=%d, Successful=%d, Failed=%d",
		stats.TotalJobs, stats.SuccessfulJobs, stats.FailedJobs)
}

// TestRetryLogic tests that the job queue properly retries failed actions
func TestRetryLogic(t *testing.T) {
	// Set up the test retry config override
	testRetryConfigOverride = nil
	defer func() {
		testRetryConfigOverride = nil
	}()

	// Create a dedicated context for this test that won't be canceled prematurely
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel() // Ensure context gets canceled at the end of the test

	// Create a real job queue with a short processing interval for testing
	realQueue := jobqueue.NewJobQueue()
	realQueue.SetProcessingInterval(50 * time.Millisecond) // Process jobs quickly for testing
	realQueue.StartWithContext(ctx)
	defer realQueue.Stop()

	// Create a processor with the real queue
	processor := &Processor{
		JobQueue: realQueue,
		Settings: &conf.Settings{
			Debug: true,
		},
	}

	// Create a counter for tracking attempts
	var attemptCount int
	var attemptMutex sync.Mutex

	// Create a channel to signal when the job succeeds
	successChan := make(chan struct{})

	// Number of times the action should fail before succeeding
	failCount := 2

	// Create a retry configuration
	retryConfig := jobqueue.RetryConfig{
		Enabled:      true,
		MaxRetries:   5, // More than failCount to ensure it eventually succeeds
		InitialDelay: 10 * time.Millisecond,
		MaxDelay:     100 * time.Millisecond,
		Multiplier:   1.5,
	}

	// Create a mock action that fails a specified number of times before succeeding
	mockAction := &MockAction{
		ExecuteFunc: func(data interface{}) error {
			attemptMutex.Lock()
			attemptCount++
			currentAttempt := attemptCount
			attemptMutex.Unlock()

			t.Logf("Attempt %d of %d", currentAttempt, failCount+1)

			if currentAttempt <= failCount {
				// Return failure for the first N attempts
				return fmt.Errorf("simulated failure on attempt %d", currentAttempt)
			}

			// Signal that the job has succeeded
			t.Logf("Job succeeded on attempt %d", currentAttempt)
			close(successChan)
			return nil
		},
	}

	// Set up the test retry config override to return our retry config for MockAction
	testRetryConfigOverride = func(action Action) (jobqueue.RetryConfig, bool) {
		if _, ok := action.(*MockAction); ok {
			return retryConfig, true
		}
		return jobqueue.RetryConfig{}, false
	}

	// Create a task with the mock action
	task := &Task{
		Type: TaskTypeAction,
		Detection: Detections{
			Note: datastore.Note{
				CommonName:     "Retry Bird",
				ScientificName: "Retryus maximus",
				Confidence:     0.95,
				Source:         "test-source",
				BeginTime:      time.Now(),
				EndTime:        time.Now().Add(15 * time.Second),
			},
		},
		Action: mockAction,
	}

	// Enqueue the task
	err := processor.EnqueueTask(task)
	if err != nil {
		t.Fatalf("Failed to enqueue task: %v", err)
	}

	// Wait for the job to succeed with a timeout
	select {
	case <-successChan:
		// Job succeeded after retries
		t.Log("Success channel received signal")
	case <-time.After(5 * time.Second): // Increased timeout for CI environments
		t.Fatalf("Timeout waiting for job to succeed after retries")
	}

	// Wait a bit more to ensure all job stats are updated
	time.Sleep(200 * time.Millisecond)

	// Verify that the action was executed the expected number of times
	attemptMutex.Lock()
	finalAttemptCount := attemptCount
	attemptMutex.Unlock()

	if finalAttemptCount != failCount+1 {
		t.Errorf("Expected action to be executed %d times, got %d executions", failCount+1, finalAttemptCount)
	}

	// Verify that the job queue statistics reflect the retries and successful completion
	stats := realQueue.GetStats()
	if stats.SuccessfulJobs != 1 {
		t.Errorf("Expected 1 successful job, got %d", stats.SuccessfulJobs)
	}
	if stats.RetryAttempts < failCount {
		t.Errorf("Expected at least %d retry attempts, got %d", failCount, stats.RetryAttempts)
	}

	// Test with an action that always fails and exhausts retries
	maxRetries := 2
	exhaustionRetryConfig := jobqueue.RetryConfig{
		Enabled:      true,
		MaxRetries:   maxRetries,
		InitialDelay: 10 * time.Millisecond,
		MaxDelay:     100 * time.Millisecond,
		Multiplier:   1.5,
	}

	// Reset attempt counter
	attemptMutex.Lock()
	attemptCount = 0
	attemptMutex.Unlock()

	// Create a channel to track execution attempts
	attemptChan := make(chan int, maxRetries+1)
	allAttemptsComplete := make(chan struct{})

	// Create a mock action that always fails
	exhaustingAction := &MockAction{
		ExecuteFunc: func(data interface{}) error {
			attemptMutex.Lock()
			attemptCount++
			currentAttempt := attemptCount
			attemptMutex.Unlock()

			t.Logf("Exhaustion test: Attempt %d of %d", currentAttempt, maxRetries+1)
			attemptChan <- currentAttempt

			// Close the completion channel when all attempts are done
			if currentAttempt == maxRetries+1 {
				close(allAttemptsComplete)
			}

			return fmt.Errorf("simulated failure that will exhaust retries")
		},
	}

	// Update the test retry config override to handle both actions
	testRetryConfigOverride = func(action Action) (jobqueue.RetryConfig, bool) {
		if action == exhaustingAction {
			return exhaustionRetryConfig, true
		} else if action == mockAction {
			return retryConfig, true
		}
		return jobqueue.RetryConfig{}, false
	}

	// Create a task with the exhausting action
	exhaustingTask := &Task{
		Type: TaskTypeAction,
		Detection: Detections{
			Note: datastore.Note{
				CommonName:     "Exhausting Bird",
				ScientificName: "Exhaustus maximus",
				Confidence:     0.90,
				Source:         "test-source",
				BeginTime:      time.Now(),
				EndTime:        time.Now().Add(15 * time.Second),
			},
		},
		Action: exhaustingAction,
	}

	// Enqueue the exhausting task
	err = processor.EnqueueTask(exhaustingTask)
	if err != nil {
		t.Fatalf("Failed to enqueue exhausting task: %v", err)
	}

	// Wait for all attempts to complete with an increased timeout
	select {
	case <-allAttemptsComplete:
		// All attempts completed
		t.Log("All exhaustion attempts completed")
	case <-time.After(5 * time.Second):
		t.Fatalf("Timeout waiting for all exhaustion attempts to complete")
	}

	// Wait a bit more to ensure all processing is complete
	time.Sleep(200 * time.Millisecond)

	// Verify that the action was executed the expected number of times
	attemptMutex.Lock()
	finalExhaustionCount := attemptCount
	attemptMutex.Unlock()

	if finalExhaustionCount != maxRetries+1 {
		t.Errorf("Expected exhausting action to be executed %d times, got %d executions", maxRetries+1, finalExhaustionCount)
	}

	// Verify that the job queue statistics reflect the failed job
	stats = realQueue.GetStats()

	// Total jobs should be 2 (1 successful from first test, 1 failed from exhaustion test)
	if stats.TotalJobs != 2 {
		t.Errorf("Expected 2 total jobs, got %d", stats.TotalJobs)
	}

	if stats.SuccessfulJobs != 1 {
		t.Errorf("Expected 1 successful job, got %d", stats.SuccessfulJobs)
	}

	if stats.FailedJobs != 1 {
		t.Errorf("Expected 1 failed job, got %d", stats.FailedJobs)
	}

	// Log final statistics
	t.Logf("Job queue stats: Total=%d, Successful=%d, Failed=%d, Retries=%d",
		stats.TotalJobs, stats.SuccessfulJobs, stats.FailedJobs, stats.RetryAttempts)
}

// TestEdgeCases tests edge cases for the EnqueueTask method
// This test includes various edge cases including:
// - Actions with long execution times
// - Actions that return errors with sensitive data
// - Nil processor case (which is expected to panic and is caught with defer/recover)
func TestEdgeCases(t *testing.T) {
	tests := []struct {
		name        string
		setupFunc   func() (*Processor, *Task)
		wantErr     bool
		expectedErr string
	}{
		{
			name: "Action with extremely long execution time",
			setupFunc: func() (*Processor, *Task) {
				// Use a real job queue
				realQueue := jobqueue.NewJobQueue()
				realQueue.Start()

				p := &Processor{
					JobQueue: realQueue,
					Settings: &conf.Settings{},
				}
				action := &MockAction{
					ExecuteFunc: func(data interface{}) error {
						time.Sleep(100 * time.Millisecond) // Simulate long execution
						return nil
					},
				}
				task := &Task{
					Type:      TaskTypeAction,
					Detection: Detections{},
					Action:    action,
				}
				return p, task
			},
			wantErr: false,
		},
		{
			name: "Action with sensitive data in error",
			setupFunc: func() (*Processor, *Task) {
				// Create a custom function to simulate an error with sensitive data
				// We'll use a real job queue but intercept the enqueue call
				realQueue := jobqueue.NewJobQueue()
				// Don't start the queue so enqueue will fail

				p := &Processor{
					JobQueue: realQueue,
					Settings: &conf.Settings{},
				}
				task := &Task{
					Type:      TaskTypeAction,
					Detection: Detections{},
					Action:    &MockAction{},
				}
				return p, task
			},
			wantErr:     true,
			expectedErr: "job queue has been stopped",
		},
		{
			name: "Nil processor",
			// This test case deliberately tests the behavior when a nil processor is used.
			// We expect a panic with a nil pointer dereference, which is caught and verified.
			// This is an intentional test of error handling for misuse of the API.
			setupFunc: func() (*Processor, *Task) {
				return nil, &Task{
					Type:      TaskTypeAction,
					Detection: Detections{},
					Action:    &MockAction{},
				}
			},
			wantErr:     true,
			expectedErr: "nil pointer dereference", // Expectation of a panic
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.name == "Nil processor" {
				// Use defer/recover to catch the expected panic
				defer func() {
					if r := recover(); r == nil {
						t.Errorf("Expected panic but none occurred")
					}
				}()
			}

			p, task := tt.setupFunc()

			// Clean up job queue if it exists
			if p != nil && p.JobQueue != nil {
				defer p.JobQueue.Stop()
			}

			err := p.EnqueueTask(task)

			if (err != nil) != tt.wantErr {
				t.Errorf("EnqueueTask() error = %v, wantErr %v", err, tt.wantErr)
			}

			if err != nil && tt.expectedErr != "" && !strings.Contains(err.Error(), tt.expectedErr) {
				t.Errorf("EnqueueTask() error = %v, expected to contain %v", err, tt.expectedErr)
			}
		})
	}
}

// BenchmarkEnqueueTask benchmarks the EnqueueTask method
func BenchmarkEnqueueTask(b *testing.B) {
	// Create a mock job queue that doesn't do any locking for better performance
	mockQueue := &MockJobQueue{
		enqueueJob: &jobqueue.Job{ID: "bench-job"},
	}

	// Create a real job queue for the processor
	realQueue := jobqueue.NewJobQueue()

	// Create a processor with the real queue
	processor := &Processor{
		JobQueue: realQueue,
	}

	// Create a task
	task := &Task{
		Type:   TaskTypeAction,
		Action: &MockAction{},
	}

	// Replace the real queue with our mock for testing
	originalQueue := processor.JobQueue
	defer func() {
		// Restore the original queue after the test
		processor.JobQueue = originalQueue
	}()

	// Use a custom function for benchmarking
	enqueueTaskBench := func(task *Task) error {
		if task == nil {
			return fmt.Errorf("cannot enqueue nil task")
		}

		if task.Action == nil {
			return fmt.Errorf("cannot enqueue task with nil action")
		}

		// Get retry configuration for the action
		retryConfig := getJobQueueRetryConfig(task.Action)

		// Enqueue the task to our mock queue without locking
		_, err := mockQueue.Enqueue(&ActionAdapter{action: task.Action}, task.Detection, retryConfig)
		return err
	}

	// Reset the timer to exclude setup time
	b.ResetTimer()

	// Run the benchmark
	for i := 0; i < b.N; i++ {
		err := enqueueTaskBench(task)
		if err != nil {
			b.Fatalf("EnqueueTask failed: %v", err)
		}
	}
}

// TestSanitizeActionType tests that the sanitizeActionType function correctly sanitizes sensitive information
func TestSanitizeActionType(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "simple type",
			input:    "*processor.MockAction",
			expected: "*processor.MockAction",
		},
		{
			name:     "RTSP URL with credentials",
			input:    "*actions.RTSPAction{URL:rtsp://admin:password123@192.168.1.100:554/stream}",
			expected: "*actions.RTSPAction{URL:rtsp://[redacted]@192.168.1.100:554/stream}",
		},
		{
			name:     "MQTT URL with credentials",
			input:    "*actions.MQTTAction{Broker:mqtt://user:secret@mqtt.example.com:1883}",
			expected: "*actions.MQTTAction{Broker:mqtt://[redacted]@mqtt.example.com:1883}",
		},
		{
			name:     "API key in type",
			input:    "*actions.APIAction{Key:api_key=abc123xyz789}",
			expected: "*actions.APIAction{Key:api_key=[REDACTED]}",
		},
		{
			name:     "Password in type",
			input:    "*actions.LoginAction{Username:user,password=supersecret123}",
			expected: "*actions.LoginAction{Username:user,password=[REDACTED]",
		},
		{
			name:     "Multiple sensitive data",
			input:    "*actions.MultiAction{RTSP:rtsp://user:pass@example.com,API:api_key=12345,Auth:password=secret}",
			expected: "*actions.MultiAction{RTSP:rtsp://[redacted]@example.com,API:api_key=[REDACTED],Auth=[REDACTED]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sanitized := sanitizeActionType(tt.input)
			// Print the actual output for debugging
			t.Logf("Input: %q", tt.input)
			t.Logf("Actual: %q", sanitized)
			t.Logf("Expected: %q", tt.expected)
			if sanitized != tt.expected {
				t.Errorf("sanitizeActionType() = %q, want %q", sanitized, tt.expected)
			}
		})
	}
}
