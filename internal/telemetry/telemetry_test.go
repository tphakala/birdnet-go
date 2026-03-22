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
	"github.com/tphakala/birdnet-go/internal/conf"
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

func TestScrubStackTraceFrames(t *testing.T) {
	t.Parallel()
	event := sentry.NewEvent()
	event.Level = sentry.LevelFatal
	event.Exception = []sentry.Exception{{
		Type:  "nil pointer",
		Value: "runtime error",
		Stacktrace: &sentry.Stacktrace{
			Frames: []sentry.Frame{
				{
					AbsPath:  "/home/user/go/src/birdnet-go/internal/telemetry/sentry.go",
					Filename: "internal/telemetry/sentry.go",
					Function: "CaptureError",
					Lineno:   42,
				},
				{
					AbsPath:  "/home/user/projects/birdnet-go/main.go",
					Filename: "main.go",
					Function: "main",
					Lineno:   10,
				},
			},
		},
	}}

	scrubStackTraceFrames(event)

	assert.NotContains(t, event.Exception[0].Stacktrace.Frames[0].AbsPath, "/home/user")
	assert.Equal(t, "main.go", event.Exception[0].Stacktrace.Frames[1].Filename)
	assert.NotContains(t, event.Exception[0].Stacktrace.Frames[0].Filename, "internal")
}

func TestNonFatalNonErrorEventsHaveNoStackTrace(t *testing.T) {
	t.Parallel()
	event := sentry.NewEvent()
	event.Level = sentry.LevelWarning
	event.Exception = []sentry.Exception{{
		Stacktrace: &sentry.Stacktrace{
			Frames: []sentry.Frame{{AbsPath: "/some/path", Function: "foo"}},
		},
	}}

	applyPrivacyFilters(event)

	assert.Nil(t, event.Exception[0].Stacktrace)
}

func TestCollectResourceSnapshot(t *testing.T) {
	t.Parallel()
	snap := CollectResourceSnapshot()
	assert.Positive(t, snap.GoroutineCount)
	assert.Positive(t, snap.HeapAllocMB)
	assert.Positive(t, snap.HeapSysMB)
}

func TestPrivacyExtraFieldWhitelist(t *testing.T) {
	extra := map[string]any{
		"error_type":   "validation",
		"component":    "datastore",
		"stacktrace":   "...",
		"operation":    "save_note",
		"category":     "database",
		"error_origin": "code",
		"secret_field": "should_remove",
		"user_data":    "pii",
	}
	removePrivacyExtraFields(extra)
	assert.Contains(t, extra, "operation")
	assert.Contains(t, extra, "category")
	assert.Contains(t, extra, "error_origin")
	assert.NotContains(t, extra, "secret_field")
	assert.NotContains(t, extra, "user_data")
}

func TestRemovePrivacyExtraFields_AllowsDiagnosticFields(t *testing.T) {
	t.Parallel()

	extra := map[string]any{
		"error_type":           "test",
		"component":            "test",
		"expected_format":      "species_confidence_timestamp",
		"filename":             "bubo_bubo_80p.wav",
		"confidence_string":    "80p",
		"parsed_species":       "bubo_bubo",
		"parsed_timestamp_str": "20210102T150405Z",
		"timestamp_string":     "20210102T150405Z",
		"parsed_confidence":    "80p",
		"exit_code":            137,
		"process_state":        "signal: killed",
		"config_key":           "retention.max_usage",
		"file_exists":          true,
		"file_size_bytes":      int64(4096),
		"input_file_bytes":     int64(0),
		"total_duration_ms":    int64(5000),
		"max_attempts":         10,
		"secret_data":          "should_drop",
	}

	removed := removePrivacyExtraFields(extra)

	assert.Equal(t, 1, removed, "only secret_data should be removed")
	assert.Contains(t, extra, "expected_format")
	assert.Contains(t, extra, "filename")
	assert.Contains(t, extra, "confidence_string")
	assert.Contains(t, extra, "parsed_species")
	assert.Contains(t, extra, "parsed_timestamp_str")
	assert.Contains(t, extra, "timestamp_string")
	assert.Contains(t, extra, "parsed_confidence")
	assert.Contains(t, extra, "exit_code")
	assert.Contains(t, extra, "process_state")
	assert.Contains(t, extra, "config_key")
	assert.Contains(t, extra, "file_exists")
	assert.Contains(t, extra, "file_size_bytes")
	assert.Contains(t, extra, "input_file_bytes")
	assert.Contains(t, extra, "total_duration_ms")
	assert.Contains(t, extra, "max_attempts")
	assert.NotContains(t, extra, "secret_data")
}

