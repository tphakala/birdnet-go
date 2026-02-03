package telemetry

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInitManager_ConcurrentInitialization(t *testing.T) {
	t.Parallel()

	// This test verifies thread-safe initialization under concurrent access.
	// Note: Uses singleton manager which tests actual production behavior.
	// State may persist from previous tests, which is acceptable for concurrency testing.
	manager := GetInitManager()

	// Launch multiple goroutines trying to initialize components
	var wg sync.WaitGroup
	numGoroutines := 10

	// Test concurrent error integration initialization
	wg.Add(numGoroutines)
	for range numGoroutines {
		go func() {
			defer wg.Done()
			err := manager.InitializeErrorIntegrationSafe()
			assert.NoError(t, err, "Error integration initialization should succeed")
		}()
	}
	wg.Wait()

	// Verify it was only initialized once
	state := manager.GetComponentState("error_integration")
	assert.Equal(t, InitStateCompleted, state, "Expected error_integration state to be completed")
}

func TestInitManager_StateTransitions(t *testing.T) {
	t.Parallel()

	// Create a fresh manager for this test
	manager := &InitManager{
		initLog: GetLogger(),
	}

	// Test state transitions
	components := []string{"error_integration", "sentry", "event_bus", "worker"}

	for _, comp := range components {
		// Initial state should be not started
		state := manager.GetComponentState(comp)
		assert.Equal(t, InitStateNotStarted, state, "Expected %s initial state to be not_started", comp)
	}

	// Simulate state changes
	manager.errorIntegration.Store(int32(InitStateInProgress))
	state := manager.GetComponentState("error_integration")
	assert.Equal(t, InitStateInProgress, state, "Expected error_integration state to be in_progress")

	manager.errorIntegration.Store(int32(InitStateCompleted))
	state = manager.GetComponentState("error_integration")
	assert.Equal(t, InitStateCompleted, state, "Expected error_integration state to be completed")
}

func TestInitManager_WaitForComponent(t *testing.T) {
	t.Parallel()

	manager := &InitManager{
		initLog: GetLogger(),
	}

	// Test immediate success
	manager.errorIntegration.Store(int32(InitStateCompleted))
	err := manager.WaitForComponent("error_integration", InitStateCompleted, 100*time.Millisecond)
	require.NoError(t, err, "WaitForComponent should succeed for completed component")

	// Test timeout
	err = manager.WaitForComponent("sentry", InitStateCompleted, 50*time.Millisecond)
	require.Error(t, err, "Expected timeout error")

	// Test state change during wait using channel synchronization
	ready := make(chan struct{})
	stateSet := make(chan struct{})

	go func() {
		<-ready
		manager.eventBus.Store(int32(InitStateCompleted))
		close(stateSet)
	}()

	// Start the wait in a goroutine
	waitErr := make(chan error, 1)
	go func() {
		waitErr <- manager.WaitForComponent("event_bus", InitStateCompleted, 100*time.Millisecond)
	}()

	// Signal to set the state
	close(ready)

	// Wait for state to be set
	<-stateSet

	// Get the result
	err = <-waitErr
	assert.NoError(t, err, "WaitForComponent should succeed when state changes")
}

func TestInitManager_HealthCheck(t *testing.T) {
	t.Parallel()

	manager := &InitManager{
		initLog: GetLogger(),
	}

	// Set various states
	manager.errorIntegration.Store(int32(InitStateCompleted))
	manager.sentryClient.Store(int32(InitStateFailed))
	manager.sentryErr.Store(errTest)
	manager.eventBus.Store(int32(InitStateInProgress))
	manager.telemetryWorker.Store(int32(InitStateNotStarted))

	health := manager.HealthCheck()

	// Check overall health (should be false due to failed component)
	assert.False(t, health.Healthy, "Expected overall health to be false")

	// Check individual component health
	assert.True(t, health.Components["error_integration"].Healthy, "Expected error_integration to be healthy")
	assert.False(t, health.Components["sentry"].Healthy, "Expected sentry to be unhealthy")
	assert.NotEmpty(t, health.Components["sentry"].Error, "Expected sentry to have error message")
	assert.Equal(t, InitStateInProgress, health.Components["event_bus"].State, "Expected event_bus state to be in_progress")
}

func TestInitManager_Shutdown(t *testing.T) {
	t.Parallel()

	// Create a fresh manager for this test to avoid interference
	manager := &InitManager{
		initLog: GetLogger(),
	}

	// Set some states
	manager.telemetryWorker.Store(int32(InitStateCompleted))
	manager.eventBus.Store(int32(InitStateCompleted))

	// Test shutdown with timeout
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()

	err := manager.Shutdown(ctx)
	require.NoError(t, err, "Shutdown should succeed")

	// Verify states were reset
	state := manager.GetComponentState("worker")
	assert.Equal(t, InitStateNotStarted, state, "Expected worker state to be not_started after shutdown")

	state = manager.GetComponentState("event_bus")
	assert.Equal(t, InitStateNotStarted, state, "Expected event_bus state to be not_started after shutdown")
}

// Test concurrent access to health checks
func TestInitManager_ConcurrentHealthChecks(t *testing.T) {
	t.Parallel()

	manager := GetInitManager()

	// Set up some initial state
	manager.errorIntegration.Store(int32(InitStateCompleted))
	manager.sentryClient.Store(int32(InitStateInProgress))

	var wg sync.WaitGroup
	numGoroutines := 20

	// Launch concurrent health checks
	wg.Add(numGoroutines)
	for range numGoroutines {
		go func() {
			defer wg.Done()

			// Perform multiple operations
			for j := range 10 {
				health := manager.HealthCheck()
				_ = health.Healthy

				state := manager.GetComponentState("error_integration")
				_ = state.String()

				// Simulate state changes
				if j%3 == 0 {
					manager.eventBus.Store(int32(InitStateInProgress))
				}
			}
		}()
	}

	wg.Wait()
}

// errTest is a test error for validation
var errTest = &testError{msg: "test error"}

type testError struct {
	msg string
}

func (e *testError) Error() string {
	return e.msg
}
