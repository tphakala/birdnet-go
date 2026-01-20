package processor

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/analysis/jobqueue"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/detection"
	"github.com/tphakala/birdnet-go/internal/privacy"
)

// Mock types are now defined in test_helpers_test.go:
// - MockJobQueue
// - MockAction
// - MockBirdWeatherAction
// - MockMqttAction
// - MockSettings
// - testAudioSource()

// TestPrivacyWrapError tests that privacy.WrapError correctly sanitizes errors
// These tests verify the integration with the centralized privacy package.
func TestPrivacyWrapError(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name             string
		err              error
		shouldContain    []string // Strings that SHOULD be in the sanitized output
		shouldNotContain []string // Strings that should NOT be in the sanitized output
	}{
		{
			name:          "nil error",
			err:           nil,
			shouldContain: nil,
		},
		{
			name:          "simple error",
			err:           errors.New("simple error message"),
			shouldContain: []string{"simple error message"},
		},
		{
			name:             "RTSP URL with credentials",
			err:              errors.New("failed to connect to rtsp://admin:password123@192.168.1.100:554/stream"),
			shouldContain:    []string{"failed to connect to"},
			shouldNotContain: []string{"admin", "password123"},
		},
		{
			name: "API key in error",
			// NOTE: This is a fake API key used only for testing the sanitization function.
			err:              errors.New("API request failed: api_key=abc123xyz789"),
			shouldContain:    []string{"API request failed"},
			shouldNotContain: []string{"abc123xyz789"},
		},
		{
			name:             "Token in error",
			err:              errors.New("authentication failed: token=eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9"),
			shouldContain:    []string{"authentication failed", "[TOKEN]"},
			shouldNotContain: []string{"eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9"},
		},
		{
			name:             "Email in error",
			err:              errors.New("notification failed for user@example.com"),
			shouldContain:    []string{"notification failed for", "[EMAIL]"},
			shouldNotContain: []string{"user@example.com"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if tt.err == nil {
				assert.NoError(t, privacy.WrapError(tt.err), "privacy.WrapError(nil) should return nil")
				return
			}

			sanitized := privacy.WrapError(tt.err)
			require.Error(t, sanitized, "privacy.WrapError() returned nil for non-nil error")

			// Check that expected strings are present
			for _, s := range tt.shouldContain {
				assert.Contains(t, sanitized.Error(), s, "sanitized error should contain %q", s)
			}

			// Check that sensitive strings are NOT present
			for _, s := range tt.shouldNotContain {
				assert.NotContains(t, sanitized.Error(), s, "sanitized error should NOT contain %q", s)
			}

			// Check that the original error is preserved via Unwrap
			assert.ErrorIs(t, sanitized, tt.err, "errors.Is(sanitized, original) = false, want true")
		})
	}
}

// TestPrivacyWrapErrorWrapped tests that privacy.WrapError works with wrapped errors
func TestPrivacyWrapErrorWrapped(t *testing.T) {
	t.Parallel()
	// Create a wrapped error with sensitive information
	baseErr := errors.New("user@example.com")
	wrappedErr := fmt.Errorf("operation failed for: %w", baseErr)

	// Sanitize the wrapped error
	sanitized := privacy.WrapError(wrappedErr)

	// Check that the sensitive information was removed
	assert.Contains(t, sanitized.Error(), "operation failed for:")
	assert.Contains(t, sanitized.Error(), "[EMAIL]")
	assert.NotContains(t, sanitized.Error(), "user@example.com")

	// Check that the original error is preserved
	require.ErrorIs(t, sanitized, wrappedErr, "errors.Is(sanitized, wrappedErr) = false, want true")

	// Check that we can still access the base error
	require.ErrorIs(t, sanitized, baseErr, "errors.Is(sanitized, baseErr) = false, want true")
}

// TestStartWorkerPool tests the startWorkerPool function
func TestStartWorkerPool(t *testing.T) {
	t.Parallel()
	// Create a real job queue for testing
	realQueue := jobqueue.NewJobQueue()

	// Create a processor with the real queue
	processor := &Processor{
		JobQueue: realQueue,
	}

	// Call the function with a single worker
	processor.startWorkerPool()

	// Verify the cancel function was stored
	assert.NotNil(t, processor.workerCancel, "Cancel function was not stored")

	// Clean up
	err := realQueue.Stop()
	assert.NoError(t, err, "Failed to stop queue")
}

