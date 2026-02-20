//go:build integration

// webhook_integration_test.go tests the webhook provider against real HTTP servers
// using httptest.Server to verify end-to-end payload delivery, authentication,
// custom templates, and failover behavior with full notification objects.

package notification_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/notification"
)

// webhookPayloadCapture captures request details for verification.
type webhookPayloadCapture struct {
	Method      string
	ContentType string
	Body        string
	Headers     http.Header
}

// newCaptureServer creates an httptest.Server that captures request details and responds with 200.
func newCaptureServer(t *testing.T) (*httptest.Server, *webhookPayloadCapture) {
	t.Helper()
	capture := &webhookPayloadCapture{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capture.Method = r.Method
		capture.ContentType = r.Header.Get("Content-Type")
		capture.Headers = r.Header.Clone()
		body, _ := io.ReadAll(r.Body)
		capture.Body = string(body)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)
	return server, capture
}

func TestWebhookIntegration_DefaultPayload(t *testing.T) {
	server, capture := newCaptureServer(t)

	endpoints := []notification.WebhookEndpoint{
		{URL: server.URL, Method: "POST"},
	}
	provider, err := notification.NewWebhookProvider("test-webhook", true, endpoints, nil, "")
	require.NoError(t, err)
	require.NoError(t, provider.ValidateConfig())
	t.Cleanup(provider.Close)

	n := notification.NewNotification(
		notification.TypeDetection,
		notification.PriorityHigh,
		"Robin Detected",
		"American Robin detected with 95% confidence",
	)
	n.Component = "analysis"
	n.Metadata = map[string]any{
		"species":    "American Robin",
		"confidence": 0.95,
		"location":   "backyard",
	}

	err = provider.Send(context.Background(), n)
	require.NoError(t, err, "Send should succeed")

	// Verify HTTP request details
	assert.Equal(t, "POST", capture.Method)
	assert.Equal(t, "application/json", capture.ContentType)

	// Verify JSON payload structure
	var payload map[string]any
	require.NoError(t, json.Unmarshal([]byte(capture.Body), &payload))

	assert.Equal(t, "detection", payload["type"])
	assert.Equal(t, "high", payload["priority"])
	assert.Equal(t, "Robin Detected", payload["title"])
	assert.Equal(t, "American Robin detected with 95% confidence", payload["message"])
	assert.Equal(t, "analysis", payload["component"])
	assert.NotEmpty(t, payload["id"])
	assert.NotEmpty(t, payload["timestamp"])

	// Verify metadata
	metadata, ok := payload["metadata"].(map[string]any)
	require.True(t, ok, "metadata should be a map")
	assert.Equal(t, "American Robin", metadata["species"])
	assert.InDelta(t, 0.95, metadata["confidence"], 0.001)
}

func TestWebhookIntegration_CustomTemplate(t *testing.T) {
	tests := []struct {
		name           string
		template       string
		notification   *notification.Notification
		expectedFields map[string]any
	}{
		{
			name:     "discord_embed_style",
			template: `{"content":"","embeds":[{"title":"{{.Title}}","description":"{{.Message}}","color":3066993}]}`,
			notification: func() *notification.Notification {
				return notification.NewNotification(
					notification.TypeDetection, notification.PriorityHigh,
					"Bird Alert", "A robin was spotted",
				)
			}(),
			expectedFields: map[string]any{
				"content": "",
			},
		},
		{
			name:     "slack_block_style",
			template: `{"text":"{{.Title}}: {{.Message}}"}`,
			notification: func() *notification.Notification {
				return notification.NewNotification(
					notification.TypeInfo, notification.PriorityMedium,
					"System Update", "All systems operational",
				)
			}(),
			expectedFields: map[string]any{
				"text": "System Update: All systems operational",
			},
		},
		{
			name:     "minimal_payload",
			template: `{"event":"{{.Type}}","body":"{{.Message}}"}`,
			notification: func() *notification.Notification {
				return notification.NewNotification(
					notification.TypeWarning, notification.PriorityLow,
					"", "Disk usage at 85%",
				)
			}(),
			expectedFields: map[string]any{
				"event": "warning",
				"body":  "Disk usage at 85%",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server, capture := newCaptureServer(t)

			endpoints := []notification.WebhookEndpoint{
				{URL: server.URL, Method: "POST"},
			}
			provider, err := notification.NewWebhookProvider("test-template", true, endpoints, nil, tt.template)
			require.NoError(t, err)
			require.NoError(t, provider.ValidateConfig())
			t.Cleanup(provider.Close)

			err = provider.Send(context.Background(), tt.notification)
			require.NoError(t, err, "Send should succeed")

			// Parse response and verify expected fields
			var payload map[string]any
			require.NoError(t, json.Unmarshal([]byte(capture.Body), &payload))

			for key, expected := range tt.expectedFields {
				assert.Equal(t, expected, payload[key], "field %s should match", key)
			}
		})
	}
}

