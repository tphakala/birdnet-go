package notification

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCircuitBreaker_ClosedState(t *testing.T) {
	t.Parallel()

	cb := newTestCircuitBreaker(t, DefaultCircuitBreakerTestConfig())

	// Verify initial state
	assertCircuitState(t, cb, StateClosed)

	// Successful calls should keep circuit closed
	for i := range 5 {
		err := cb.Call(context.Background(), func(_ context.Context) error {
			return nil
		})
		require.NoError(t, err, "call %d should succeed", i)
		assertCircuitState(t, cb, StateClosed)
	}
}

func TestCircuitBreaker_TransitionToOpen(t *testing.T) {
	t.Parallel()

	config := DefaultCircuitBreakerTestConfig()
	cb := newTestCircuitBreaker(t, config)

	testErr := errors.New("test error")

	// Make failures up to threshold - 1
	for i := 0; i < config.MaxFailures-1; i++ {
		err := cb.Call(context.Background(), func(_ context.Context) error {
			return testErr
		})
		require.ErrorIs(t, err, testErr, "call %d should return test error", i)
		assertCircuitState(t, cb, StateClosed)
	}

	// One more failure should open the circuit
	err := cb.Call(context.Background(), func(_ context.Context) error {
		return testErr
	})
	require.ErrorIs(t, err, testErr)
	assertCircuitState(t, cb, StateOpen)

	// Subsequent calls should fail immediately with circuit breaker error
	err = cb.Call(context.Background(), func(_ context.Context) error {
		t.Error("function should not be called when circuit is open")
		return nil
	})
	require.ErrorIs(t, err, ErrCircuitBreakerOpen)
}

func TestCircuitBreaker_TransitionToHalfOpen(t *testing.T) {
	t.Parallel()

	config := CircuitBreakerConfig{
		MaxFailures:         2,
		Timeout:             50 * time.Millisecond,
		HalfOpenMaxRequests: 1,
	}
	cb := newTestCircuitBreaker(t, config)

	// Open the circuit
	openCircuitBreaker(t, cb, config.MaxFailures)
	require.Equal(t, StateOpen, cb.State(), "circuit should be open")

	// Wait for timeout
	time.Sleep(config.Timeout + 10*time.Millisecond)

	// Next call should transition to half-open
	callMade := false
	err := cb.Call(context.Background(), func(_ context.Context) error {
		callMade = true
		return nil
	})

	require.NoError(t, err, "call in half-open state should succeed")
	assert.True(t, callMade, "function should be called in half-open state")
	assertCircuitState(t, cb, StateClosed)
}

func TestCircuitBreaker_HalfOpenFailure(t *testing.T) {
	t.Parallel()

	config := CircuitBreakerConfig{
		MaxFailures:         2,
		Timeout:             50 * time.Millisecond,
		HalfOpenMaxRequests: 1,
	}
	cb := newTestCircuitBreaker(t, config)

	testErr := errors.New("test error")

	// Open the circuit
	openCircuitBreaker(t, cb, config.MaxFailures)

	// Wait for timeout to allow half-open
	time.Sleep(config.Timeout + 10*time.Millisecond)

	// Fail in half-open state should reopen circuit
	err := cb.Call(context.Background(), func(_ context.Context) error {
		return testErr
	})

	require.ErrorIs(t, err, testErr)
	assertCircuitState(t, cb, StateOpen)
}

