//nolint:dupl // Table-driven tests have similar structures
package notification

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockHealthProvider implements Provider for health check testing
type mockHealthProvider struct {
	name           string
	enabled        bool
	validateErr    error
	validateCalled int
	mu             sync.Mutex
}

func (m *mockHealthProvider) GetName() string {
	return m.name
}

func (m *mockHealthProvider) ValidateConfig() error {
	m.mu.Lock()
	m.validateCalled++
	m.mu.Unlock()
	return m.validateErr
}

func (m *mockHealthProvider) Send(_ context.Context, _ *Notification) error {
	return nil
}

func (m *mockHealthProvider) SupportsType(_ Type) bool {
	return true
}

func (m *mockHealthProvider) IsEnabled() bool {
	return m.enabled
}

func (m *mockHealthProvider) getValidateCalled() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.validateCalled
}

// TestHealthCheckConfig_Validate tests the configuration validation
func TestHealthCheckConfig_Validate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		config  HealthCheckConfig
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid_config",
			config: HealthCheckConfig{
				Enabled:  true,
				Interval: 60 * time.Second,
				Timeout:  10 * time.Second,
			},
			wantErr: false,
		},
		{
			name: "interval_too_short",
			config: HealthCheckConfig{
				Enabled:  true,
				Interval: 500 * time.Millisecond,
				Timeout:  100 * time.Millisecond,
			},
			wantErr: true,
			errMsg:  "interval must be at least 1 second",
		},
		{
			name: "timeout_too_short",
			config: HealthCheckConfig{
				Enabled:  true,
				Interval: 60 * time.Second,
				Timeout:  500 * time.Millisecond,
			},
			wantErr: true,
			errMsg:  "timeout must be at least 1 second",
		},
		{
			name: "timeout_equals_interval",
			config: HealthCheckConfig{
				Enabled:  true,
				Interval: 10 * time.Second,
				Timeout:  10 * time.Second,
			},
			wantErr: true,
			errMsg:  "timeout (10s) must be less than interval (10s)",
		},
		{
			name: "timeout_greater_than_interval",
			config: HealthCheckConfig{
				Enabled:  true,
				Interval: 10 * time.Second,
				Timeout:  20 * time.Second,
			},
			wantErr: true,
			errMsg:  "timeout (20s) must be less than interval (10s)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := tt.config.Validate()
			if tt.wantErr {
				require.Error(t, err)
				if tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg)
				}
			} else {
				require.NoError(t, err)
			}
		})
	}
}

// TestDefaultHealthCheckConfig verifies default configuration values
func TestDefaultHealthCheckConfig(t *testing.T) {
	t.Parallel()

	config := DefaultHealthCheckConfig()

	assert.True(t, config.Enabled)
	assert.Equal(t, 60*time.Second, config.Interval)
	assert.Equal(t, 10*time.Second, config.Timeout)

	// Ensure defaults pass validation
	err := config.Validate()
	assert.NoError(t, err)
}

// TestNewHealthChecker verifies health checker creation
func TestNewHealthChecker(t *testing.T) {
	t.Parallel()

	config := DefaultHealthCheckConfig()
	log := GetLogger()

	hc := NewHealthChecker(config, log, nil)

	require.NotNil(t, hc)
	assert.Equal(t, config.Interval, hc.interval)
	assert.Equal(t, config.Timeout, hc.timeout)
	assert.NotNil(t, hc.providers)
	assert.Empty(t, hc.providers)
}

// TestHealthChecker_RegisterProvider tests provider registration
func TestHealthChecker_RegisterProvider(t *testing.T) {
	t.Parallel()

	hc := NewHealthChecker(DefaultHealthCheckConfig(), nil, nil)
	provider := &mockHealthProvider{name: "test-provider", enabled: true}

	hc.RegisterProvider(provider, nil)

	health, exists := hc.GetProviderHealth("test-provider")
	require.True(t, exists)
	assert.Equal(t, "test-provider", health.ProviderName)
	assert.True(t, health.Healthy)
}

