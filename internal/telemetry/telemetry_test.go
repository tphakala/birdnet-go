package telemetry

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/privacy"
)

//nolint:gocognit // test requires multiple scenarios for comprehensive coverage
func TestMockTransport(t *testing.T) {
	t.Parallel()
	t.Run("SendEvent stores events", func(t *testing.T) {
		t.Parallel()
		transport := NewMockTransport()

		event := &sentry.Event{
			Message: "test event",
			Level:   sentry.LevelInfo,
			Tags: map[string]string{
				"component": "test",
			},
		}

		transport.SendEvent(event)

		assert.Equal(t, 1, transport.GetEventCount(), "Expected 1 event")

		captured := transport.GetLastEvent()
		require.NotNil(t, captured, "Expected event to be captured")
		assert.Equal(t, "test event", captured.Message, "Expected message 'test event'")
	})

	t.Run("Clear removes all events", func(t *testing.T) {
		transport := NewMockTransport()

		// Send multiple events
		for i := range 5 {
			transport.SendEvent(&sentry.Event{
				Message: fmt.Sprintf("event %d", i),
			})
		}

		assert.Equal(t, 5, transport.GetEventCount(), "Expected 5 events")

		transport.Clear()

		assert.Zero(t, transport.GetEventCount(), "Expected 0 events after clear")
	})

	t.Run("SetDisabled prevents event capture", func(t *testing.T) {
		t.Parallel()
		transport := NewMockTransport()
		transport.SetDisabled(true)

		transport.SendEvent(&sentry.Event{Message: "should not be captured"})

		assert.Zero(t, transport.GetEventCount(), "Expected 0 events when disabled")
	})

	t.Run("FindEventByMessage locates events", func(t *testing.T) {
		t.Parallel()
		transport := NewMockTransport()

		events := []string{"first", "second", "third"}
		for _, msg := range events {
			transport.SendEvent(&sentry.Event{Message: msg})
		}

		found := transport.FindEventByMessage("second")
		require.NotNil(t, found, "Expected to find event with message 'second'")
		assert.Equal(t, "second", found.Message, "Found wrong event")

		notFound := transport.FindEventByMessage("fourth")
		assert.Nil(t, notFound, "Should not find non-existent event")
	})

	t.Run("WaitForEventCount with timeout", func(t *testing.T) {
		t.Parallel()
		transport := NewMockTransport()

		// Test immediate success
		transport.SendEvent(&sentry.Event{Message: "event1"})
		transport.SendEvent(&sentry.Event{Message: "event2"})

		assert.True(t, transport.WaitForEventCount(2, 100*time.Millisecond), "Expected WaitForEventCount to succeed immediately")

		// Test timeout
		assert.False(t, transport.WaitForEventCount(5, 50*time.Millisecond), "Expected WaitForEventCount to timeout")
	})

	t.Run("FlushWithContext respects cancellation", func(t *testing.T) {
		t.Parallel()
		transport := NewMockTransport()
		transport.SetDelay(100 * time.Millisecond)

		// Test with cancelled context
		ctx, cancel := context.WithCancel(t.Context())
		cancel() // Cancel immediately

		assert.False(t, transport.FlushWithContext(ctx), "Expected flush to fail with cancelled context")

		// Test with timeout
		ctx, cancel = context.WithTimeout(t.Context(), 200*time.Millisecond)
		defer cancel()

		assert.True(t, transport.FlushWithContext(ctx), "Expected flush to succeed within timeout")
	})
}

func TestURLAnonymization(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		input       string
		contains    string
		notContains string
	}{
		{
			name:        "http URL",
			input:       "Failed to fetch https://api.example.com/v1/data",
			notContains: "example.com",
			contains:    "url-",
		},
		{
			name:        "rtsp URL",
			input:       "RTSP stream error: rtsp://192.168.1.100:554/stream1",
			notContains: "192.168.1.100",
			contains:    "url-",
		},
		{
			name:        "multiple URLs",
			input:       "Connection failed: https://api1.example.com and https://api2.example.com",
			notContains: "example.com",
			contains:    "url-",
		},
		{
			name:        "preserves non-URL content",
			input:       "Error occurred while processing data",
			contains:    "Error occurred while processing data",
			notContains: "url-",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			scrubbed := privacy.ScrubMessage(tt.input)

			if tt.notContains != "" {
				assert.NotContains(t, scrubbed, tt.notContains, "Scrubbed message should not contain sensitive data")
			}

			if tt.contains != "" {
				assert.Contains(t, scrubbed, tt.contains, "Scrubbed message should contain expected pattern")
			}
		})
	}
}

func TestEventSummaries(t *testing.T) {
	t.Parallel()
	transport := NewMockTransport()

	// Create events with different properties
	events := []struct {
		message string
		level   sentry.Level
		tags    map[string]string
		extra   map[string]any
	}{
		{
			message: "Info event",
			level:   sentry.LevelInfo,
			tags:    map[string]string{"component": "test", "version": "1.0"},
			extra:   map[string]any{"count": 42},
		},
		{
			message: "Error event",
			level:   sentry.LevelError,
			tags:    map[string]string{"component": "api"},
			extra:   map[string]any{"endpoint": "/test"},
		},
	}

	for _, e := range events {
		event := &sentry.Event{
			Message:   e.message,
			Level:     e.level,
			Tags:      e.tags,
			Extra:     e.extra,
			Timestamp: time.Now(),
		}
		transport.SendEvent(event)
	}

	summaries := transport.GetEventSummaries()
	require.Len(t, summaries, len(events), "Expected summaries for all events")

	// Verify first summary
	s := summaries[0]
	assert.Equal(t, "Info event", s.Message, "Expected message 'Info event'")
	assert.Equal(t, "info", s.Level, "Expected level 'info'")
	assert.Equal(t, "test", s.Tags["component"], "Expected tag component='test'")

	count := s.Extra["count"]
	switch v := count.(type) {
	case float64:
		assert.InDelta(t, float64(42), v, 0.001, "Expected extra count=42")
	case int:
		assert.Equal(t, 42, v, "Expected extra count=42")
	default:
		assert.Fail(t, "unexpected type for count",
			"Expected extra count to be numeric, got %T: %v", count, count)
	}
}

func TestConcurrentAccess(t *testing.T) {
	t.Parallel()
	transport := NewMockTransport()

	// Run concurrent operations
	var wg sync.WaitGroup
	numGoroutines := 10
	eventsPerGoroutine := 100

	// Writers
	for i := range numGoroutines {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := range eventsPerGoroutine {
				transport.SendEvent(&sentry.Event{
					Message: fmt.Sprintf("event-%d-%d", id, j),
				})
			}
		}(i)
	}

	// Readers
	for range numGoroutines {
		wg.Go(func() {
			for range eventsPerGoroutine {
				_ = transport.GetEventCount()
				_ = transport.GetEvents()
				time.Sleep(time.Microsecond) // Small delay to increase contention
			}
		})
	}

	wg.Wait()

	// Verify all events were captured
	expectedCount := numGoroutines * eventsPerGoroutine
	assert.Equal(t, expectedCount, transport.GetEventCount(), "Expected all events to be captured")
}
