package telemetry

import (
	"context"
	"strings"
	"sync"
	"testing"
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
			ctx:      NewTraceIDContext(context.Background(), "trace-123"),
			expected: "trace-123",
		},
		{
			name:     "extracts x-trace-id from typed key",
			ctx:      NewXTraceIDContext(context.Background(), "xtrace-456"),
			expected: "xtrace-456",
		},
		{
			name:     "extracts request-id from typed key",
			ctx:      NewRequestIDContext(context.Background(), "req-789"),
			expected: "req-789",
		},
		{
			name:     "returns empty for missing key",
			ctx:      context.Background(),
			expected: "",
		},
		{
			name:     "prefers trace-id over x-trace-id",
			ctx:      NewTraceIDContext(NewXTraceIDContext(context.Background(), "xtrace"), "trace"),
			expected: "trace",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := extractTraceID(tt.ctx)
			if result != tt.expected {
				t.Errorf("extractTraceID() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestExtractTraceID_NoCollisionWithStringKeys(t *testing.T) {
	t.Parallel()

	// Using a plain string key should NOT extract the value
	// This ensures we don't have key collisions with other packages
	//nolint:staticcheck // SA1029: intentionally using string key to test collision avoidance
	ctx := context.WithValue(context.Background(), "trace-id", "should-not-match")

	result := extractTraceID(ctx)
	if result != "" {
		t.Errorf("extractTraceID() should not match plain string key, got %q", result)
	}
}

func TestSentryDSN_ValidFormat(t *testing.T) {
	t.Parallel()

	// Verify the DSN constant exists and has valid format
	if sentryDSN == "" {
		t.Error("sentryDSN should not be empty")
	}

	// Verify it's a valid Sentry DSN format (https://<key>@<host>/<project>)
	if !strings.HasPrefix(sentryDSN, "https://") {
		t.Errorf("sentryDSN should start with https://, got %s", sentryDSN)
	}

	if !strings.Contains(sentryDSN, "@") {
		t.Error("sentryDSN should contain @ symbol")
	}

	// Note: .sentry.io check assumes cloud Sentry; self-hosted endpoints
	// would not have this domain. Log a warning instead of failing.
	if !strings.Contains(sentryDSN, ".sentry.io") {
		t.Log("Warning: sentryDSN does not contain .sentry.io - may be self-hosted")
	}
}

func TestIsTelemetryEnabled_InTestMode(t *testing.T) {
	// Note: Not parallel because it modifies global testMode state

	// Enable test mode and update cached state
	EnableTestMode()
	defer DisableTestMode()

	if !IsTelemetryEnabled() {
		t.Error("IsTelemetryEnabled() should return true in test mode")
	}
}

func TestFlushWithContext_Success(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	err := flushWithContext(ctx, "test_operation")
	if err != nil {
		t.Errorf("flushWithContext should succeed with valid context, got: %v", err)
	}
}

func TestFlushWithContext_CancelledContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	err := flushWithContext(ctx, "test_operation")
	if err == nil {
		t.Error("flushWithContext should return error for cancelled context")
	}
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
		if coord == nil {
			t.Error("InitializeCoordinatorOnce returned nil")
			continue
		}
		if first == nil {
			first = coord
		} else if coord != first {
			t.Error("InitializeCoordinatorOnce returned different instances")
		}
	}
}
