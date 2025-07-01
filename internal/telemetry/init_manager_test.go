package telemetry

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestInitManager_ConcurrentInitialization(t *testing.T) {
	t.Parallel()

	// This test verifies thread-safe initialization under concurrent access
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
			if err != nil {
				t.Errorf("Error integration initialization failed: %v", err)
			}
		}()
	}
	wg.Wait()

	// Verify it was only initialized once
	state := manager.GetComponentState("error_integration")
	if state != InitStateCompleted {
		t.Errorf("Expected error_integration state to be completed, got %s", state)
	}
}

func TestInitManager_StateTransitions(t *testing.T) {
	t.Parallel()

	// Create a fresh manager for this test
	manager := &InitManager{
		logger: getLoggerSafe("test"),
	}

	// Test state transitions
	components := []string{"error_integration", "sentry", "event_bus", "worker"}

	for _, comp := range components {
		// Initial state should be not started
		if state := manager.GetComponentState(comp); state != InitStateNotStarted {
			t.Errorf("Expected %s initial state to be not_started, got %s", comp, state)
		}
	}

	// Simulate state changes
	manager.errorIntegration.Store(int32(InitStateInProgress))
	if state := manager.GetComponentState("error_integration"); state != InitStateInProgress {
		t.Errorf("Expected error_integration state to be in_progress, got %s", state)
	}

	manager.errorIntegration.Store(int32(InitStateCompleted))
	if state := manager.GetComponentState("error_integration"); state != InitStateCompleted {
		t.Errorf("Expected error_integration state to be completed, got %s", state)
	}
}

func TestInitManager_WaitForComponent(t *testing.T) {
	t.Parallel()

	manager := &InitManager{
		logger: getLoggerSafe("test"),
	}

	// Test immediate success
	manager.errorIntegration.Store(int32(InitStateCompleted))
	err := manager.WaitForComponent("error_integration", InitStateCompleted, 100*time.Millisecond)
	if err != nil {
		t.Errorf("WaitForComponent failed for completed component: %v", err)
	}

	// Test timeout
	err = manager.WaitForComponent("sentry", InitStateCompleted, 50*time.Millisecond)
	if err == nil {
		t.Error("Expected timeout error, got nil")
	}

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
	if err != nil {
		t.Errorf("WaitForComponent failed when state changed: %v", err)
	}
}

func TestInitManager_HealthCheck(t *testing.T) {
	t.Parallel()

	manager := &InitManager{
		logger: getLoggerSafe("test"),
	}

	// Set various states
	manager.errorIntegration.Store(int32(InitStateCompleted))
	manager.sentryClient.Store(int32(InitStateFailed))
	manager.sentryErr.Store(errTest)
	manager.eventBus.Store(int32(InitStateInProgress))
	manager.telemetryWorker.Store(int32(InitStateNotStarted))

	health := manager.HealthCheck()

	// Check overall health (should be false due to failed component)
	if health.Healthy {
		t.Error("Expected overall health to be false")
	}

	// Check individual component health
	if health.Components["error_integration"].Healthy != true {
		t.Error("Expected error_integration to be healthy")
	}

	if health.Components["sentry"].Healthy != false {
		t.Error("Expected sentry to be unhealthy")
	}

	if health.Components["sentry"].Error == "" {
		t.Error("Expected sentry to have error message")
	}

	if health.Components["event_bus"].State != InitStateInProgress {
		t.Errorf("Expected event_bus state to be in_progress, got %s", 
			health.Components["event_bus"].State)
	}
}

func TestInitManager_Shutdown(t *testing.T) {
	t.Parallel()

	// Create a fresh manager for this test to avoid interference
	manager := &InitManager{
		logger: getLoggerSafe("test"),
	}

	// Set some states
	manager.telemetryWorker.Store(int32(InitStateCompleted))
	manager.eventBus.Store(int32(InitStateCompleted))

	// Test shutdown with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := manager.Shutdown(ctx)
	if err != nil {
		t.Errorf("Shutdown failed: %v", err)
	}

	// Verify states were reset
	if state := manager.GetComponentState("worker"); state != InitStateNotStarted {
		t.Errorf("Expected worker state to be not_started after shutdown, got %s", state)
	}

	if state := manager.GetComponentState("event_bus"); state != InitStateNotStarted {
		t.Errorf("Expected event_bus state to be not_started after shutdown, got %s", state)
	}
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