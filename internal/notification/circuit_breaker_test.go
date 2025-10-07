package notification

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestCircuitBreaker_ClosedState(t *testing.T) {
	config := CircuitBreakerConfig{
		MaxFailures:         3,
		Timeout:             100 * time.Millisecond,
		HalfOpenMaxRequests: 1,
	}
	cb := NewPushCircuitBreaker(config, nil, "test-provider")

	// Verify initial state
	if cb.State() != StateClosed {
		t.Errorf("expected initial state to be Closed, got %v", cb.State())
	}

	// Successful calls should keep circuit closed
	for i := 0; i < 5; i++ {
		err := cb.Call(context.Background(), func(ctx context.Context) error {
			return nil
		})
		if err != nil {
			t.Errorf("call %d failed: %v", i, err)
		}
		if cb.State() != StateClosed {
			t.Errorf("expected state to be Closed after success, got %v", cb.State())
		}
	}
}

func TestCircuitBreaker_TransitionToOpen(t *testing.T) {
	config := CircuitBreakerConfig{
		MaxFailures:         3,
		Timeout:             100 * time.Millisecond,
		HalfOpenMaxRequests: 1,
	}
	cb := NewPushCircuitBreaker(config, nil, "test-provider")

	testErr := errors.New("test error")

	// Make failures up to threshold - 1
	for i := 0; i < config.MaxFailures-1; i++ {
		err := cb.Call(context.Background(), func(ctx context.Context) error {
			return testErr
		})
		if !errors.Is(err, testErr) {
			t.Errorf("expected test error, got %v", err)
		}
		if cb.State() != StateClosed {
			t.Errorf("expected state to be Closed at failure %d, got %v", i, cb.State())
		}
	}

	// One more failure should open the circuit
	err := cb.Call(context.Background(), func(ctx context.Context) error {
		return testErr
	})
	if !errors.Is(err, testErr) {
		t.Errorf("expected test error, got %v", err)
	}
	if cb.State() != StateOpen {
		t.Errorf("expected state to be Open after max failures, got %v", cb.State())
	}

	// Subsequent calls should fail immediately with circuit breaker error
	err = cb.Call(context.Background(), func(ctx context.Context) error {
		t.Error("function should not be called when circuit is open")
		return nil
	})
	if !errors.Is(err, ErrCircuitBreakerOpen) {
		t.Errorf("expected ErrCircuitBreakerOpen, got %v", err)
	}
}

func TestCircuitBreaker_TransitionToHalfOpen(t *testing.T) {
	config := CircuitBreakerConfig{
		MaxFailures:         2,
		Timeout:             50 * time.Millisecond,
		HalfOpenMaxRequests: 1,
	}
	cb := NewPushCircuitBreaker(config, nil, "test-provider")

	testErr := errors.New("test error")

	// Open the circuit
	for i := 0; i < config.MaxFailures; i++ {
		_ = cb.Call(context.Background(), func(ctx context.Context) error {
			return testErr
		})
	}

	if cb.State() != StateOpen {
		t.Fatalf("expected circuit to be Open, got %v", cb.State())
	}

	// Wait for timeout
	time.Sleep(config.Timeout + 10*time.Millisecond)

	// Next call should transition to half-open
	callMade := false
	err := cb.Call(context.Background(), func(ctx context.Context) error {
		callMade = true
		return nil
	})

	if err != nil {
		t.Errorf("expected successful call in half-open state, got error: %v", err)
	}

	if !callMade {
		t.Error("expected function to be called in half-open state")
	}

	// Should transition back to closed after successful call
	if cb.State() != StateClosed {
		t.Errorf("expected state to be Closed after successful half-open call, got %v", cb.State())
	}
}