// TestHealthChecker_RegisterMultipleProviders tests multiple provider registration
func TestHealthChecker_RegisterMultipleProviders(t *testing.T) {
	t.Parallel()

	hc := NewHealthChecker(DefaultHealthCheckConfig(), nil, nil)

	providers := []*mockHealthProvider{
		{name: "provider-1", enabled: true},
		{name: "provider-2", enabled: true},
		{name: "provider-3", enabled: false},
	}

	for _, p := range providers {
		hc.RegisterProvider(p, nil)
	}

	allHealth := hc.GetAllProviderHealth()
	assert.Len(t, allHealth, 3)
}

// TestHealthChecker_GetProviderHealth_NotFound tests getting health for non-existent provider
func TestHealthChecker_GetProviderHealth_NotFound(t *testing.T) {
	t.Parallel()

	hc := NewHealthChecker(DefaultHealthCheckConfig(), nil, nil)

	health, exists := hc.GetProviderHealth("non-existent")
	assert.False(t, exists)
	assert.Equal(t, ProviderHealth{}, health)
}

// TestHealthChecker_IsHealthy tests overall health check
func TestHealthChecker_IsHealthy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		providers []struct {
			name    string
			healthy bool
			cbState CircuitState
		}
		expected bool
	}{
		{
			name:     "no_providers",
			expected: true,
		},
		{
			name: "all_healthy",
			providers: []struct {
				name    string
				healthy bool
				cbState CircuitState
			}{
				{name: "p1", healthy: true, cbState: StateClosed},
				{name: "p2", healthy: true, cbState: StateClosed},
			},
			expected: true,
		},
		{
			name: "one_unhealthy",
			providers: []struct {
				name    string
				healthy bool
				cbState CircuitState
			}{
				{name: "p1", healthy: true, cbState: StateClosed},
				{name: "p2", healthy: false, cbState: StateClosed},
			},
			expected: false,
		},
		{
			name: "unhealthy_but_circuit_open",
			providers: []struct {
				name    string
				healthy bool
				cbState CircuitState
			}{
				{name: "p1", healthy: true, cbState: StateClosed},
				{name: "p2", healthy: false, cbState: StateOpen},
			},
			expected: true, // Circuit-broken providers are acceptable
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			hc := NewHealthChecker(DefaultHealthCheckConfig(), nil, nil)

			for _, p := range tt.providers {
				provider := &mockHealthProvider{name: p.name, enabled: true}
				hc.RegisterProvider(provider, nil)

				// Manually set health status for testing
				hc.mu.Lock()
				entry := hc.providers[p.name]
				entry.mu.Lock()
				entry.health.Healthy = p.healthy
				entry.health.CircuitBreakerState = p.cbState
				entry.mu.Unlock()
				hc.mu.Unlock()
			}

			result := hc.IsHealthy()
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestHealthChecker_GetHealthSummary tests health summary generation
func TestHealthChecker_GetHealthSummary(t *testing.T) {
	t.Parallel()

	hc := NewHealthChecker(DefaultHealthCheckConfig(), nil, nil)

	// Register 3 providers with different states
	providers := []struct {
		name    string
		healthy bool
		cbState CircuitState
	}{
		{name: "healthy-1", healthy: true, cbState: StateClosed},
		{name: "healthy-2", healthy: true, cbState: StateClosed},
		{name: "unhealthy", healthy: false, cbState: StateOpen},
	}

	for _, p := range providers {
		provider := &mockHealthProvider{name: p.name, enabled: true}
		hc.RegisterProvider(provider, nil)

		hc.mu.Lock()
		entry := hc.providers[p.name]
		entry.mu.Lock()
		entry.health.Healthy = p.healthy
		entry.health.CircuitBreakerState = p.cbState
		entry.mu.Unlock()
		hc.mu.Unlock()
	}

	summary := hc.GetHealthSummary()

	assert.Equal(t, 3, summary["total_providers"])
	assert.Equal(t, 2, summary["healthy_providers"])
	assert.Equal(t, 1, summary["open_circuits"])
	assert.True(t, summary["overall_healthy"].(bool))
	assert.NotNil(t, summary["last_check"])
}

// TestHealthChecker_StartStop tests starting and stopping the health checker
func TestHealthChecker_StartStop(t *testing.T) {
	t.Parallel()

	config := HealthCheckConfig{
		Enabled:  true,
		Interval: 100 * time.Millisecond, // Short interval for testing
		Timeout:  50 * time.Millisecond,
	}

	hc := NewHealthChecker(config, nil, nil)
	provider := &mockHealthProvider{name: "test", enabled: true}
	hc.RegisterProvider(provider, nil)

	ctx := t.Context()

	// Start health checker
	err := hc.Start(ctx)
	require.NoError(t, err)

	// Wait for at least one check - use longer timeout for CI reliability
	time.Sleep(250 * time.Millisecond)

	// Verify provider was checked
	assert.Positive(t, provider.getValidateCalled())

	// Stop health checker
	hc.Stop()

	// Verify no more checks happen
	callsBefore := provider.getValidateCalled()
	time.Sleep(250 * time.Millisecond)
	callsAfter := provider.getValidateCalled()
	assert.Equal(t, callsBefore, callsAfter)
}

// TestHealthChecker_StartAlreadyStarted tests double-start error
func TestHealthChecker_StartAlreadyStarted(t *testing.T) {
	t.Parallel()

	config := HealthCheckConfig{
		Enabled:  true,
		Interval: 1 * time.Second,
		Timeout:  500 * time.Millisecond,
	}

	hc := NewHealthChecker(config, nil, nil)
	ctx := t.Context()

	err := hc.Start(ctx)
	require.NoError(t, err)
	defer hc.Stop()

	// Try to start again
	err = hc.Start(ctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already started")
}

// TestHealthChecker_isCircuitBreakerGating tests circuit breaker gating detection
func TestHealthChecker_isCircuitBreakerGating(t *testing.T) {
	t.Parallel()

	hc := NewHealthChecker(DefaultHealthCheckConfig(), nil, nil)

	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "nil_error",
			err:      nil,
			expected: false,
		},
		{
			name:     "circuit_breaker_open",
			err:      ErrCircuitBreakerOpen,
			expected: true,
		},
		{
			name:     "too_many_requests",
			err:      ErrTooManyRequests,
			expected: true,
		},
		{
			name:     "other_error",
			err:      errors.New("random error"),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := hc.isCircuitBreakerGating(tt.err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestHealthChecker_recordHealthSuccess tests successful health recording
func TestHealthChecker_recordHealthSuccess(t *testing.T) {
	t.Parallel()

	hc := NewHealthChecker(DefaultHealthCheckConfig(), nil, nil)

	entry := &healthCheckEntry{
		health: ProviderHealth{
			ProviderName:        "test",
			Healthy:             false,
			ConsecutiveFailures: 5,
			ErrorMessage:        "previous error",
			TotalSuccesses:      10,
		},
	}

	hc.recordHealthSuccess(entry, "test")

	assert.True(t, entry.health.Healthy)
	assert.Zero(t, entry.health.ConsecutiveFailures)
	assert.Empty(t, entry.health.ErrorMessage)
	assert.Equal(t, 11, entry.health.TotalSuccesses)
	assert.False(t, entry.health.LastSuccessTime.IsZero())
}

// TestHealthChecker_recordHealthFailure tests failure health recording
func TestHealthChecker_recordHealthFailure(t *testing.T) {
	t.Parallel()

	hc := NewHealthChecker(DefaultHealthCheckConfig(), nil, nil)

	entry := &healthCheckEntry{
		health: ProviderHealth{
			ProviderName:        "test",
			Healthy:             true,
			ConsecutiveFailures: 2,
			TotalFailures:       5,
		},
	}

	testErr := errors.New("test error")
	hc.recordHealthFailure(entry, "test", testErr)

	assert.False(t, entry.health.Healthy)
	assert.Equal(t, 3, entry.health.ConsecutiveFailures)
	assert.Equal(t, 6, entry.health.TotalFailures)
	assert.Equal(t, "test error", entry.health.ErrorMessage)
	assert.False(t, entry.health.LastFailureTime.IsZero())
}

// TestHealthChecker_updateCircuitBreakerState tests circuit breaker state sync
func TestHealthChecker_updateCircuitBreakerState(t *testing.T) {
	t.Parallel()

	hc := NewHealthChecker(DefaultHealthCheckConfig(), nil, nil)

	t.Run("with_circuit_breaker", func(t *testing.T) {
		cb := NewPushCircuitBreaker(CircuitBreakerConfig{
			MaxFailures:         5,
			Timeout:             10 * time.Second,
			HalfOpenMaxRequests: 1,
		}, nil, "test-provider")

		entry := &healthCheckEntry{
			health: ProviderHealth{
				CircuitBreakerState: 0, // default zero value
			},
		}

		hc.updateCircuitBreakerState(entry, cb)

		assert.Equal(t, StateClosed, entry.health.CircuitBreakerState)
	})

	t.Run("without_circuit_breaker", func(t *testing.T) {
		entry := &healthCheckEntry{
			health: ProviderHealth{
				CircuitBreakerState: StateOpen, // some previous state
			},
		}

		hc.updateCircuitBreakerState(entry, nil)

		// State should remain unchanged when no circuit breaker
		assert.Equal(t, StateOpen, entry.health.CircuitBreakerState)
	})
}

// TestHealthChecker_executeHealthCheck tests health check execution
func TestHealthChecker_executeHealthCheck(t *testing.T) {
	config := HealthCheckConfig{
		Enabled:  true,
		Interval: 60 * time.Second,
		Timeout:  5 * time.Second,
	}

	ctx := t.Context()
	hc := NewHealthChecker(config, nil, nil)
	hc.baseCtx = ctx // Set base context for tests

	t.Run("success_without_circuit_breaker", func(t *testing.T) {
		provider := &mockHealthProvider{name: "test", enabled: true, validateErr: nil}

		err := hc.executeHealthCheck(provider, nil)

		require.NoError(t, err)
		assert.Equal(t, 1, provider.getValidateCalled())
	})

	t.Run("failure_without_circuit_breaker", func(t *testing.T) {
		expectedErr := errors.New("validation failed")
		provider := &mockHealthProvider{name: "test", enabled: true, validateErr: expectedErr}

		err := hc.executeHealthCheck(provider, nil)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "validation failed")
	})

	t.Run("success_with_circuit_breaker", func(t *testing.T) {
		provider := &mockHealthProvider{name: "test", enabled: true, validateErr: nil}
		cb := NewPushCircuitBreaker(CircuitBreakerConfig{
			MaxFailures:         5,
			Timeout:             10 * time.Second,
			HalfOpenMaxRequests: 1,
		}, nil, "test-provider")

		err := hc.executeHealthCheck(provider, cb)

		assert.NoError(t, err)
	})
}

// TestHealthChecker_ConcurrentAccess tests thread-safety of health checker
func TestHealthChecker_ConcurrentAccess(t *testing.T) {
	t.Parallel()

	config := HealthCheckConfig{
		Enabled:  true,
		Interval: 50 * time.Millisecond,
		Timeout:  25 * time.Millisecond,
	}

	hc := NewHealthChecker(config, nil, nil)

	// Register multiple providers
	for i := range 5 {
		provider := &mockHealthProvider{name: fmt.Sprintf("provider-%d", i), enabled: true}
		hc.RegisterProvider(provider, nil)
	}

	ctx := t.Context()
	err := hc.Start(ctx)
	require.NoError(t, err)
	defer hc.Stop()

	// Concurrent reads during health checks
	var wg sync.WaitGroup
	for range 10 {
		wg.Add(3)

		// Concurrent GetAllProviderHealth
		go func() {
			defer wg.Done()
			for range 20 {
				_ = hc.GetAllProviderHealth()
				time.Sleep(5 * time.Millisecond)
			}
		}()

		// Concurrent GetProviderHealth
		go func() {
			defer wg.Done()
			for range 20 {
				_, _ = hc.GetProviderHealth("provider-0")
				time.Sleep(5 * time.Millisecond)
			}
		}()

		// Concurrent IsHealthy
		go func() {
			defer wg.Done()
			for range 20 {
				_ = hc.IsHealthy()
				time.Sleep(5 * time.Millisecond)
			}
		}()
	}

	wg.Wait()
}

// TestHealthChecker_updateHealthStatus tests health status update logic
func TestHealthChecker_updateHealthStatus(t *testing.T) {
	t.Parallel()

	hc := NewHealthChecker(DefaultHealthCheckConfig(), nil, nil)

	tests := []struct {
		name           string
		err            error
		initialHealthy bool
		expectedChange bool // whether health status should change
	}{
		{
			name:           "success_updates_health",
			err:            nil,
			initialHealthy: false,
			expectedChange: true,
		},
		{
			name:           "failure_updates_health",
			err:            errors.New("test error"),
			initialHealthy: true,
			expectedChange: true,
		},
		{
			name:           "circuit_open_no_change",
			err:            ErrCircuitBreakerOpen,
			initialHealthy: false,
			expectedChange: false,
		},
		{
			name:           "too_many_requests_no_change",
			err:            ErrTooManyRequests,
			initialHealthy: true,
			expectedChange: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			entry := &healthCheckEntry{
				health: ProviderHealth{
					ProviderName: "test",
					Healthy:      tt.initialHealthy,
				},
			}

			hc.updateHealthStatus(entry, "test", tt.err)

			if tt.expectedChange {
				if tt.err == nil {
					assert.True(t, entry.health.Healthy)
				} else {
					assert.False(t, entry.health.Healthy)
				}
			} else {
				// Status should remain unchanged
				assert.Equal(t, tt.initialHealthy, entry.health.Healthy)
			}
		})
	}
}

// TestHealthChecker_checkProvider_Integration tests the full checkProvider flow
func TestHealthChecker_checkProvider_Integration(t *testing.T) {
	config := HealthCheckConfig{
		Enabled:  true,
		Interval: 60 * time.Second,
		Timeout:  5 * time.Second,
	}

	ctx := t.Context()
	hc := NewHealthChecker(config, nil, nil)
	hc.baseCtx = ctx

	t.Run("successful_check", func(t *testing.T) {
		provider := &mockHealthProvider{name: "success-test", enabled: true, validateErr: nil}
		hc.RegisterProvider(provider, nil)

		hc.mu.RLock()
		entry := hc.providers["success-test"]
		hc.mu.RUnlock()

		hc.checkProvider(entry)

		health, exists := hc.GetProviderHealth("success-test")
		require.True(t, exists)
		assert.True(t, health.Healthy)
		assert.Equal(t, 1, health.TotalAttempts)
		assert.Equal(t, 1, health.TotalSuccesses)
		assert.Equal(t, 0, health.TotalFailures)
	})

	t.Run("failed_check", func(t *testing.T) {
		provider := &mockHealthProvider{
			name:        "fail-test",
			enabled:     true,
			validateErr: errors.New("validation failed"),
		}
		hc.RegisterProvider(provider, nil)

		hc.mu.RLock()
		entry := hc.providers["fail-test"]
		hc.mu.RUnlock()

		hc.checkProvider(entry)

		health, exists := hc.GetProviderHealth("fail-test")
		require.True(t, exists)
		assert.False(t, health.Healthy)
		assert.Equal(t, 1, health.TotalAttempts)
		assert.Equal(t, 0, health.TotalSuccesses)
		assert.Equal(t, 1, health.TotalFailures)
		assert.Equal(t, 1, health.ConsecutiveFailures)
		assert.Equal(t, "validation failed", health.ErrorMessage)
	})
}
