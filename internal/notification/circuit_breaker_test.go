package notification

import (
	"context"
	"errors"
	"sync"
	"testing"
	"testing/synctest"
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
		err := cb.Call(t.Context(), func(_ context.Context) error {
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
	for i := range config.MaxFailures - 1 {
		err := cb.Call(t.Context(), func(_ context.Context) error {
			return testErr
		})
		require.ErrorIs(t, err, testErr, "call %d should return test error", i)
		assertCircuitState(t, cb, StateClosed)
	}

	// One more failure should open the circuit
	err := cb.Call(t.Context(), func(_ context.Context) error {
		return testErr
	})
	require.ErrorIs(t, err, testErr)
	assertCircuitState(t, cb, StateOpen)

	// Subsequent calls should fail immediately with circuit breaker error
	functionCalled := false
	err = cb.Call(t.Context(), func(_ context.Context) error {
		functionCalled = true
		return nil
	})
	require.ErrorIs(t, err, ErrCircuitBreakerOpen)
	assert.False(t, functionCalled, "function should not be called when circuit is open")
}

func TestCircuitBreaker_TransitionToHalfOpen(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		config := CircuitBreakerConfig{
			MaxFailures:         2,
			Timeout:             50 * time.Millisecond,
			HalfOpenMaxRequests: 1,
		}
		cb := NewPushCircuitBreaker(config, nil, "test-provider")
		require.NotNil(t, cb, "NewPushCircuitBreaker should return non-nil")

		// Open the circuit
		ctx := t.Context()
		for range config.MaxFailures {
			_ = cb.Call(ctx, func(_ context.Context) error {
				return assert.AnError
			})
		}
		require.Equal(t, StateOpen, cb.State(), "circuit should be open")

		// Wait for timeout - instant with synctest
		time.Sleep(config.Timeout + 10*time.Millisecond)

		// Next call should transition to half-open
		callMade := false
		err := cb.Call(ctx, func(_ context.Context) error {
			callMade = true
			return nil
		})

		require.NoError(t, err, "call in half-open state should succeed")
		assert.True(t, callMade, "function should be called in half-open state")
		assert.Equal(t, StateClosed, cb.State(), "circuit breaker state mismatch")
	})
}

func TestCircuitBreaker_HalfOpenFailure(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		config := CircuitBreakerConfig{
			MaxFailures:         2,
			Timeout:             50 * time.Millisecond,
			HalfOpenMaxRequests: 1,
		}
		cb := NewPushCircuitBreaker(config, nil, "test-provider")
		require.NotNil(t, cb, "NewPushCircuitBreaker should return non-nil")

		testErr := errors.New("test error")

		// Open the circuit
		ctx := t.Context()
		for range config.MaxFailures {
			_ = cb.Call(ctx, func(_ context.Context) error {
				return assert.AnError
			})
		}

		// Wait for timeout to allow half-open - instant with synctest
		time.Sleep(config.Timeout + 10*time.Millisecond)

		// Fail in half-open state should reopen circuit
		err := cb.Call(ctx, func(_ context.Context) error {
			return testErr
		})

		require.ErrorIs(t, err, testErr)
		assert.Equal(t, StateOpen, cb.State(), "circuit breaker state mismatch")
	})
}

func TestCircuitBreaker_HalfOpenMaxRequests(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		config := CircuitBreakerConfig{
			MaxFailures:         2,
			Timeout:             50 * time.Millisecond,
			HalfOpenMaxRequests: 2,
		}
		cb := NewPushCircuitBreaker(config, nil, "test-provider")
		require.NotNil(t, cb, "NewPushCircuitBreaker should return non-nil")

		testErr := errors.New("test error")

		// Open the circuit
		ctx := t.Context()
		for range config.MaxFailures {
			_ = cb.Call(ctx, func(_ context.Context) error {
				return assert.AnError
			})
		}
		require.Equal(t, StateOpen, cb.State(), "circuit should be open after failures")

		// Wait for timeout to allow transition to half-open - instant with synctest
		time.Sleep(config.Timeout + 10*time.Millisecond)

		// Fire N+1 concurrent calls to test the half-open limit
		var wg sync.WaitGroup
		errChan := make(chan error, config.HalfOpenMaxRequests+1)
		blocker := make(chan struct{})

		// Launch HalfOpenMaxRequests + 1 concurrent calls
		for range config.HalfOpenMaxRequests + 1 {
			wg.Go(func() {
				err := cb.Call(ctx, func(_ context.Context) error {
					// Block to ensure concurrent execution
					<-blocker
					return testErr
				})
				errChan <- err
			})
		}

		// Wait for goroutines to block on blocker channel
		synctest.Wait()

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
	})
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
	err := cb.Call(t.Context(), func(_ context.Context) error {
		return nil
	})
	require.NoError(t, err, "call should succeed after reset")
}

