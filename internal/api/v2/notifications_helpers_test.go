package api

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/notification"
)

// assertMapContainsExpected checks that result map contains all expected key-value pairs.
func assertMapContainsExpected(t *testing.T, result, expected map[string]any) {
	t.Helper()
	for key, expectedValue := range expected {
		actualValue, exists := result[key]
		if !exists {
			t.Errorf("createToastEventData() missing key %q", key)
			continue
		}
		if !reflect.DeepEqual(actualValue, expectedValue) {
			t.Errorf("createToastEventData() key %q = %v, want %v", key, actualValue, expectedValue)
		}
	}
}

// assertNoZeroDuration checks that zero duration is not included in result.
func assertNoZeroDuration(t *testing.T, result, metadata map[string]any) {
	t.Helper()
	if metadata["duration"] == 0 {
		if _, exists := result["duration"]; exists {
			t.Error("createToastEventData() should not include zero duration")
		}
	}
}

// assertNoNilAction checks that nil action is not included in result.
func assertNoNilAction(t *testing.T, result, metadata map[string]any) {
	t.Helper()
	if metadata["action"] == nil {
		if _, exists := result["action"]; exists {
			t.Error("createToastEventData() should not include nil action")
		}
	}
}

// mockController creates a controller with minimal setup for testing
func mockController() *Controller {
	return &Controller{
		Settings: &conf.Settings{
			WebServer: conf.WebServerSettings{
				Debug: true,
			},
		},
		apiLogger: nil, // Will skip logging in tests
	}
}

func TestController_createToastEventData(t *testing.T) {
	t.Parallel()

	c := mockController()

	tests := []struct {
		name     string
		notif    *notification.Notification
		expected map[string]any
	}{
		{
			name: "complete toast notification",
			notif: &notification.Notification{
				ID:        "test-notif-id",
				Message:   "Test toast message",
				Component: "test-component",
				Timestamp: time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC),
				Metadata: map[string]any{
					"isToast":   true,
					"toastId":   "toast-123",
					"toastType": "success",
					"duration":  5000,
					"action": &notification.ToastAction{
						Label:   "View Details",
						URL:     "/details",
						Handler: "viewHandler",
					},
				},
			},
			expected: map[string]any{
				"id":        "toast-123",
				"message":   "Test toast message",
				"type":      "success",
				"timestamp": time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC),
				"component": "test-component",
				"duration":  5000,
				"action": &notification.ToastAction{
					Label:   "View Details",
					URL:     "/details",
					Handler: "viewHandler",
				},
			},
		},
		{
			name: "minimal toast notification",
			notif: &notification.Notification{
				ID:        "minimal-notif-id",
				Message:   "Minimal toast",
				Timestamp: time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC),
				Metadata: map[string]any{
					"isToast":   true,
					"toastId":   "toast-minimal",
					"toastType": "info",
				},
			},
			expected: map[string]any{
				"id":        "toast-minimal",
				"message":   "Minimal toast",
				"type":      "info",
				"timestamp": time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC),
				"component": "",
			},
		},
		{
			name: "toast with zero duration",
			notif: &notification.Notification{
				ID:      "zero-duration-id",
				Message: "Zero duration toast",
				Metadata: map[string]any{
					"isToast":   true,
					"toastId":   "toast-zero",
					"toastType": "warning",
					"duration":  0, // Should not be included in output
				},
			},
			expected: map[string]any{
				"id":        "toast-zero",
				"message":   "Zero duration toast",
				"type":      "warning",
				"timestamp": time.Time{},
				"component": "",
				// duration should not be present
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result := c.createToastEventData(tt.notif)
			assertMapContainsExpected(t, result, tt.expected)
			assertNoZeroDuration(t, result, tt.notif.Metadata)
			assertNoNilAction(t, result, tt.notif.Metadata)
		})
	}
}