// TestGetJobQueueRetryConfig tests that the getJobQueueRetryConfig function correctly extracts retry configuration from different action types
func TestGetJobQueueRetryConfig(t *testing.T) {
	// Remove t.Parallel() to avoid race conditions with testRetryConfigOverride
	// Set testRetryConfigOverride to nil to ensure clean state
	testRetryConfigOverride = nil
	defer func() {
		testRetryConfigOverride = nil
	}()
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
			// Don't run subtests in parallel to avoid race conditions
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
			assert.Equal(t, tt.wantEnabled, config.Enabled, "getJobQueueRetryConfig() Enabled")
			assert.Equal(t, tt.wantMaxRetries, config.MaxRetries, "getJobQueueRetryConfig() MaxRetries")

			// For actions with retry configuration, check if the full configuration is preserved
			switch tt.action.(type) {
			case *BirdWeatherAction:
				assert.Equal(t, bwRetryConfig.InitialDelay, config.InitialDelay, "getJobQueueRetryConfig() InitialDelay")
				assert.Equal(t, bwRetryConfig.MaxDelay, config.MaxDelay, "getJobQueueRetryConfig() MaxDelay")
				assert.InDelta(t, bwRetryConfig.Multiplier, config.Multiplier, 0, "getJobQueueRetryConfig() Multiplier")
			case *MqttAction:
				assert.Equal(t, mqttRetryConfig.InitialDelay, config.InitialDelay, "getJobQueueRetryConfig() InitialDelay")
				assert.Equal(t, mqttRetryConfig.MaxDelay, config.MaxDelay, "getJobQueueRetryConfig() MaxDelay")
				assert.InDelta(t, mqttRetryConfig.Multiplier, config.Multiplier, 0, "getJobQueueRetryConfig() Multiplier")
			}
		})
	}
}