func TestCircuitBreaker_IsHealthy(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		config := CircuitBreakerConfig{
			MaxFailures:         2,
			Timeout:             50 * time.Millisecond,
			HalfOpenMaxRequests: 1,
		}
		cb := NewPushCircuitBreaker(config, nil, "test-provider")
		require.NotNil(t, cb, "NewPushCircuitBreaker should return non-nil")

		// Initially healthy
		assert.True(t, cb.IsHealthy(), "circuit breaker health mismatch")

		// Open circuit
		ctx := t.Context()
		for range config.MaxFailures {
			_ = cb.Call(ctx, func(_ context.Context) error {
				return assert.AnError
			})
		}

		// Should be unhealthy when open
		assert.False(t, cb.IsHealthy(), "circuit breaker health mismatch")

		// Wait for half-open - instant with synctest
		time.Sleep(config.Timeout + 10*time.Millisecond)

		// Successful call should restore health
		err := cb.Call(ctx, func(_ context.Context) error {
			return nil
		})
		require.NoError(t, err)

		assert.True(t, cb.IsHealthy(), "circuit breaker health mismatch")
	})
}

func TestCircuitBreaker_GetStats(t *testing.T) {
	t.Parallel()

	cb := newTestCircuitBreaker(t, DefaultCircuitBreakerTestConfig())

	testErr := errors.New("test error")

	// Make some failures
	for range 2 {
		_ = cb.Call(t.Context(), func(_ context.Context) error {
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

	synctest.Test(t, func(t *testing.T) {
		config := CircuitBreakerConfig{
			MaxFailures:         10,
			Timeout:             100 * time.Millisecond,
			HalfOpenMaxRequests: 1,
		}
		cb := NewPushCircuitBreaker(config, nil, "test-provider")
		require.NotNil(t, cb, "NewPushCircuitBreaker should return non-nil")

		const numCalls = 100
		ctx := t.Context()

		// Run concurrent successful calls
		var wg sync.WaitGroup
		errChan := make(chan error, numCalls)
		for range numCalls {
			wg.Go(func() {
				err := cb.Call(ctx, func(_ context.Context) error {
					time.Sleep(1 * time.Millisecond) // instant with synctest
					return nil
				})
				if err != nil {
					errChan <- err
				}
			})
		}

		wg.Wait()
		close(errChan)

		// Check for errors - collect any that occurred
		concurrentErrors := make([]error, 0, numCalls)
		for err := range errChan {
			concurrentErrors = append(concurrentErrors, err)
		}
		assert.Empty(t, concurrentErrors, "concurrent calls should not fail")

		assert.Equal(t, StateClosed, cb.State(), "circuit breaker state mismatch")
	})
}

func TestCircuitBreaker_ContextCancellation(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		cb := NewPushCircuitBreaker(DefaultCircuitBreakerTestConfig(), nil, "test-provider")
		require.NotNil(t, cb, "NewPushCircuitBreaker should return non-nil")

		ctx, cancel := context.WithCancel(t.Context())
		cancel() // Cancel immediately

		err := cb.Call(ctx, func(ctx context.Context) error {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(100 * time.Millisecond): // instant with synctest
				return nil
			}
		})

		require.ErrorIs(t, err, context.Canceled)

		// Context cancellation should NOT count as provider failure (it's client-side)
		assert.Equal(t, 0, cb.Failures(),
			"context cancellation (client-side) should not count as provider failure")
	})
}