func TestCircuitBreaker_HalfOpenMaxRequests(t *testing.T) {
	t.Parallel()

	config := CircuitBreakerConfig{
		MaxFailures:         2,
		Timeout:             50 * time.Millisecond,
		HalfOpenMaxRequests: 2,
	}
	cb := newTestCircuitBreaker(t, config)

	testErr := errors.New("test error")

	// Open the circuit
	openCircuitBreaker(t, cb, config.MaxFailures)
	require.Equal(t, StateOpen, cb.State(), "circuit should be open after failures")

	// Wait for timeout to allow transition to half-open
	time.Sleep(config.Timeout + 10*time.Millisecond)

	// Fire N+1 concurrent calls to test the half-open limit
	var wg sync.WaitGroup
	errChan := make(chan error, config.HalfOpenMaxRequests+1)
	blocker := make(chan struct{})

	// Launch HalfOpenMaxRequests + 1 concurrent calls
	for i := 0; i < config.HalfOpenMaxRequests+1; i++ {
		wg.Go(func() {
			err := cb.Call(context.Background(), func(_ context.Context) error {
				// Block to ensure concurrent execution
				<-blocker
				return testErr
			})
			errChan <- err
		})
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

	// Exactly one should be rejected (N+1 callers with N allowed)
	assert.Equal(t, 1, tooManyCount,
		"expected exactly 1 ErrTooManyRequests when exceeding HalfOpenMaxRequests=%d",
		config.HalfOpenMaxRequests)

	// The others should get through and fail with testErr
	assert.Equal(t, config.HalfOpenMaxRequests, otherErrCount,
		"expected %d half-open probes to execute", config.HalfOpenMaxRequests)
}

func TestCircuitBreaker_Reset(t *testing.T) {
	t.Parallel()

	config := CircuitBreakerConfig{
		MaxFailures:         2,
		Timeout:             100 * time.Millisecond,
		HalfOpenMaxRequests: 1,
	}
	cb := newTestCircuitBreaker(t, config)

	// Open the circuit
	openCircuitBreaker(t, cb, config.MaxFailures)
	require.Equal(t, StateOpen, cb.State(), "circuit should be open")

	// Reset should close circuit
	cb.Reset()

	assertCircuitState(t, cb, StateClosed)
	assert.Equal(t, 0, cb.Failures(), "failures should be 0 after reset")

	// Should allow calls
	err := cb.Call(context.Background(), func(_ context.Context) error {
		return nil
	})
	require.NoError(t, err, "call should succeed after reset")
}

func TestCircuitBreaker_IsHealthy(t *testing.T) {
	t.Parallel()

	config := CircuitBreakerConfig{
		MaxFailures:         2,
		Timeout:             50 * time.Millisecond,
		HalfOpenMaxRequests: 1,
	}
	cb := newTestCircuitBreaker(t, config)

	// Initially healthy
	assertCircuitHealthy(t, cb, true)

	// Open circuit
	openCircuitBreaker(t, cb, config.MaxFailures)

	// Should be unhealthy when open
	assertCircuitHealthy(t, cb, false)

	// Wait for half-open
	time.Sleep(config.Timeout + 10*time.Millisecond)

	// Successful call should restore health
	err := cb.Call(context.Background(), func(_ context.Context) error {
		return nil
	})
	require.NoError(t, err)

	assertCircuitHealthy(t, cb, true)
}

func TestCircuitBreaker_GetStats(t *testing.T) {
	t.Parallel()

	cb := newTestCircuitBreaker(t, DefaultCircuitBreakerTestConfig())

	testErr := errors.New("test error")

	// Make some failures
	for range 2 {
		_ = cb.Call(context.Background(), func(_ context.Context) error {
			return testErr
		})
	}

	stats := cb.GetStats()

	assert.Equal(t, StateClosed, stats.State)
	assert.Equal(t, 2, stats.Failures)
	assert.False(t, stats.LastFailureTime.IsZero(), "LastFailureTime should be set")
}

func TestCircuitBreaker_ConcurrentCalls(t *testing.T) {
	t.Parallel()

	config := CircuitBreakerConfig{
		MaxFailures:         10,
		Timeout:             100 * time.Millisecond,
		HalfOpenMaxRequests: 1,
	}
	cb := newTestCircuitBreaker(t, config)

	const numCalls = 100

	// Run concurrent successful calls
	errChan := make(chan error, numCalls)
	done := make(chan bool, numCalls)
	for range numCalls {
		go func() {
			err := cb.Call(context.Background(), func(_ context.Context) error {
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
	for range numCalls {
		<-done
	}
	close(errChan)

	// Check for errors
	for err := range errChan {
		t.Errorf("concurrent call failed: %v", err)
	}

	assertCircuitState(t, cb, StateClosed)
}

func TestCircuitBreaker_ContextCancellation(t *testing.T) {
	t.Parallel()

	cb := newTestCircuitBreaker(t, DefaultCircuitBreakerTestConfig())

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

	require.ErrorIs(t, err, context.Canceled)

	// Context cancellation should NOT count as provider failure (it's client-side)
	assert.Equal(t, 0, cb.Failures(),
		"context cancellation (client-side) should not count as provider failure")
}
