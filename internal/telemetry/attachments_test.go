package telemetry

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtractTraceID_TypedContextKeys(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		ctx      context.Context
		expected string
	}{
		{
			name:     "extracts trace-id from typed key",
			ctx:      NewTraceIDContext(t.Context(), "trace-123"),
			expected: "trace-123",
		},
		{
			name:     "extracts x-trace-id from typed key",
			ctx:      NewXTraceIDContext(t.Context(), "xtrace-456"),
			expected: "xtrace-456",
		},
		{
			name:     "extracts request-id from typed key",
			ctx:      NewRequestIDContext(t.Context(), "req-789"),
			expected: "req-789",
		},
		{
			name:     "returns empty for missing key",
			ctx:      t.Context(),
			expected: "",
		},
		{
			name:     "prefers trace-id over x-trace-id",
			ctx:      NewTraceIDContext(NewXTraceIDContext(t.Context(), "xtrace"), "trace"),
			expected: "trace",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := extractTraceID(tt.ctx)
			assert.Equal(t, tt.expected, result, "extractTraceID() should return expected value")
		})
	}
}

func TestExtractTraceID_NoCollisionWithStringKeys(t *testing.T) {
	t.Parallel()

	// Using a plain string key should NOT extract the value
	// This ensures we don't have key collisions with other packages
	//nolint:staticcheck // SA1029: intentionally using string key to test collision avoidance
	ctx := context.WithValue(t.Context(), "trace-id", "should-not-match")

	result := extractTraceID(ctx)
	assert.Empty(t, result, "extractTraceID() should not match plain string key")
}

func TestSentryDSN_ValidFormat(t *testing.T) {
	// Not parallel: reads the package-level sentryDSN var, which
	// TestResolveSentryDSN_Precedence mutates. Sequential tests run before the
	// parallel phase, so this cannot overlap with parallel readers either.

	// The DSN is injected at build time via ldflags, or at runtime via
	// BIRDNET_GO_SENTRY_DSN, so plain `go test` builds resolve to an empty DSN.
	// Only assert the format when a DSN is actually configured.
	dsn := resolveSentryDSN()
	if dsn == "" {
		t.Skip("no Sentry DSN configured for this build (expected for from-source/test builds)")
	}

	// Verify it's a valid Sentry DSN format (https://<key>@<host>/<project>)
	assert.True(t, strings.HasPrefix(dsn, "https://"), "DSN should start with https://, got %s", dsn)
	assert.Contains(t, dsn, "@", "DSN should contain @ symbol")

	// Note: .sentry.io check assumes cloud Sentry; self-hosted endpoints
	// would not have this domain. Log a warning instead of failing.
	if !strings.Contains(dsn, ".sentry.io") {
		t.Log("Warning: DSN does not contain .sentry.io - may be self-hosted")
	}
}

// TestResolveSentryDSN_Precedence verifies the resolver precedence:
// BIRDNET_GO_SENTRY_DSN env var > ldflags-baked sentryDSN > empty.
func TestResolveSentryDSN_Precedence(t *testing.T) {
	// Not parallel: mutates the package-level sentryDSN var and the process env.
	original := sentryDSN
	t.Cleanup(func() { sentryDSN = original })

	const (
		baked    = "https://baked@example.ingest.sentry.io/1"
		override = "https://override@example.ingest.sentry.io/2"
	)

	t.Run("env overrides baked-in value", func(t *testing.T) {
		sentryDSN = baked
		t.Setenv(sentryDSNEnvVar, override)
		assert.Equal(t, override, resolveSentryDSN())
	})

	t.Run("falls back to baked-in value when env unset", func(t *testing.T) {
		sentryDSN = baked
		t.Setenv(sentryDSNEnvVar, "")
		assert.Equal(t, baked, resolveSentryDSN())
	})

	t.Run("whitespace-only env value is ignored", func(t *testing.T) {
		sentryDSN = baked
		t.Setenv(sentryDSNEnvVar, "   ")
		assert.Equal(t, baked, resolveSentryDSN())
	})

	t.Run("empty when neither is configured", func(t *testing.T) {
		sentryDSN = ""
		t.Setenv(sentryDSNEnvVar, "")
		assert.Empty(t, resolveSentryDSN())
	})

	t.Run("env value is trimmed", func(t *testing.T) {
		sentryDSN = ""
		t.Setenv(sentryDSNEnvVar, "  "+override+"  ")
		assert.Equal(t, override, resolveSentryDSN())
	})

	t.Run("baked value is trimmed", func(t *testing.T) {
		sentryDSN = "  " + baked + "  "
		t.Setenv(sentryDSNEnvVar, "")
		assert.Equal(t, baked, resolveSentryDSN())
	})
}

func TestIsTelemetryEnabled_InTestMode(t *testing.T) {
	// Note: Not parallel because it modifies global testMode state

	// Enable test mode and update cached state
	EnableTestMode()
	defer DisableTestMode()

	assert.True(t, IsTelemetryEnabled(), "IsTelemetryEnabled() should return true in test mode")
}

func TestFlushWithContext_Success(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	err := flushWithContext(ctx, "test_operation")
	assert.NoError(t, err, "flushWithContext should succeed with valid context")
}

func TestFlushWithContext_CancelledContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	cancel() // Cancel immediately

	err := flushWithContext(ctx, "test_operation")
	assert.Error(t, err, "flushWithContext should return error for cancelled context")
}

func TestGetGlobalInitCoordinator_ThreadSafe(t *testing.T) {
	t.Parallel()

	// Test concurrent access to GetGlobalInitCoordinator
	// This should not cause data races when run with -race flag
	const numGoroutines = 10
	var wg sync.WaitGroup

	for range numGoroutines {
		wg.Go(func() {
			// Concurrent reads should be safe
			_ = GetGlobalInitCoordinator()
		})
	}

	wg.Wait()
}

func TestInitCoordinator_OnceInitialization(t *testing.T) {
	// Note: Not parallel - modifies global state
	// This test verifies that InitializeCoordinatorOnce returns the same instance
	// even when called concurrently

	const numGoroutines = 10
	coordinators := make(chan *InitCoordinator, numGoroutines)
	var wg sync.WaitGroup

	// Launch concurrent calls to get/create coordinator
	for range numGoroutines {
		wg.Go(func() {
			coord := InitializeCoordinatorOnce()
			coordinators <- coord
		})
	}

	// Wait for all goroutines to complete before reading from channel
	wg.Wait()
	close(coordinators)

	// Collect all results
	var first *InitCoordinator
	for coord := range coordinators {
		require.NotNil(t, coord, "InitializeCoordinatorOnce returned nil")
		if first == nil {
			first = coord
		} else {
			assert.Same(t, first, coord, "InitializeCoordinatorOnce should return the same instance")
		}
	}
}