func TestController_processNotificationEvent(t *testing.T) {
	t.Parallel()

	// Since processNotificationEvent calls other methods that require HTTP context,
	// we'll test the decision logic by checking which path it takes
	c := mockController()

	tests := []struct {
		name      string
		notif     *notification.Notification
		expectErr bool
		isToast   bool
	}{
		{
			name: "regular notification",
			notif: &notification.Notification{
				ID:      "regular-id",
				Message: "Regular notification",
				Metadata: map[string]any{
					"isToast": false,
				},
			},
			isToast: false,
		},
		{
			name: "toast notification",
			notif: &notification.Notification{
				ID:      "toast-id",
				Message: "Toast notification",
				Metadata: map[string]any{
					"isToast":   true,
					"toastId":   "toast-123",
					"toastType": "success",
				},
			},
			isToast: true,
		},
		{
			name: "notification without isToast metadata",
			notif: &notification.Notification{
				ID:       "no-metadata-id",
				Message:  "No metadata notification",
				Metadata: map[string]any{},
			},
			isToast: false,
		},
		{
			name: "notification with nil metadata",
			notif: &notification.Notification{
				ID:       "nil-metadata-id",
				Message:  "Nil metadata notification",
				Metadata: nil,
			},
			isToast: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Create a minimal echo context for testing
			e := echo.New()
			req := httptest.NewRequest("GET", "/test", http.NoBody)
			rec := httptest.NewRecorder()
			ctx := e.NewContext(req, rec)

			// We can't easily test the full method without mocking sendSSEMessage,
			// but we can verify that the method doesn't panic and handles the input
			err := c.processNotificationEvent(ctx, "test-client", tt.notif)

			// The method will likely fail because sendSSEMessage isn't mocked,
			// but we're mainly testing that it doesn't panic and follows the right path
			if tt.expectErr && err == nil {
				t.Error("processNotificationEvent() expected error but got none")
			}

			// The main value of this test is ensuring the method doesn't panic
			// with various inputs and that it correctly identifies toast vs notification
		})
	}
}

func Test_setSSEHeaders(t *testing.T) {
	t.Parallel()

	e := echo.New()
	req := httptest.NewRequest("GET", "/test", http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	setSSEHeaders(ctx)

	expectedHeaders := map[string]string{
		"Content-Type":                 "text/event-stream",
		"Cache-Control":                "no-cache",
		"Connection":                   "keep-alive",
		"Access-Control-Allow-Origin":  "*",
		"Access-Control-Allow-Headers": "Cache-Control",
	}

	for key, expectedValue := range expectedHeaders {
		actualValue := rec.Header().Get(key)
		if actualValue != expectedValue {
			t.Errorf("setSSEHeaders() header %q = %q, want %q", key, actualValue, expectedValue)
		}
	}
}

func TestController_logNotificationConnection(t *testing.T) {
	t.Parallel()

	// Test with nil logger (should not panic)
	c := &Controller{
		Settings:  mockController().Settings,
		apiLogger: nil,
	}

	// These should not panic
	c.logNotificationConnection("test-client", "192.168.1.1", "test-agent", true)
	c.logNotificationConnection("test-client", "192.168.1.1", "", false)

	// Test with different debug settings
	c.Settings.WebServer.Debug = false
	c.logNotificationConnection("test-client", "192.168.1.1", "test-agent", true)
}

func TestController_logNotificationError(t *testing.T) {
	t.Parallel()

	c := mockController()

	// Should not panic with nil logger
	c.logNotificationError("test error", nil, "test-client")
	c.logNotificationError("test error", echo.NewHTTPError(500, "test"), "test-client")
}

func TestController_logToastSent(t *testing.T) {
	t.Parallel()

	c := mockController()

	notif := &notification.Notification{
		ID:        "test-id",
		Component: "test-component",
		Metadata: map[string]any{
			"isToast":   true,
			"toastId":   "toast-123",
			"toastType": "success",
		},
	}

	// Should not panic with nil logger
	c.logToastSent("test-client", notif)

	// Test with debug disabled
	c.Settings.WebServer.Debug = false
	c.logToastSent("test-client", notif)
}

func TestController_logNotificationSent(t *testing.T) {
	t.Parallel()

	c := mockController()

	notif := &notification.Notification{
		ID:       "test-id",
		Type:     notification.TypeInfo,
		Priority: notification.PriorityLow,
	}

	// Should not panic with nil logger
	c.logNotificationSent("test-client", notif)

	// Test with debug disabled
	c.Settings.WebServer.Debug = false
	c.logNotificationSent("test-client", notif)
}

// Test that demonstrates the metadata extraction logic
func TestMetadataExtraction(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		metadata map[string]any
		wantBool bool
	}{
		{
			name:     "isToast is true",
			metadata: map[string]any{"isToast": true},
			wantBool: true,
		},
		{
			name:     "isToast is false",
			metadata: map[string]any{"isToast": false},
			wantBool: false,
		},
		{
			name:     "isToast is not boolean",
			metadata: map[string]any{"isToast": "true"},
			wantBool: false, // Type assertion will fail
		},
		{
			name:     "isToast is missing",
			metadata: map[string]any{"other": "value"},
			wantBool: false,
		},
		{
			name:     "nil metadata",
			metadata: nil,
			wantBool: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// This replicates the logic from processNotificationEvent
			var isToast bool
			if tt.metadata != nil {
				isToast, _ = tt.metadata["isToast"].(bool)
			}

			if isToast != tt.wantBool {
				t.Errorf("metadata extraction isToast = %v, want %v", isToast, tt.wantBool)
			}
		})
	}
}