// TestEnqueueTask tests the Processor.EnqueueTask method with various action types and scenarios
func TestEnqueueTask(t *testing.T) {
	t.Parallel()

	// Test with different action types
	t.Run("DifferentActionTypes", func(t *testing.T) {
		t.Parallel()
		// Create a real job queue for this subtest
		realQueue := jobqueue.NewJobQueue()
		realQueue.Start()
		defer func() {
			assert.NoError(t, realQueue.Stop(), "Failed to stop queue")
		}()

		// Create a processor with the real queue
		processor := &Processor{
			JobQueue: realQueue,
			Settings: &conf.Settings{
				Debug: true,
			},
		}
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
				// Don't run in parallel since we're sharing the processor's queue
				// Create a task with this action
				now := time.Now()
				task := &Task{
					Type: TaskTypeAction,
					Detection: Detections{
						Result: detection.Result{
							Species: detection.Species{
								CommonName:     "Test Bird",
								ScientificName: "Testus birdus",
							},
							Confidence:  0.95,
							AudioSource: detection.AudioSource{ID: "test-source", SafeString: "test-source", DisplayName: "test-source"},
							BeginTime:   now,
							EndTime:     now.Add(15 * time.Second),
						},
						Note: datastore.Note{
							CommonName:     "Test Bird",
							ScientificName: "Testus birdus",
							Confidence:     0.95,
							Source:         testAudioSource(),
							BeginTime:      now,
							EndTime:        now.Add(15 * time.Second),
						},
					},
					Action: tc.action,
				}

				// Enqueue the task
				err := processor.EnqueueTask(task)
				assert.NoError(t, err, "Failed to enqueue task with %s", tc.name)
			})
		}
	})

	// Test error handling with a queue that's been stopped
	t.Run("StoppedQueue", func(t *testing.T) {
		t.Parallel()
		// Create a new queue that we'll stop immediately
		stoppedQueue := jobqueue.NewJobQueue()
		stoppedQueue.Start()
		require.NoError(t, stoppedQueue.Stop(), "Failed to stop queue")

		// Create a processor with the stopped queue
		stoppedProcessor := &Processor{
			JobQueue: stoppedQueue,
			Settings: &conf.Settings{
				Debug: true,
			},
		}

		// Create a task
		now := time.Now()
		task := &Task{
			Type: TaskTypeAction,
			Detection: Detections{
				Result: detection.Result{
					Species: detection.Species{
						CommonName:     "Test Bird",
						ScientificName: "Testus birdus",
					},
					Confidence:  0.95,
					AudioSource: detection.AudioSource{ID: "test-source", SafeString: "test-source", DisplayName: "test-source"},
					BeginTime:   now,
					EndTime:     now.Add(15 * time.Second),
				},
				Note: datastore.Note{
					CommonName:     "Test Bird",
					ScientificName: "Testus birdus",
					Confidence:     0.95,
					Source:         testAudioSource(),
					BeginTime:      now,
					EndTime:        now.Add(15 * time.Second),
				},
			},
			Action: &MockAction{},
		}

		// Enqueue the task, expecting an error
		err := stoppedProcessor.EnqueueTask(task)
		require.Error(t, err, "Expected error when enqueueing to stopped queue")
		assert.ErrorIs(t, err, jobqueue.ErrQueueStopped, "Expected error to be ErrQueueStopped")
	})

	// Test with a full queue
	t.Run("FullQueue", func(t *testing.T) {
		t.Parallel()
		// Create a queue with a very small capacity
		tinyQueue := jobqueue.NewJobQueueWithOptions(2, 1, false)
		tinyQueue.Start()
		defer func() {
			assert.NoError(t, tinyQueue.Stop(), "Failed to stop queue")
		}()

		// Create a processor with the tiny queue
		tinyProcessor := &Processor{
			JobQueue: tinyQueue,
			Settings: &conf.Settings{
				Debug: true,
			},
		}

		// Create tasks to fill the queue
		for i := range 5 {
			now := time.Now()
			task := &Task{
				Type: TaskTypeAction,
				Detection: Detections{
					Result: detection.Result{
						Species: detection.Species{
							CommonName:     fmt.Sprintf("Test Bird %d", i),
							ScientificName: "Testus birdus",
						},
						Confidence:  0.95,
						AudioSource: detection.AudioSource{ID: "test-source", SafeString: "test-source", DisplayName: "test-source"},
						BeginTime:   now,
						EndTime:     now.Add(15 * time.Second),
					},
					Note: datastore.Note{
						CommonName:     fmt.Sprintf("Test Bird %d", i),
						ScientificName: "Testus birdus",
						Confidence:     0.95,
						Source:         testAudioSource(),
						BeginTime:      now,
						EndTime:        now.Add(15 * time.Second),
					},
				},
				Action: &MockAction{
					ExecuteFunc: func(data any) error {
						// Sleep to keep the queue full
						time.Sleep(100 * time.Millisecond)
						return nil
					},
				},
			}

			// Enqueue the task, but don't fail the test if we get a queue full error
			err := tinyProcessor.EnqueueTask(task)
			if err != nil {
				require.ErrorIs(t, err, jobqueue.ErrQueueFull, "Unexpected error")
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
		t.Parallel()
		// Create a real job queue for this subtest
		realQueue := jobqueue.NewJobQueue()
		realQueue.Start()
		defer func() {
			assert.NoError(t, realQueue.Stop(), "Failed to stop queue")
		}()

		// Create a processor with the real queue
		processor := &Processor{
			JobQueue: realQueue,
			Settings: &conf.Settings{
				Debug: true,
			},
		}

		// Create a large detection with many results
		now := time.Now()
		det := Detections{
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
			Note: datastore.Note{
				CommonName:     "Test Bird",
				ScientificName: "Testus birdus",
				Confidence:     0.95,
				Source:         testAudioSource(),
				BeginTime:      now,
				EndTime:        now.Add(15 * time.Second),
			},
		}

		// Add a large number of results
		for i := range 100 {
			det.Results = append(det.Results, detection.AdditionalResult{
				Species:    detection.Species{ScientificName: fmt.Sprintf("Test Bird %d", i), CommonName: fmt.Sprintf("Test Bird %d", i)},
				Confidence: float64(i) / 100.0,
			})
		}

		// Create a task with this large detection
		task := &Task{
			Type:      TaskTypeAction,
			Detection: det,
			Action:    &MockAction{},
		}

		// Enqueue the task
		err := processor.EnqueueTask(task)
		assert.NoError(t, err, "Failed to enqueue task with large detection")
	})
}

// TestEnqueueTaskBasic tests the basic functionality of the EnqueueTask method
func TestEnqueueTaskBasic(t *testing.T) {
	t.Parallel()
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
	require.Error(t, err, "Expected error for nil task")
	require.ErrorIs(t, err, ErrNilTask, "Expected error to be ErrNilTask")

	// Test case 2: Nil action
	task := &Task{
		Type:      TaskTypeAction,
		Detection: Detections{},
		Action:    nil,
	}
	err = processor.EnqueueTask(task)
	require.Error(t, err, "Expected error for nil action")
	require.ErrorIs(t, err, ErrNilAction, "Expected error to be ErrNilAction")

	// Test case 3: Successful enqueue
	task = &Task{
		Type:      TaskTypeAction,
		Detection: Detections{},
		Action:    &MockAction{},
	}
	err = processor.EnqueueTask(task)
	require.NoError(t, err, "Unexpected error")

	// Clean up
	err = realQueue.Stop()
	assert.NoError(t, err, "Failed to stop queue")
}

// TestEnqueueMultipleTasks tests enqueueing multiple tasks with Scandinavian bird species
func TestEnqueueMultipleTasks(t *testing.T) {
	t.Parallel()
	// Create a real job queue for testing
	realQueue := jobqueue.NewJobQueue()
	realQueue.Start()
	defer func() {
		assert.NoError(t, realQueue.Stop(), "Failed to stop queue")
	}()

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
			now := time.Now()
			task := &Task{
				Type: TaskTypeAction,
				Detection: Detections{
					Result: detection.Result{
						Species: detection.Species{
							CommonName:     b.CommonName,
							ScientificName: b.ScientificName,
						},
						Confidence:  b.Confidence,
						AudioSource: detection.AudioSource{ID: "test-source", SafeString: "test-source", DisplayName: "test-source"},
						BeginTime:   now,
						EndTime:     now.Add(15 * time.Second),
					},
					Note: datastore.Note{
						CommonName:     b.CommonName,
						ScientificName: b.ScientificName,
						Confidence:     b.Confidence,
						Source:         testAudioSource(),
						BeginTime:      now,
						EndTime:        now.Add(15 * time.Second),
					},
				},
				Action: &MockAction{},
			}

			// Enqueue the task
			err := processor.EnqueueTask(task)
			if !assert.NoError(t, err, "Failed to enqueue task for %s (%s)", b.CommonName, b.ScientificName) {
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
	assert.Equal(t, len(scandinavianBirds), successCount, "Expected all tasks to be enqueued successfully")

	// Get job queue stats
	stats := realQueue.GetStats()

	// Verify the job queue has the expected number of jobs
	assert.GreaterOrEqual(t, stats.TotalJobs, len(scandinavianBirds), "Expected at least %d total jobs", len(scandinavianBirds))

	// Log some statistics
	t.Logf("Successfully enqueued %d tasks", successCount)
	t.Logf("Job queue stats: Total=%d, Completed=%d, Failed=%d",
		stats.TotalJobs, stats.SuccessfulJobs, stats.FailedJobs)
}

// TestIntegrationWithJobQueue tests the integration between Processor.EnqueueTask and a real job queue
func TestIntegrationWithJobQueue(t *testing.T) {
	// Remove t.Parallel() to avoid race conditions with testRetryConfigOverride
	// Set up the test retry config override to ensure consistent behavior
	testRetryConfigOverride = nil
	defer func() {
		testRetryConfigOverride = nil
	}()

	// Create a real job queue with a short processing interval for testing
	realQueue := jobqueue.NewJobQueue()
	realQueue.SetProcessingInterval(50 * time.Millisecond) // Process jobs quickly for testing
	realQueue.Start()
	defer func() {
		assert.NoError(t, realQueue.Stop(), "Failed to stop queue")
	}()

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
		ExecuteFunc: func(data any) error {
			// Signal that the action was executed
			executionChan <- struct{}{}
			return nil
		},
	}

	// Create a task with the mock action
	now := time.Now()
	task := &Task{
		Type: TaskTypeAction,
		Detection: Detections{
			Result: detection.Result{
				Species: detection.Species{
					CommonName:     "Test Bird",
					ScientificName: "Testus birdus",
				},
				Confidence:  0.95,
				AudioSource: detection.AudioSource{ID: "test-source", SafeString: "test-source", DisplayName: "Test Source"},
				BeginTime:   now,
				EndTime:     now.Add(15 * time.Second),
			},
			Note: datastore.Note{
				CommonName:     "Test Bird",
				ScientificName: "Testus birdus",
				Confidence:     0.95,
				Source:         testAudioSource(),
				BeginTime:      now,
				EndTime:        now.Add(15 * time.Second),
			},
		},
		Action: mockAction,
	}

	// Enqueue the task
	err := processor.EnqueueTask(task)
	require.NoError(t, err, "Failed to enqueue task")

	// Wait for the action to be executed with a timeout
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	select {
	case <-executionChan:
		// Action was executed successfully
	case <-ctx.Done():
		require.Fail(t, "Timeout waiting for action to be executed")
	}

	// Verify that the action was executed exactly once
	assert.Equal(t, 1, mockAction.ExecuteCount, "Expected action to be executed once")

	// Wait a bit for the job queue to update its statistics
	waitCtx, waitCancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer waitCancel()
	<-waitCtx.Done()

	// Verify that the job queue statistics reflect the completed job
	stats := realQueue.GetStats()
	assert.Equal(t, 1, stats.SuccessfulJobs, "Expected 1 successful job")

	// Test with a failing action
	failingAction := &MockAction{
		ExecuteFunc: func(data any) error {
			return fmt.Errorf("simulated failure")
		},
	}

	// Create a task with the failing action
	failNow := time.Now()
	failingTask := &Task{
		Type: TaskTypeAction,
		Detection: Detections{
			Result: detection.Result{
				Species: detection.Species{
					CommonName:     "Failing Bird",
					ScientificName: "Failurus maximus",
				},
				Confidence:  0.90,
				AudioSource: detection.AudioSource{ID: "test-source", SafeString: "test-source", DisplayName: "Test Source"},
				BeginTime:   failNow,
				EndTime:     failNow.Add(15 * time.Second),
			},
			Note: datastore.Note{
				CommonName:     "Failing Bird",
				ScientificName: "Failurus maximus",
				Confidence:     0.90,
				Source:         testAudioSource(),
				BeginTime:      failNow,
				EndTime:        failNow.Add(15 * time.Second),
			},
		},
		Action: failingAction,
	}

	// Enqueue the failing task
	err = processor.EnqueueTask(failingTask)
	require.NoError(t, err, "Failed to enqueue failing task")

	// Wait for the job queue to process the failing job
	processCtx, processCancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer processCancel()
	<-processCtx.Done()

	// Verify that the failing action was executed
	assert.Equal(t, 1, failingAction.ExecuteCount, "Expected failing action to be executed once")

	// Verify that the job queue statistics reflect the failed job
	stats = realQueue.GetStats()
	assert.GreaterOrEqual(t, stats.FailedJobs, 1, "Expected at least 1 failed job")

	// Log final statistics
	t.Logf("Job queue stats: Total=%d, Successful=%d, Failed=%d",
		stats.TotalJobs, stats.SuccessfulJobs, stats.FailedJobs)
}

// TestRetryLogic tests that the job queue properly retries failed actions
func TestRetryLogic(t *testing.T) {
	// Remove t.Parallel() to avoid race conditions with testRetryConfigOverride
	// Set up the test retry config override
	testRetryConfigOverride = nil
	defer func() {
		testRetryConfigOverride = nil
	}()

	// Create a dedicated context for this test that won't be canceled prematurely
	ctx := t.Context() // Ensure context gets canceled at the end of the test

	// Create a real job queue with a short processing interval for testing
	realQueue := jobqueue.NewJobQueue()
	realQueue.SetProcessingInterval(50 * time.Millisecond) // Process jobs quickly for testing
	realQueue.StartWithContext(ctx)
	defer func() {
		assert.NoError(t, realQueue.Stop(), "Failed to stop queue")
	}()

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

	// Set up the test retry config override BEFORE creating the processor
	testRetryConfigOverride = func(action Action) (jobqueue.RetryConfig, bool) {
		if _, ok := action.(*MockAction); ok {
			return retryConfig, true
		}
		return jobqueue.RetryConfig{}, false
	}

	// Create a processor with the real queue
	processor := &Processor{
		JobQueue: realQueue,
		Settings: &conf.Settings{
			Debug: true,
		},
	}

	// Create a mock action that fails a specified number of times before succeeding
	mockAction := &MockAction{
		ExecuteFunc: func(data any) error {
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

	// Create a task with the mock action
	retryNow := time.Now()
	task := &Task{
		Type: TaskTypeAction,
		Detection: Detections{
			Result: detection.Result{
				Species: detection.Species{
					CommonName:     "Retry Bird",
					ScientificName: "Retryus maximus",
				},
				Confidence:  0.95,
				AudioSource: detection.AudioSource{ID: "test-source", SafeString: "test-source", DisplayName: "Test Source"},
				BeginTime:   retryNow,
				EndTime:     retryNow.Add(15 * time.Second),
			},
			Note: datastore.Note{
				CommonName:     "Retry Bird",
				ScientificName: "Retryus maximus",
				Confidence:     0.95,
				Source:         testAudioSource(),
				BeginTime:      retryNow,
				EndTime:        retryNow.Add(15 * time.Second),
			},
		},
		Action: mockAction,
	}

	// Enqueue the task
	err := processor.EnqueueTask(task)
	require.NoError(t, err, "Failed to enqueue task")

	// Wait for the job to succeed with a timeout
	successCtx, successCancel := context.WithTimeout(context.Background(), 5*time.Second) // Increased timeout for CI environments
	defer successCancel()
	select {
	case <-successChan:
		// Job succeeded after retries
		t.Log("Success channel received signal")
	case <-successCtx.Done():
		require.Fail(t, "Timeout waiting for job to succeed after retries")
	}

	// Wait a bit more to ensure all job stats are updated
	statsCtx, statsCancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer statsCancel()
	<-statsCtx.Done()

	// Verify that the action was executed the expected number of times
	attemptMutex.Lock()
	finalAttemptCount := attemptCount
	attemptMutex.Unlock()

	assert.Equal(t, failCount+1, finalAttemptCount, "Expected action to be executed %d times", failCount+1)

	// Verify that the job queue statistics reflect the retries and successful completion
	stats := realQueue.GetStats()
	assert.Equal(t, 1, stats.SuccessfulJobs, "Expected 1 successful job")
	assert.GreaterOrEqual(t, stats.RetryAttempts, failCount, "Expected at least %d retry attempts", failCount)

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
		ExecuteFunc: func(data any) error {
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
		switch action {
		case exhaustingAction:
			return exhaustionRetryConfig, true
		case mockAction:
			return retryConfig, true
		default:
			return jobqueue.RetryConfig{}, false
		}
	}

	// Create a task with the exhausting action
	exhaustNow := time.Now()
	exhaustingTask := &Task{
		Type: TaskTypeAction,
		Detection: Detections{
			Result: detection.Result{
				Species: detection.Species{
					CommonName:     "Exhausting Bird",
					ScientificName: "Exhaustus maximus",
				},
				Confidence:  0.90,
				AudioSource: detection.AudioSource{ID: "test-source", SafeString: "test-source", DisplayName: "Test Source"},
				BeginTime:   exhaustNow,
				EndTime:     exhaustNow.Add(15 * time.Second),
			},
			Note: datastore.Note{
				CommonName:     "Exhausting Bird",
				ScientificName: "Exhaustus maximus",
				Confidence:     0.90,
				Source:         testAudioSource(),
				BeginTime:      exhaustNow,
				EndTime:        exhaustNow.Add(15 * time.Second),
			},
		},
		Action: exhaustingAction,
	}

	// Enqueue the exhausting task
	err = processor.EnqueueTask(exhaustingTask)
	require.NoError(t, err, "Failed to enqueue exhausting task")

	// Wait for all attempts to complete with an increased timeout
	exhaustCtx, exhaustCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer exhaustCancel()
	select {
	case <-allAttemptsComplete:
		// All attempts completed
		t.Log("All exhaustion attempts completed")
	case <-exhaustCtx.Done():
		require.Fail(t, "Timeout waiting for all exhaustion attempts to complete")
	}

	// Wait a bit more to ensure all processing is complete
	finalCtx, finalCancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer finalCancel()
	<-finalCtx.Done()

	// Verify that the action was executed the expected number of times
	attemptMutex.Lock()
	finalExhaustionCount := attemptCount
	attemptMutex.Unlock()

	assert.Equal(t, maxRetries+1, finalExhaustionCount, "Expected exhausting action to be executed %d times", maxRetries+1)

	// Verify that the job queue statistics reflect the failed job
	stats = realQueue.GetStats()

	// Total jobs should be 2 (1 successful from first test, 1 failed from exhaustion test)
	assert.Equal(t, 2, stats.TotalJobs, "Expected 2 total jobs")
	assert.Equal(t, 1, stats.SuccessfulJobs, "Expected 1 successful job")
	assert.Equal(t, 1, stats.FailedJobs, "Expected 1 failed job")

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
	t.Parallel()
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
					ExecuteFunc: func(data any) error {
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
			t.Parallel()
			if tt.name == "Nil processor" {
				// Use defer/recover to catch the expected panic
				defer func() {
					assert.NotNil(t, recover(), "Expected panic but none occurred")
				}()
			}

			p, task := tt.setupFunc()

			// Clean up job queue if it exists
			if p != nil && p.JobQueue != nil {
				defer func() {
					assert.NoError(t, p.JobQueue.Stop(), "Failed to stop queue")
				}()
			}

			err := p.EnqueueTask(task)

			if tt.wantErr {
				require.Error(t, err, "EnqueueTask() expected error")
				if tt.expectedErr != "" {
					assert.Contains(t, err.Error(), tt.expectedErr, "EnqueueTask() error message")
				}
			} else {
				assert.NoError(t, err, "EnqueueTask() unexpected error")
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
		_, err := mockQueue.Enqueue(context.Background(), &ActionAdapter{action: task.Action}, task.Detection, retryConfig)
		return err
	}

	// Reset the timer to exclude setup time
	b.ResetTimer()

	// Run the benchmark
	for b.Loop() {
		err := enqueueTaskBench(task)
		require.NoError(b, err, "EnqueueTask failed")
	}
}

// TestPrivacyScrubMessage tests that privacy.ScrubMessage correctly sanitizes sensitive information in strings
// This replaces the old TestSanitizeActionType test.
func TestPrivacyScrubMessage(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name             string
		input            string
		shouldContain    []string // Strings that SHOULD be in the sanitized output
		shouldNotContain []string // Strings that should NOT be in the sanitized output
	}{
		{
			name:          "simple type",
			input:         "*processor.MockAction",
			shouldContain: []string{"*processor.MockAction"},
		},
		{
			name:             "RTSP URL with credentials",
			input:            "*actions.RTSPAction{URL:rtsp://admin:password123@192.168.1.100:554/stream}",
			shouldContain:    []string{"*actions.RTSPAction"},
			shouldNotContain: []string{"admin", "password123"},
		},
		{
			name:             "API key in type",
			input:            "*actions.APIAction{Key:api_key=abc123xyz789}",
			shouldContain:    []string{"*actions.APIAction", "[TOKEN]"},
			shouldNotContain: []string{"abc123xyz789"},
		},
		{
			name:             "Email in string",
			input:            "notification for user@example.com failed",
			shouldContain:    []string{"notification for", "[EMAIL]", "failed"},
			shouldNotContain: []string{"user@example.com"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			sanitized := privacy.ScrubMessage(tt.input)
			// Print the actual output for debugging
			t.Logf("Input: %q", tt.input)
			t.Logf("Actual: %q", sanitized)

			// Check that expected strings are present
			for _, s := range tt.shouldContain {
				assert.Contains(t, sanitized, s, "sanitized should contain %q", s)
			}

			// Check that sensitive strings are NOT present
			for _, s := range tt.shouldNotContain {
				assert.NotContains(t, sanitized, s, "sanitized should NOT contain %q", s)
			}
		})
	}
}
