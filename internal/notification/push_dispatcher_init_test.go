package notification

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"golang.org/x/sync/semaphore"
)

// TestCalculateMaxConcurrentJobs tests concurrent job calculation
func TestCalculateMaxConcurrentJobs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		providerCount int
		expected      int64
	}{
		{
			name:          "no_providers_uses_default",
			providerCount: 0,
			expected:      defaultMaxConcurrentJobs,
		},
		{
			name:          "few_providers_uses_default",
			providerCount: 3,
			expected:      defaultMaxConcurrentJobs, // 3*20=60 < 100
		},
		{
			name:          "many_providers_scales_up",
			providerCount: 10,
			expected:      int64(10 * jobsPerProvider), // 10*20=200 > 100
		},
		{
			name:          "exactly_at_threshold",
			providerCount: 5,
			expected:      defaultMaxConcurrentJobs, // 5*20=100 = 100
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			settings := &conf.Settings{
				Notification: conf.NotificationConfig{
					Push: conf.PushSettings{
						Providers: make([]conf.PushProviderConfig, tt.providerCount),
					},
				},
			}

			result := calculateMaxConcurrentJobs(settings)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestCategorizeError tests error categorization
func TestCategorizeError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		err      error
		expected string
	}{
		{
			name:     "nil_error",
			err:      nil,
			expected: "none",
		},
		{
			name:     "deadline_exceeded",
			err:      context.DeadlineExceeded,
			expected: "timeout",
		},
		{
			name:     "context_canceled",
			err:      context.Canceled,
			expected: "cancelled",
		},
		{
			name:     "network_error",
			err:      errors.New("network connection refused"),
			expected: "network",
		},
		{
			name:     "dial_error",
			err:      errors.New("dial tcp failed"),
			expected: "network",
		},
		{
			name:     "lookup_error",
			err:      errors.New("lookup host failed"),
			expected: "network",
		},
		{
			name:     "connection_error",
			err:      errors.New("connection reset by peer"),
			expected: "network",
		},
		{
			name:     "validation_error",
			err:      errors.New("validation failed"),
			expected: "validation",
		},
		{
			name:     "invalid_error",
			err:      errors.New("invalid parameter"),
			expected: "validation",
		},
		{
			name:     "malformed_error",
			err:      errors.New("malformed request"),
			expected: "validation",
		},
		{
			name:     "permission_denied",
			err:      errors.New("permission denied"),
			expected: "permission",
		},
		{
			name:     "unauthorized",
			err:      errors.New("unauthorized access"),
			expected: "permission",
		},
		{
			name:     "forbidden",
			err:      errors.New("forbidden"),
			expected: "permission",
		},
		{
			name:     "not_found",
			err:      errors.New("resource not found"),
			expected: "not_found",
		},
		{
			name:     "404_error",
			err:      errors.New("HTTP 404"),
			expected: "not_found",
		},
		{
			name:     "generic_error",
			err:      errors.New("some random error"),
			expected: "provider_error",
		},
		{
			name:     "empty_error",
			err:      errors.New(""),
			expected: "provider_error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := categorizeError(tt.err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestContainsAny tests substring matching
func TestContainsAny(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		s        string
		substrs  []string
		expected bool
	}{
		{
			name:     "contains_first",
			s:        "network error occurred",
			substrs:  []string{"network", "connection"},
			expected: true,
		},
		{
			name:     "contains_second",
			s:        "connection refused",
			substrs:  []string{"network", "connection"},
			expected: true,
		},
		{
			name:     "contains_none",
			s:        "some other error",
			substrs:  []string{"network", "connection"},
			expected: false,
		},
		{
			name:     "case_insensitive",
			s:        "NETWORK ERROR",
			substrs:  []string{"network"},
			expected: true,
		},
		{
			name:     "empty_string",
			s:        "",
			substrs:  []string{"network"},
			expected: false,
		},
		{
			name:     "empty_substrs",
			s:        "some error",
			substrs:  []string{},
			expected: false,
		},
		{
			name:     "empty_substr_in_list",
			s:        "some error",
			substrs:  []string{"", "error"},
			expected: true, // matches "error"
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := containsAny(tt.s, tt.substrs...)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestPushDispatcher_shouldRetry tests retry decision logic
func TestPushDispatcher_shouldRetry(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		err        error
		attempts   int
		maxRetries int
		expected   bool
	}{
		{
			name:       "first_attempt_retryable",
			err:        errors.New("temporary error"),
			attempts:   1,
			maxRetries: 3,
			expected:   true,
		},
		{
			name:       "max_attempts_reached",
			err:        errors.New("temporary error"),
			attempts:   4,
			maxRetries: 3,
			expected:   false,
		},
		{
			name:       "non_retryable_error",
			err:        &providerError{Err: errors.New("fatal"), Retryable: false},
			attempts:   1,
			maxRetries: 3,
			expected:   false,
		},
		{
			name:       "retryable_provider_error",
			err:        &providerError{Err: errors.New("temp"), Retryable: true},
			attempts:   1,
			maxRetries: 3,
			expected:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			d := &pushDispatcher{
				maxRetries: tt.maxRetries,
				log:        nil, // Suppress logs in tests
			}

			result := d.shouldRetry(tt.err, tt.attempts, "test-provider")
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestPushDispatcher_acquireSemaphoreSlot tests semaphore acquisition
func TestPushDispatcher_acquireSemaphoreSlot(t *testing.T) {
	t.Parallel()

	t.Run("no_semaphore_always_succeeds", func(t *testing.T) {
		t.Parallel()

		d := &pushDispatcher{
			concurrencySem: nil,
		}

		ep := &enhancedProvider{name: "test"}
		notif := NewNotification(TypeInfo, PriorityLow, "Test", "Message")

		result := d.acquireSemaphoreSlot(t.Context(), ep, notif)
		assert.True(t, result)
	})

	t.Run("semaphore_available", func(t *testing.T) {
		t.Parallel()

		d := &pushDispatcher{
			concurrencySem: semaphore.NewWeighted(10),
		}

		ep := &enhancedProvider{name: "test"}
		notif := NewNotification(TypeInfo, PriorityLow, "Test", "Message")

		result := d.acquireSemaphoreSlot(t.Context(), ep, notif)
		assert.True(t, result)

		// Clean up - release the acquired slot
		d.concurrencySem.Release(1)
	})

	t.Run("semaphore_exhausted", func(t *testing.T) {
		t.Parallel()

		// Create semaphore with 1 slot
		sem := semaphore.NewWeighted(1)

		// Acquire the only slot
		err := sem.Acquire(t.Context(), 1)
		require.NoError(t, err)

		d := &pushDispatcher{
			concurrencySem: sem,
		}

		ep := &enhancedProvider{name: "test"}
		notif := NewNotification(TypeInfo, PriorityLow, "Test", "Message")

		// Should timeout trying to acquire (within 100ms)
		result := d.acquireSemaphoreSlot(t.Context(), ep, notif)
		assert.False(t, result)

		// Clean up
		sem.Release(1)
	})
}

// TestPushDispatcher_recoverFromDispatchPanic tests panic recovery
func TestPushDispatcher_recoverFromDispatchPanic(t *testing.T) {
	t.Parallel()

	t.Run("releases_semaphore", func(t *testing.T) {
		t.Parallel()

		sem := semaphore.NewWeighted(10)
		// Acquire a slot
		err := sem.Acquire(t.Context(), 1)
		require.NoError(t, err)

		d := &pushDispatcher{
			concurrencySem: sem,
		}

		ep := &enhancedProvider{name: "test"}
		notif := NewNotification(TypeInfo, PriorityLow, "Test", "Message")

		// Call recovery function directly
		d.recoverFromDispatchPanic(ep, notif)

		// Verify semaphore was released - we can acquire all 10 now
		err = sem.Acquire(t.Context(), 10)
		require.NoError(t, err)
		sem.Release(10)
	})

	t.Run("recovers_from_panic", func(t *testing.T) {
		t.Parallel()

		d := &pushDispatcher{
			concurrencySem: nil,
		}

		ep := &enhancedProvider{name: "test"}
		notif := NewNotification(TypeInfo, PriorityLow, "Test", "Message")

		// This should not panic even when called from defer after a panic
		// We can't easily test actual panic recovery, but we verify it doesn't panic
		assert.NotPanics(t, func() {
			d.recoverFromDispatchPanic(ep, notif)
		})
	})
}

// TestPushDispatcher_shouldDispatchToProvider tests dispatch filtering
func TestPushDispatcher_shouldDispatchToProvider(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		provider *fakeProvider
		filter   conf.PushFilterConfig
		notif    *Notification
		expected bool
	}{
		{
			name: "enabled_and_supports_type",
			provider: &fakeProvider{
				name:    "test",
				enabled: true,
				types:   map[Type]bool{TypeInfo: true},
			},
			filter:   conf.PushFilterConfig{},
			notif:    &Notification{Type: TypeInfo},
			expected: true,
		},
		{
			name: "disabled_provider",
			provider: &fakeProvider{
				name:    "test",
				enabled: false,
				types:   map[Type]bool{TypeInfo: true},
			},
			filter:   conf.PushFilterConfig{},
			notif:    &Notification{Type: TypeInfo},
			expected: false,
		},
		{
			name: "unsupported_type",
			provider: &fakeProvider{
				name:    "test",
				enabled: true,
				types:   map[Type]bool{TypeError: true},
			},
			filter:   conf.PushFilterConfig{},
			notif:    &Notification{Type: TypeInfo},
			expected: false,
		},
		{
			name: "filter_mismatch",
			provider: &fakeProvider{
				name:    "test",
				enabled: true,
				types:   map[Type]bool{TypeInfo: true},
			},
			filter: conf.PushFilterConfig{
				Types: []string{"error"}, // Filter requires error type
			},
			notif:    &Notification{Type: TypeInfo},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			d := &pushDispatcher{}

			ep := &enhancedProvider{
				prov:   tt.provider,
				filter: tt.filter,
				name:   tt.provider.name,
			}

			result := d.shouldDispatchToProvider(ep, tt.notif)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestPushDispatcher_checkRateLimit tests rate limiting
func TestPushDispatcher_checkRateLimit(t *testing.T) {
	t.Parallel()

	t.Run("no_rate_limiter", func(t *testing.T) {
		t.Parallel()

		d := &pushDispatcher{}
		ep := &enhancedProvider{
			name:        "test",
			rateLimiter: nil,
		}
		notif := NewNotification(TypeInfo, PriorityLow, "Test", "Message")

		result := d.checkRateLimit(ep, notif)
		assert.True(t, result)
	})

	t.Run("rate_limiter_allows", func(t *testing.T) {
		t.Parallel()

		rl := NewPushRateLimiter(PushRateLimiterConfig{
			RequestsPerMinute: 100,
			BurstSize:         10,
		})

		d := &pushDispatcher{}
		ep := &enhancedProvider{
			name:        "test",
			rateLimiter: rl,
		}
		notif := NewNotification(TypeInfo, PriorityLow, "Test", "Message")

		result := d.checkRateLimit(ep, notif)
		assert.True(t, result)
	})

	t.Run("rate_limiter_blocks", func(t *testing.T) {
		t.Parallel()

		// Create rate limiter with very low limits
		rl := NewPushRateLimiter(PushRateLimiterConfig{
			RequestsPerMinute: 1,
			BurstSize:         1,
		})

		d := &pushDispatcher{}
		ep := &enhancedProvider{
			name:        "test",
			rateLimiter: rl,
		}
		notif := NewNotification(TypeInfo, PriorityLow, "Test", "Message")

		// First should succeed
		result1 := d.checkRateLimit(ep, notif)
		assert.True(t, result1)

		// Second should be blocked (rate limited)
		result2 := d.checkRateLimit(ep, notif)
		assert.False(t, result2)
	})
}

// TestStartDispatcherIfNeeded tests conditional dispatcher start
func TestStartDispatcherIfNeeded(t *testing.T) {
	t.Parallel()

	t.Run("disabled_returns_nil", func(t *testing.T) {
		t.Parallel()

		pd := &pushDispatcher{
			enabled: false,
		}

		err := startDispatcherIfNeeded(pd)
		assert.NoError(t, err)
	})

	t.Run("no_providers_returns_nil", func(t *testing.T) {
		t.Parallel()

		pd := &pushDispatcher{
			enabled:   true,
			providers: []enhancedProvider{},
		}

		err := startDispatcherIfNeeded(pd)
		assert.NoError(t, err)
	})
}

// TestEffectiveTypes tests default type assignment
func TestEffectiveTypes(t *testing.T) {
	t.Parallel()

	t.Run("empty_returns_defaults", func(t *testing.T) {
		t.Parallel()

		result := effectiveTypes([]string{})
		assert.Len(t, result, 5)
		assert.Contains(t, result, "error")
		assert.Contains(t, result, "warning")
		assert.Contains(t, result, "info")
		assert.Contains(t, result, "detection")
		assert.Contains(t, result, "system")
	})

	t.Run("nil_returns_defaults", func(t *testing.T) {
		t.Parallel()

		result := effectiveTypes(nil)
		assert.Len(t, result, 5)
	})

	t.Run("custom_types_preserved", func(t *testing.T) {
		t.Parallel()

		input := []string{"error", "detection"}
		result := effectiveTypes(input)

		assert.Equal(t, input, result)
		// Verify it's a copy, not the same slice
		input[0] = "modified"
		assert.NotEqual(t, input[0], result[0])
	})
}

// TestOrDefault tests default value helper
func TestOrDefault(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		value    string
		def      string
		expected string
	}{
		{
			name:     "non_empty_value",
			value:    "test",
			def:      "default",
			expected: "test",
		},
		{
			name:     "empty_value",
			value:    "",
			def:      "default",
			expected: "default",
		},
		{
			name:     "whitespace_only",
			value:    "   ",
			def:      "default",
			expected: "default",
		},
		{
			name:     "value_with_spaces",
			value:    "  test  ",
			def:      "default",
			expected: "  test  ", // preserves spaces if not empty
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := orDefault(tt.value, tt.def)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestCreateHealthCheckerIfEnabled tests health checker creation
func TestCreateHealthCheckerIfEnabled(t *testing.T) {
	t.Parallel()

	t.Run("disabled_returns_nil", func(t *testing.T) {
		t.Parallel()

		settings := &conf.Settings{
			Notification: conf.NotificationConfig{
				Push: conf.PushSettings{
					HealthCheck: conf.HealthCheckConfig{
						Enabled: false,
					},
				},
			},
		}

		result := createHealthCheckerIfEnabled(settings, nil, nil)
		assert.Nil(t, result)
	})

	t.Run("enabled_creates_checker", func(t *testing.T) {
		t.Parallel()

		settings := &conf.Settings{
			Notification: conf.NotificationConfig{
				Push: conf.PushSettings{
					HealthCheck: conf.HealthCheckConfig{
						Enabled:  true,
						Interval: conf.Duration(60 * time.Second),
						Timeout:  conf.Duration(10 * time.Second),
					},
				},
			},
		}

		result := createHealthCheckerIfEnabled(settings, nil, nil)
		require.NotNil(t, result)
		assert.Equal(t, 60*time.Second, result.interval)
		assert.Equal(t, 10*time.Second, result.timeout)
	})
}

// TestRegisterProvidersWithHealthChecker tests provider registration
func TestRegisterProvidersWithHealthChecker(t *testing.T) {
	t.Parallel()

	t.Run("nil_health_checker", func(t *testing.T) {
		t.Parallel()

		pd := &pushDispatcher{
			healthChecker: nil,
			providers: []enhancedProvider{
				{name: "test"},
			},
		}

		// Should not panic
		assert.NotPanics(t, func() {
			registerProvidersWithHealthChecker(pd)
		})
	})

	t.Run("registers_all_providers", func(t *testing.T) {
		t.Parallel()

		hc := NewHealthChecker(DefaultHealthCheckConfig(), nil, nil)

		pd := &pushDispatcher{
			healthChecker: hc,
			providers: []enhancedProvider{
				{
					prov: &fakeProvider{name: "provider1", enabled: true},
					name: "provider1",
				},
				{
					prov: &fakeProvider{name: "provider2", enabled: true},
					name: "provider2",
				},
			},
		}

		registerProvidersWithHealthChecker(pd)

		// Verify providers were registered
		all := hc.GetAllProviderHealth()
		assert.Len(t, all, 2)
	})
}