func TestApplyStacktracePrivacyFilters_ErrorLevel(t *testing.T) {
	t.Parallel()

	event := &sentry.Event{
		Level: sentry.LevelError,
		Exception: []sentry.Exception{{
			Type:  "TestError",
			Value: "test error",
			Stacktrace: &sentry.Stacktrace{
				Frames: []sentry.Frame{
					{
						Function: "myFunc",
						Module:   "github.com/tphakala/birdnet-go/internal/myaudio",
						Filename: "internal/myaudio/ffmpeg_stream.go",
						AbsPath:  "/home/user/birdnet-go/internal/myaudio/ffmpeg_stream.go",
						Lineno:   42,
					},
				},
			},
		}},
	}

	applyStacktracePrivacyFilters(event)

	assert.NotNil(t, event.Exception[0].Stacktrace, "error-level events should retain scrubbed stacktraces")
	assert.Len(t, event.Exception[0].Stacktrace.Frames, 1)
	assert.NotContains(t, event.Exception[0].Stacktrace.Frames[0].AbsPath, "/home/user/")
}

func TestApplyStacktracePrivacyFilters_WarningLevel(t *testing.T) {
	t.Parallel()

	event := &sentry.Event{
		Level: sentry.LevelWarning,
		Exception: []sentry.Exception{{
			Stacktrace: &sentry.Stacktrace{
				Frames: []sentry.Frame{{Function: "f", AbsPath: "/home/user/code.go"}},
			},
		}},
	}

	applyStacktracePrivacyFilters(event)

	assert.Nil(t, event.Exception[0].Stacktrace, "warning-level events should still have stacktraces stripped")
}

func TestApplyStacktracePrivacyFilters_FatalLevel(t *testing.T) {
	t.Parallel()

	event := &sentry.Event{
		Level: sentry.LevelFatal,
		Exception: []sentry.Exception{{
			Stacktrace: &sentry.Stacktrace{
				Frames: []sentry.Frame{{Function: "f", AbsPath: "/home/user/code.go"}},
			},
		}},
	}

	applyStacktracePrivacyFilters(event)

	assert.NotNil(t, event.Exception[0].Stacktrace, "fatal-level events should retain stacktraces")
}

func TestInitSentry_DisabledDrainsQueue(t *testing.T) {
	// Not parallel — mutates package-level state

	EnableTestMode()
	t.Cleanup(func() {
		DisableTestMode()
		deferredMutex.Lock()
		deferredMessages = nil
		sentryInitialized = false
		deferredOverflowLogged = false
		deferredMutex.Unlock()
	})

	// Pre-populate deferred messages
	deferredMutex.Lock()
	deferredMessages = []DeferredMessage{
		{Message: "msg-1", Level: sentry.LevelWarning, Component: "test"},
		{Message: "msg-2", Level: sentry.LevelError, Component: "test"},
	}
	sentryInitialized = false
	deferredMutex.Unlock()

	// Call InitSentry with Sentry disabled
	settings := &conf.Settings{}
	settings.Sentry.Enabled = false

	err := InitSentry(settings)
	require.NoError(t, err)

	// Verify queue is drained and state is set
	deferredMutex.Lock()
	defer deferredMutex.Unlock()

	assert.Nil(t, deferredMessages, "deferred messages should be nil after opt-out drain")
	assert.True(t, sentryInitialized, "sentryInitialized should be true after opt-out")
}

func TestCaptureMessageDeferred_Cap(t *testing.T) {
	// Not parallel — mutates package-level state

	EnableTestMode()
	t.Cleanup(func() {
		DisableTestMode()
		deferredMutex.Lock()
		deferredMessages = nil
		sentryInitialized = false
		deferredOverflowLogged = false
		deferredMutex.Unlock()
	})

	// Reset state so messages are deferred (not sent immediately)
	deferredMutex.Lock()
	deferredMessages = nil
	sentryInitialized = false
	deferredOverflowLogged = false
	deferredMutex.Unlock()

	// Queue more than maxDeferredMessages
	for i := range maxDeferredMessages + 50 {
		CaptureMessageDeferred(
			fmt.Sprintf("msg-%d", i),
			sentry.LevelWarning,
			"test",
		)
	}

	deferredMutex.Lock()
	count := len(deferredMessages)
	deferredMutex.Unlock()

	assert.Equal(t, maxDeferredMessages, count,
		"deferred queue should be capped at maxDeferredMessages")
}

func TestCaptureError_NilError(t *testing.T) {
	// Not parallel — mutates package-level state (EnableTestMode)

	// Enable test mode so telemetry functions don't skip
	EnableTestMode()
	t.Cleanup(func() {
		DisableTestMode()
	})

	// CaptureError with nil should not panic
	assert.NotPanics(t, func() {
		CaptureError(nil, "test-component")
	}, "CaptureError should not panic on nil error")
}

func TestCaptureMessage_InfoLevelFiltered(t *testing.T) {
	t.Parallel()
	// Info and debug-level messages should be silently dropped.
	// These calls should not panic even without Sentry initialized.
	CaptureMessage("System initialized", sentry.LevelInfo, "system")
	CaptureMessage("Verbose trace", sentry.LevelDebug, "system")
	FastCaptureMessage("System initialized", sentry.LevelInfo, "system")
	FastCaptureMessage("Verbose trace", sentry.LevelDebug, "system")
	// If we get here without panic, the early return works.
}