func TestCircuitBreaker_HalfOpenFailure(t *testing.T) {
	config := CircuitBreakerConfig{
		MaxFailures:         2,
		Timeout:             50 * time.Millisecond,
		HalfOpenMaxRequests: 1,
	}
	cb := NewPushCircuitBreaker(config, nil, "test-provider")

	testErr := errors.New("test error")

	// Open the circuit
	for i := 0; i < config.MaxFailures; i++ {
		_ = cb.Call(context.Background(), func(ctx context.Context) error {
			return testErr
		})
	}

	// Wait for timeout to allow half-open
	time.Sleep(config.Timeout + 10*time.Millisecond)

	// Fail in half-open state should reopen circuit
	err := cb.Call(context.Background(), func(ctx context.Context) error {
		return testErr
	})

	if !errors.Is(err, testErr) {
		t.Errorf("expected test error, got %v", err)
	}

	if cb.State() != StateOpen {
		t.Errorf("expected state to be Open after half-open failure, got %v", cb.State())
	}
}

func TestCircuitBreaker_HalfOpenMaxRequests(t *testing.T) {
	config := CircuitBreakerConfig{
		MaxFailures:         2,
		Timeout:             50 * time.Millisecond,
		HalfOpenMaxRequests: 2,
	}
	cb := NewPushCircuitBreaker(config, nil, "test-provider")

	testErr := errors.New("test error")

	// Open the circuit
	for i := 0; i < config.MaxFailures; i++ {
		_ = cb.Call(context.Background(), func(ctx context.Context) error {
			return testErr
		})
	}

	if cb.State() != StateOpen {
		t.Fatalf("expected circuit to be Open after failures, got %v", cb.State())
	}

	// Wait for timeout to allow transition to half-open
	time.Sleep(config.Timeout + 10*time.Millisecond)

	// Fire N+1 concurrent calls to test the half-open limit
	var wg sync.WaitGroup
	errChan := make(chan error, config.HalfOpenMaxRequests+1)
	blocker := make(chan struct{})

	// Launch HalfOpenMaxRequests + 1 concurrent calls
	for i := 0; i < config.HalfOpenMaxRequests+1; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			err := cb.Call(context.Background(), func(ctx context.Context) error {
				// Block to ensure concurrent execution
				<-blocker
				return testErr
			})
			errChan <- err
		}(i)
	}

	// Give goroutines time to start and call beforeCall()
	time.Sleep(10 * time.Millisecond)

	// Unblock all calls
	close(blocker)
	wg.Wait()
	close(errChan)

	// Count how many got ErrTooManyRequests
	tooManyCount := 0
	otherErrCount := 0
	for err := range errChan {
		if errors.Is(err, ErrTooManyRequests) {
			tooManyCount++
		} else if err != nil {
			otherErrCount++
		}
	}

	// At least one should be rejected with ErrTooManyRequests
	if tooManyCount == 0 {
		t.Errorf("expected at least 1 ErrTooManyRequests when exceeding HalfOpenMaxRequests=%d, got 0", config.HalfOpenMaxRequests)
	}

	// The others should get through and fail with testErr
	if otherErrCount < config.HalfOpenMaxRequests {
		t.Logf("Note: %d calls got through, expected ~%d", otherErrCount, config.HalfOpenMaxRequests)
	}
}

func TestCircuitBreaker_Reset(t *testing.T) {
	config := CircuitBreakerConfig{
		MaxFailures:         2,
		Timeout:             100 * time.Millisecond,
		HalfOpenMaxRequests: 1,
	}
	cb := NewPushCircuitBreaker(config, nil, "test-provider")

	testErr := errors.New("test error")

	// Open the circuit
	for i := 0; i < config.MaxFailures; i++ {
		_ = cb.Call(context.Background(), func(ctx context.Context) error {
			return testErr
		})
	}

	if cb.State() != StateOpen {
		t.Fatalf("expected circuit to be Open, got %v", cb.State())
	}

	// Reset should close circuit
	cb.Reset()

	if cb.State() != StateClosed {
		t.Errorf("expected state to be Closed after reset, got %v", cb.State())
	}

	if cb.Failures() != 0 {
		t.Errorf("expected failures to be 0 after reset, got %d", cb.Failures())
	}

	// Should allow calls
	err := cb.Call(context.Background(), func(ctx context.Context) error {
		return nil
	})
	if err != nil {
		t.Errorf("expected successful call after reset, got error: %v", err)
	}
}

