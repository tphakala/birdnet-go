package notifications

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/notification"
)

// assertMapContainsExpected checks that result map contains all expected key-value pairs.
func assertMapContainsExpected(t *testing.T, result, expected map[string]any) {
	t.Helper()
	for key, expectedValue := range expected {
		actualValue, exists := result[key]
		assert.True(t, exists, "createToastEventData() missing key %q", key)
		if exists {
			assert.Equal(t, expectedValue, actualValue, "createToastEventData() key %q mismatch", key)
		}
	}
}

// assertNoZeroDuration checks that zero duration is not included in result.
func assertNoZeroDuration(t *testing.T, result, metadata map[string]any) {
	t.Helper()
	if metadata["duration"] == 0 {
		_, exists := result["duration"]
		assert.False(t, exists, "createToastEventData() should not include zero duration")
	}
}

// assertNoNilAction checks that nil action is not included in result.
func assertNoNilAction(t *testing.T, result, metadata map[string]any) {
	t.Helper()
	if metadata["action"] == nil {
		_, exists := result["action"]
		assert.False(t, exists, "createToastEventData() should not include nil action")
	}
}

func TestController_createToastEventData(t *testing.T) {
	t.Parallel()

	c := mockHandler()

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
		{
			name: "toast with translation keys",
			notif: &notification.Notification{
				ID:            "i18n-toast-id",
				Message:       "Updating MQTT connection...",
				Component:     "api",
				Timestamp:     time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC),
				MessageKey:    "notifications.content.settings.reconfiguringMqtt",
				MessageParams: map[string]any{"setting": "mqtt"},
				Metadata: map[string]any{
					"isToast":   true,
					"toastId":   "toast-i18n",
					"toastType": "info",
					"duration":  3000,
				},
			},
			expected: map[string]any{
				"id":          "toast-i18n",
				"message":     "Updating MQTT connection...",
				"type":        "info",
				"timestamp":   time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC),
				"component":   "api",
				"duration":    3000,
				"message_key": "notifications.content.settings.reconfiguringMqtt",
			},
		},
		{
			name: "toast without translation keys omits key fields",
			notif: &notification.Notification{
				ID:      "no-key-toast-id",
				Message: "Plain toast",
				Metadata: map[string]any{
					"isToast":   true,
					"toastId":   "toast-plain",
					"toastType": "success",
				},
			},
			expected: map[string]any{
				"id":      "toast-plain",
				"message": "Plain toast",
				"type":    "success",
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

			// Verify translation key fields are included only when set
			if tt.notif.MessageKey != "" {
				assert.Equal(t, tt.notif.MessageKey, result["message_key"], "message_key should be included")
				if tt.notif.MessageParams != nil {
					assert.NotNil(t, result["message_params"], "message_params should be included when set")
				}
			} else {
				assertEventDataMissing(t, result, "message_key")
				assertEventDataMissing(t, result, "message_params")
			}
		})
	}
}

// TestController_processNotificationEvent checks that events flagged as toasts
// go out as the "toast" SSE event and everything else as "notification". The
// event name is read back from the recorded SSE frame.
func TestController_processNotificationEvent(t *testing.T) {
	t.Parallel()

	c := mockHandler()

	tests := []struct {
		name       string
		metadata   map[string]any
		wantPrefix string
	}{
		{
			name: "toast flag true sends a toast event",
			metadata: map[string]any{
				notification.MetadataKeyIsToast: true,
				"toastId":                       "toast-123",
				"toastType":                     "success",
			},
			wantPrefix: "event: toast\n",
		},
		{
			name:       "toast flag false sends a notification event",
			metadata:   map[string]any{notification.MetadataKeyIsToast: false},
			wantPrefix: "event: notification\n",
		},
		{
			// The flag is read with a bool type assertion, so a string does not count.
			name:       "toast flag as a string sends a notification event",
			metadata:   map[string]any{notification.MetadataKeyIsToast: "true"},
			wantPrefix: "event: notification\n",
		},
		{
			name:       "missing toast flag sends a notification event",
			metadata:   map[string]any{},
			wantPrefix: "event: notification\n",
		},
		{
			name:       "nil metadata sends a notification event",
			metadata:   nil,
			wantPrefix: "event: notification\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			e := echo.New()
			req := httptest.NewRequest(http.MethodGet, "/test", http.NoBody)
			rec := httptest.NewRecorder()
			ctx := e.NewContext(req, rec)

			notif := &notification.Notification{
				ID:       "dispatch-id",
				Message:  "dispatch message",
				Metadata: tt.metadata,
			}
			require.NoError(t, c.processNotificationEvent(ctx, "test-client", notif))
			assert.True(t, strings.HasPrefix(rec.Body.String(), tt.wantPrefix),
				"SSE frame should start with %q, got %q", tt.wantPrefix, rec.Body.String())
		})
	}
}