func TestWebhookIntegration_AuthTypes(t *testing.T) {
	tests := []struct {
		name       string
		auth       notification.WebhookAuth
		verifyAuth func(t *testing.T, headers http.Header)
	}{
		{
			name: "bearer_token",
			auth: notification.WebhookAuth{
				Type:  "bearer",
				Token: "secret-bird-token-2024",
			},
			verifyAuth: func(t *testing.T, headers http.Header) {
				t.Helper()
				assert.Equal(t, "Bearer secret-bird-token-2024", headers.Get("Authorization"))
			},
		},
		{
			name: "basic_auth",
			auth: notification.WebhookAuth{
				Type: "basic",
				User: "birdnet",
				Pass: "s3cret!pass",
			},
			verifyAuth: func(t *testing.T, headers http.Header) {
				t.Helper()
				auth := headers.Get("Authorization")
				assert.True(t, strings.HasPrefix(auth, "Basic "), "expected Basic auth prefix")
			},
		},
		{
			name: "custom_header",
			auth: notification.WebhookAuth{
				Type:   "custom",
				Header: "X-BirdNET-API-Key",
				Value:  "api-key-12345",
			},
			verifyAuth: func(t *testing.T, headers http.Header) {
				t.Helper()
				assert.Equal(t, "api-key-12345", headers.Get("X-BirdNET-API-Key"))
			},
		},
		{
			name: "no_auth",
			auth: notification.WebhookAuth{
				Type: "none",
			},
			verifyAuth: func(t *testing.T, headers http.Header) {
				t.Helper()
				assert.Empty(t, headers.Get("Authorization"), "should not have Authorization header")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server, capture := newCaptureServer(t)

			endpoints := []notification.WebhookEndpoint{
				{URL: server.URL, Method: "POST", Auth: tt.auth},
			}
			provider, err := notification.NewWebhookProvider("test-auth", true, endpoints, nil, "")
			require.NoError(t, err)
			require.NoError(t, provider.ValidateConfig())
			t.Cleanup(provider.Close)

			n := notification.NewNotification(
				notification.TypeInfo, notification.PriorityMedium,
				"Auth Test", "Testing authentication",
			)

			err = provider.Send(context.Background(), n)
			require.NoError(t, err, "Send should succeed")

			tt.verifyAuth(t, capture.Headers)
		})
	}
}

func TestWebhookIntegration_HTTPMethods(t *testing.T) {
	methods := []string{"POST", "PUT", "PATCH"}

	for _, method := range methods {
		t.Run(method, func(t *testing.T) {
			server, capture := newCaptureServer(t)

			endpoints := []notification.WebhookEndpoint{
				{URL: server.URL, Method: method},
			}
			provider, err := notification.NewWebhookProvider("test-method", true, endpoints, nil, "")
			require.NoError(t, err)
			require.NoError(t, provider.ValidateConfig())
			t.Cleanup(provider.Close)

			n := notification.NewNotification(
				notification.TypeInfo, notification.PriorityMedium,
				"Method Test", "Testing HTTP method",
			)

			err = provider.Send(context.Background(), n)
			require.NoError(t, err, "Send should succeed")
			assert.Equal(t, method, capture.Method)
		})
	}
}

func TestWebhookIntegration_Failover(t *testing.T) {
	t.Run("failover_to_second_endpoint", func(t *testing.T) {
		// First endpoint returns 503
		server1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
		}))
		t.Cleanup(server1.Close)

		// Second endpoint succeeds
		var server2Called atomic.Bool
		server2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			server2Called.Store(true)
			w.WriteHeader(http.StatusOK)
		}))
		t.Cleanup(server2.Close)

		endpoints := []notification.WebhookEndpoint{
			{URL: server1.URL, Method: "POST"},
			{URL: server2.URL, Method: "POST"},
		}
		provider, err := notification.NewWebhookProvider("test-failover", true, endpoints, nil, "")
		require.NoError(t, err)
		require.NoError(t, provider.ValidateConfig())
		t.Cleanup(provider.Close)

		n := notification.NewNotification(
			notification.TypeError, notification.PriorityCritical,
			"Failover Test", "Testing endpoint failover",
		)

		err = provider.Send(context.Background(), n)
		require.NoError(t, err, "Send should succeed via failover")
		assert.True(t, server2Called.Load(), "second endpoint should have been called")
	})

	t.Run("all_endpoints_fail", func(t *testing.T) {
		server1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		t.Cleanup(server1.Close)

		server2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadGateway)
		}))
		t.Cleanup(server2.Close)

		endpoints := []notification.WebhookEndpoint{
			{URL: server1.URL, Method: "POST"},
			{URL: server2.URL, Method: "POST"},
		}
		provider, err := notification.NewWebhookProvider("test-all-fail", true, endpoints, nil, "")
		require.NoError(t, err)
		require.NoError(t, provider.ValidateConfig())
		t.Cleanup(provider.Close)

		n := notification.NewNotification(
			notification.TypeError, notification.PriorityCritical,
			"All Fail", "All endpoints should fail",
		)

		err = provider.Send(context.Background(), n)
		assert.Error(t, err, "Send should fail when all endpoints fail")
		assert.Contains(t, err.Error(), "all webhook endpoints failed")
	})
}

func TestWebhookIntegration_ServerValidatesPayload(t *testing.T) {
	// Simulate a webhook receiver that validates the payload
	var receivedValid atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Content-Type") != "application/json" {
			w.WriteHeader(http.StatusUnsupportedMediaType)
			return
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		// Validate required fields
		if payload["id"] == nil || payload["type"] == nil || payload["message"] == nil {
			w.WriteHeader(http.StatusUnprocessableEntity)
			return
		}

		receivedValid.Store(true)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	endpoints := []notification.WebhookEndpoint{
		{URL: server.URL, Method: "POST"},
	}
	provider, err := notification.NewWebhookProvider("test-validate", true, endpoints, nil, "")
	require.NoError(t, err)
	require.NoError(t, provider.ValidateConfig())
	t.Cleanup(provider.Close)

	n := notification.NewNotification(
		notification.TypeDetection,
		notification.PriorityHigh,
		"Species Alert",
		"Barn Owl detected at backyard feeder",
	)

	err = provider.Send(context.Background(), n)
	require.NoError(t, err, "Send should succeed with valid payload")
	assert.True(t, receivedValid.Load(), "server should have received a valid payload")
}