func TestCircuitBreaker_IsHealthy(t *testing.T) {
	config := CircuitBreakerConfig{
		MaxFailures:         2,
		Timeout:             50 * time.Millisecond,
		HalfOpenMaxRequests: 1,
	}
	cb := NewPushCircuitBreaker(config, nil, "test-provider")

	// Initially healthy
	if !cb.IsHealthy() {
		t.Error("expected circuit to be healthy initially")
	}

	// Open circuit
	testErr := errors.New("test error")
	for i := 0; i < config.MaxFailures; i++ {
		_ = cb.Call(context.Background(), func(ctx context.Context) error {
			return testErr
		})
	}

	// Should be unhealthy when open
	if cb.IsHealthy() {
		t.Error("expected circuit to be unhealthy when open")
	}

	// Wait for half-open
	time.Sleep(config.Timeout + 10*time.Millisecond)

	// Successful call should restore health
	_ = cb.Call(context.Background(), func(ctx context.Context) error {
		return nil
	})

	if !cb.IsHealthy() {
		t.Error("expected circuit to be healthy after successful recovery")
	}
}

func TestCircuitBreaker_GetStats(t *testing.T) {
	config := CircuitBreakerConfig{
		MaxFailures:         3,
		Timeout:             100 * time.Millisecond,
		HalfOpenMaxRequests: 1,
	}
	cb := NewPushCircuitBreaker(config, nil, "test-provider")

	testErr := errors.New("test error")

	// Make some failures
	for i := 0; i < 2; i++ {
		_ = cb.Call(context.Background(), func(ctx context.Context) error {
			return testErr
		})
	}

	stats := cb.GetStats()

	if stats.State != StateClosed {
		t.Errorf("expected state Closed, got %s", stats.State)
	}

	if stats.Failures != 2 {
		t.Errorf("expected 2 failures, got %d", stats.Failures)
	}

	if stats.LastFailureTime.IsZero() {
		t.Error("expected LastFailureTime to be set")
	}
}

func TestCircuitBreaker_ConcurrentCalls(t *testing.T) {
	config := CircuitBreakerConfig{
		MaxFailures:         10,
		Timeout:             100 * time.Millisecond,
		HalfOpenMaxRequests: 1,
	}
	cb := NewPushCircuitBreaker(config, nil, "test-provider")

	// Run concurrent successful calls
	errChan := make(chan error, 100)
	done := make(chan bool, 100)
	for i := 0; i < 100; i++ {
		go func() {
			err := cb.Call(context.Background(), func(ctx context.Context) error {
				time.Sleep(1 * time.Millisecond)
				return nil
			})
			if err != nil {
				errChan <- err
			}
			done <- true
		}()
	}

	// Wait for all to complete
	for i := 0; i < 100; i++ {
		<-done
	}
	close(errChan)

	// Check for errors from main goroutine
	for err := range errChan {
		t.Errorf("concurrent call failed: %v", err)
	}

	if cb.State() != StateClosed {
		t.Errorf("expected state to be Closed after concurrent successes, got %v", cb.State())
	}
}

func TestCircuitBreaker_ContextCancellation(t *testing.T) {
	config := CircuitBreakerConfig{
		MaxFailures:         3,
		Timeout:             100 * time.Millisecond,
		HalfOpenMaxRequests: 1,
	}
	cb := NewPushCircuitBreaker(config, nil, "test-provider")

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	err := cb.Call(ctx, func(ctx context.Context) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
			return nil
		}
	})

	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled error, got %v", err)
	}

	// Context cancellation should NOT count as provider failure (it's client-side)
	// The circuit breaker should only open for actual provider issues
	if cb.Failures() != 0 {
		t.Errorf("expected 0 failures from context cancellation (client-side), got %d", cb.Failures())
	}
}
