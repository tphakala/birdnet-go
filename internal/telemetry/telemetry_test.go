package telemetry

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/getsentry/sentry-go"
)

func TestMockTransport(t *testing.T) {
	t.Run("SendEvent stores events", func(t *testing.T) {
		transport := NewMockTransport()
		
		event := &sentry.Event{
			Message: "test event",
			Level:   sentry.LevelInfo,
			Tags: map[string]string{
				"component": "test",
			},
		}
		
		transport.SendEvent(event)
		
		if count := transport.GetEventCount(); count != 1 {
			t.Errorf("Expected 1 event, got %d", count)
		}
		
		captured := transport.GetLastEvent()
		if captured == nil {
			t.Fatal("Expected event to be captured")
		}
		
		if captured.Message != "test event" {
			t.Errorf("Expected message 'test event', got %s", captured.Message)
		}
	})
	
	t.Run("Clear removes all events", func(t *testing.T) {
		transport := NewMockTransport()
		
		// Send multiple events
		for i := 0; i < 5; i++ {
			transport.SendEvent(&sentry.Event{
				Message: fmt.Sprintf("event %d", i),
			})
		}
		
		if count := transport.GetEventCount(); count != 5 {
			t.Errorf("Expected 5 events, got %d", count)
		}
		
		transport.Clear()
		
		if count := transport.GetEventCount(); count != 0 {
			t.Errorf("Expected 0 events after clear, got %d", count)
		}
	})
	
	t.Run("SetDisabled prevents event capture", func(t *testing.T) {
		transport := NewMockTransport()
		transport.SetDisabled(true)
		
		transport.SendEvent(&sentry.Event{Message: "should not be captured"})
		
		if count := transport.GetEventCount(); count != 0 {
			t.Errorf("Expected 0 events when disabled, got %d", count)
		}
	})
	
	t.Run("FindEventByMessage locates events", func(t *testing.T) {
		transport := NewMockTransport()
		
		events := []string{"first", "second", "third"}
		for _, msg := range events {
			transport.SendEvent(&sentry.Event{Message: msg})
		}
		
		found := transport.FindEventByMessage("second")
		if found == nil {
			t.Error("Expected to find event with message 'second'")
		} else if found.Message != "second" {
			t.Errorf("Found wrong event: %s", found.Message)
		}
		
		notFound := transport.FindEventByMessage("fourth")
		if notFound != nil {
			t.Error("Should not find non-existent event")
		}
	})
	
	t.Run("WaitForEventCount with timeout", func(t *testing.T) {
		transport := NewMockTransport()
		
		// Test immediate success
		transport.SendEvent(&sentry.Event{Message: "event1"})
		transport.SendEvent(&sentry.Event{Message: "event2"})
		
		if !transport.WaitForEventCount(2, 100*time.Millisecond) {
			t.Error("Expected WaitForEventCount to succeed immediately")
		}
		
		// Test timeout
		if transport.WaitForEventCount(5, 50*time.Millisecond) {
			t.Error("Expected WaitForEventCount to timeout")
		}
	})
	
	t.Run("FlushWithContext respects cancellation", func(t *testing.T) {
		transport := NewMockTransport()
		transport.SetDelay(100 * time.Millisecond)
		
		// Test with cancelled context
		ctx, cancel := context.WithCancel(context.Background())
		cancel() // Cancel immediately
		
		if transport.FlushWithContext(ctx) {
			t.Error("Expected flush to fail with cancelled context")
		}
		
		// Test with timeout
		ctx, cancel = context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()
		
		if !transport.FlushWithContext(ctx) {
			t.Error("Expected flush to succeed within timeout")
		}
	})
}

func TestURLAnonymization(t *testing.T) {
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
			scrubbed := ScrubMessage(tt.input)
			
			if tt.notContains != "" && strings.Contains(scrubbed, tt.notContains) {
				t.Errorf("Scrubbed message should not contain %q, got: %s", tt.notContains, scrubbed)
			}
			
			if tt.contains != "" && !strings.Contains(scrubbed, tt.contains) {
				t.Errorf("Scrubbed message should contain %q, got: %s", tt.contains, scrubbed)
			}
		})
	}
}

func TestEventSummaries(t *testing.T) {
	transport := NewMockTransport()
	
	// Create events with different properties
	events := []struct {
		message string
		level   sentry.Level
		tags    map[string]string
		extra   map[string]interface{}
	}{
		{
			message: "Info event",
			level:   sentry.LevelInfo,
			tags:    map[string]string{"component": "test", "version": "1.0"},
			extra:   map[string]interface{}{"count": 42},
		},
		{
			message: "Error event",
			level:   sentry.LevelError,
			tags:    map[string]string{"component": "api"},
			extra:   map[string]interface{}{"endpoint": "/test"},
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
	if len(summaries) != len(events) {
		t.Fatalf("Expected %d summaries, got %d", len(events), len(summaries))
	}
	
	// Verify first summary
	s := summaries[0]
	if s.Message != "Info event" {
		t.Errorf("Expected message 'Info event', got %s", s.Message)
	}
	if s.Level != "info" {
		t.Errorf("Expected level 'info', got %s", s.Level)
	}
	if s.Tags["component"] != "test" {
		t.Errorf("Expected tag component='test', got %s", s.Tags["component"])
	}
	if count, ok := s.Extra["count"].(float64); !ok || count != 42 {
		t.Errorf("Expected extra count=42, got %v", s.Extra["count"])
	}
}

func TestConcurrentAccess(t *testing.T) {
	transport := NewMockTransport()
	
	// Run concurrent operations
	var wg sync.WaitGroup
	numGoroutines := 10
	eventsPerGoroutine := 100
	
	// Writers
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < eventsPerGoroutine; j++ {
				transport.SendEvent(&sentry.Event{
					Message: fmt.Sprintf("event-%d-%d", id, j),
				})
			}
		}(i)
	}
	
	// Readers
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < eventsPerGoroutine; j++ {
				_ = transport.GetEventCount()
				_ = transport.GetEvents()
				time.Sleep(time.Microsecond) // Small delay to increase contention
			}
		}()
	}
	
	wg.Wait()
	
	// Verify all events were captured
	expectedCount := numGoroutines * eventsPerGoroutine
	if count := transport.GetEventCount(); count != expectedCount {
		t.Errorf("Expected %d events, got %d", expectedCount, count)
	}
}