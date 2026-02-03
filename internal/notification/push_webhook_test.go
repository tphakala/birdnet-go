//nolint:gocognit // Table-driven tests have expected complexity
package notification

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewWebhookProvider(t *testing.T) {
	t.Run("default configuration", func(t *testing.T) {
		endpoints := []WebhookEndpoint{
			{URL: "https://example.com/webhook", Method: "POST"},
		}
		provider, err := NewWebhookProvider("test-webhook", true, endpoints, nil, "")
		require.NoError(t, err, "failed to create provider")

		assert.Equal(t, "test-webhook", provider.GetName())
		assert.True(t, provider.IsEnabled(), "expected provider to be enabled")
		// Should support all types by default
		assert.True(t, provider.SupportsType(TypeError), "expected to support 'error' type")
		assert.True(t, provider.SupportsType(TypeDetection), "expected to support 'detection' type")
	})

	t.Run("with custom types", func(t *testing.T) {
		endpoints := []WebhookEndpoint{
			{URL: "https://example.com/webhook", Method: "POST"},
		}
		provider, err := NewWebhookProvider("test", true, endpoints, []string{"error", "warning"}, "")
		require.NoError(t, err, "failed to create provider")

		assert.True(t, provider.SupportsType(TypeError), "expected to support 'error' type")
		assert.True(t, provider.SupportsType(TypeWarning), "expected to support 'warning' type")
		assert.False(t, provider.SupportsType(TypeDetection), "expected NOT to support 'detection' type")
	})

	t.Run("with custom template", func(t *testing.T) {
		endpoints := []WebhookEndpoint{
			{URL: "https://example.com/webhook", Method: "POST"},
		}
		template := `{"event": "{{.Type}}", "title": "{{.Title}}"}`
		provider, err := NewWebhookProvider("test", true, endpoints, nil, template)
		require.NoError(t, err, "failed to create provider")

		assert.NotNil(t, provider.template, "expected template to be parsed")
	})

	t.Run("invalid template", func(t *testing.T) {
		endpoints := []WebhookEndpoint{
			{URL: "https://example.com/webhook", Method: "POST"},
		}
		template := `{{.InvalidField`
		_, err := NewWebhookProvider("test", true, endpoints, nil, template)
		require.Error(t, err, "expected error for invalid template")
	})
}

func TestWebhookProvider_ValidateConfig(t *testing.T) {
	t.Run("valid configuration", func(t *testing.T) {
		endpoints := []WebhookEndpoint{
			{URL: "https://example.com/webhook", Method: "POST"},
		}
		provider, _ := NewWebhookProvider("test", true, endpoints, nil, "")
		err := provider.ValidateConfig()
		require.NoError(t, err)
	})

	t.Run("disabled provider", func(t *testing.T) {
		endpoints := []WebhookEndpoint{}
		provider, _ := NewWebhookProvider("test", false, endpoints, nil, "")
		err := provider.ValidateConfig()
		require.NoError(t, err, "disabled provider should not validate")
	})

	t.Run("no endpoints", func(t *testing.T) {
		provider, _ := NewWebhookProvider("test", true, []WebhookEndpoint{}, nil, "")
		err := provider.ValidateConfig()
		require.Error(t, err, "expected error for no endpoints")
	})

	t.Run("empty URL", func(t *testing.T) {
		endpoints := []WebhookEndpoint{
			{URL: "", Method: "POST"},
		}
		provider, _ := NewWebhookProvider("test", true, endpoints, nil, "")
		err := provider.ValidateConfig()
		require.Error(t, err, "expected error for empty URL")
	})

	t.Run("invalid URL scheme", func(t *testing.T) {
		endpoints := []WebhookEndpoint{
			{URL: "ftp://example.com/webhook", Method: "POST"},
		}
		provider, _ := NewWebhookProvider("test", true, endpoints, nil, "")
		err := provider.ValidateConfig()
		require.Error(t, err, "expected error for invalid URL scheme")
	})

	t.Run("invalid HTTP method", func(t *testing.T) {
		endpoints := []WebhookEndpoint{
			{URL: "https://example.com/webhook", Method: "GET"},
		}
		provider, _ := NewWebhookProvider("test", true, endpoints, nil, "")
		err := provider.ValidateConfig()
		require.Error(t, err, "expected error for invalid HTTP method")
	})

	t.Run("default method to POST", func(t *testing.T) {
		endpoints := []WebhookEndpoint{
			{URL: "https://example.com/webhook", Method: ""},
		}
		provider, _ := NewWebhookProvider("test", true, endpoints, nil, "")
		err := provider.ValidateConfig()
		require.NoError(t, err)
		assert.Equal(t, "POST", provider.endpoints[0].Method, "expected default method POST")
	})
}

func TestWebhookProvider_ValidateAuth(t *testing.T) {
	tests := []struct {
		name     string
		auth     WebhookAuth
		wantErr  bool
		errorMsg string
	}{
		{
			name:    "none auth",
			auth:    WebhookAuth{Type: "none"},
			wantErr: false,
		},
		{
			name:    "empty auth type defaults to none",
			auth:    WebhookAuth{Type: ""},
			wantErr: false,
		},
		{
			name:    "valid bearer auth",
			auth:    WebhookAuth{Type: "bearer", Token: "secret-token"},
			wantErr: false,
		},
		{
			name:     "bearer auth without token",
			auth:     WebhookAuth{Type: "bearer"},
			wantErr:  true,
			errorMsg: "bearer auth requires token",
		},
		{
			name:    "valid basic auth",
			auth:    WebhookAuth{Type: "basic", User: "user", Pass: "pass"},
			wantErr: false,
		},
		{
			name:     "basic auth without user",
			auth:     WebhookAuth{Type: "basic", Pass: "pass"},
			wantErr:  true,
			errorMsg: "basic auth requires user and pass",
		},
		{
			name:    "valid custom auth",
			auth:    WebhookAuth{Type: "custom", Header: "X-API-Key", Value: "secret"},
			wantErr: false,
		},
		{
			name:     "custom auth without header",
			auth:     WebhookAuth{Type: "custom", Value: "secret"},
			wantErr:  true,
			errorMsg: "custom auth requires header name",
		},
		{
			name:     "unsupported auth type",
			auth:     WebhookAuth{Type: "oauth"},
			wantErr:  true,
			errorMsg: "unsupported auth type",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateResolvedWebhookAuth(&tt.auth)
			if tt.wantErr {
				require.Error(t, err, "expected error but got none")
				if tt.errorMsg != "" {
					assert.Contains(t, err.Error(), tt.errorMsg)
				}
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestWebhookProvider_Send(t *testing.T) {
	t.Run("successful send", func(t *testing.T) {
		receivedPayload := ""
		receivedMethod := ""
		receivedContentType := ""
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			receivedMethod = r.Method
			receivedContentType = r.Header.Get("Content-Type")
			body, _ := io.ReadAll(r.Body)
			receivedPayload = string(body)
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		endpoints := []WebhookEndpoint{
			{URL: server.URL, Method: "POST"},
		}
		provider, _ := NewWebhookProvider("test", true, endpoints, nil, "")
		_ = provider.ValidateConfig()

		notif := &Notification{
			ID:       "test-123",
			Type:     TypeError,
			Priority: PriorityCritical,
			Title:    "Test Error",
			Message:  "Test message",
		}

		err := provider.Send(t.Context(), notif)
		require.NoError(t, err)

		assert.Equal(t, "POST", receivedMethod)
		assert.Equal(t, "application/json", receivedContentType)
		require.NotEmpty(t, receivedPayload, "expected to receive payload")

		// Verify payload structure
		var payload WebhookPayload
		err = json.Unmarshal([]byte(receivedPayload), &payload)
		require.NoError(t, err, "failed to unmarshal payload")
		assert.Equal(t, "test-123", payload.ID)
		assert.Equal(t, "error", payload.Type)
	})

	t.Run("custom headers", func(t *testing.T) {
		receivedHeaders := make(http.Header)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			receivedHeaders = r.Header.Clone()
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		endpoints := []WebhookEndpoint{
			{
				URL:    server.URL,
				Method: "POST",
				Headers: map[string]string{
					"X-Custom-Header": "custom-value",
					"X-Another":       "test",
				},
			},
		}
		provider, _ := NewWebhookProvider("test", true, endpoints, nil, "")
		_ = provider.ValidateConfig()

		notif := &Notification{ID: "test", Type: TypeError}
		err := provider.Send(t.Context(), notif)
		require.NoError(t, err)

		assert.Equal(t, "custom-value", receivedHeaders.Get("X-Custom-Header"))
		assert.Equal(t, "test", receivedHeaders.Get("X-Another"))
	})

	t.Run("bearer authentication", func(t *testing.T) {
		receivedAuth := ""
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			receivedAuth = r.Header.Get("Authorization")
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		endpoints := []WebhookEndpoint{
			{
				URL:    server.URL,
				Method: "POST",
				Auth: WebhookAuth{
					Type:  "bearer",
					Token: "secret-token-123",
				},
			},
		}
		provider, _ := NewWebhookProvider("test", true, endpoints, nil, "")
		_ = provider.ValidateConfig()

		notif := &Notification{ID: "test", Type: TypeError}
		err := provider.Send(t.Context(), notif)
		require.NoError(t, err)

		assert.Equal(t, "Bearer secret-token-123", receivedAuth)
	})

	t.Run("basic authentication", func(t *testing.T) {
		receivedAuth := ""
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			receivedAuth = r.Header.Get("Authorization")
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		endpoints := []WebhookEndpoint{
			{
				URL:    server.URL,
				Method: "POST",
				Auth: WebhookAuth{
					Type: "basic",
					User: "testuser",
					Pass: "testpass",
				},
			},
		}
		provider, _ := NewWebhookProvider("test", true, endpoints, nil, "")
		_ = provider.ValidateConfig()

		notif := &Notification{ID: "test", Type: TypeError}
		err := provider.Send(t.Context(), notif)
		require.NoError(t, err)

		assert.True(t, strings.HasPrefix(receivedAuth, "Basic "), "expected Basic auth, got %q", receivedAuth)
	})

	t.Run("custom header authentication", func(t *testing.T) {
		receivedValue := ""
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			receivedValue = r.Header.Get("X-API-Key")
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		endpoints := []WebhookEndpoint{
			{
				URL:    server.URL,
				Method: "POST",
				Auth: WebhookAuth{
					Type:   "custom",
					Header: "X-API-Key",
					Value:  "api-key-123",
				},
			},
		}
		provider, _ := NewWebhookProvider("test", true, endpoints, nil, "")
		_ = provider.ValidateConfig()

		notif := &Notification{ID: "test", Type: TypeError}
		err := provider.Send(t.Context(), notif)
		require.NoError(t, err)

		assert.Equal(t, "api-key-123", receivedValue)
	})

	t.Run("server error response", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("Internal Server Error"))
		}))
		defer server.Close()

		endpoints := []WebhookEndpoint{
			{URL: server.URL, Method: "POST"},
		}
		provider, _ := NewWebhookProvider("test", true, endpoints, nil, "")
		_ = provider.ValidateConfig()

		notif := &Notification{ID: "test", Type: TypeError}
		err := provider.Send(t.Context(), notif)
		require.Error(t, err, "expected error for server error response")
		assert.Contains(t, err.Error(), "500", "expected error to contain status code")
	})

	t.Run("context cancellation", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(200 * time.Millisecond)
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		endpoints := []WebhookEndpoint{
			{URL: server.URL, Method: "POST"},
		}
		provider, _ := NewWebhookProvider("test", true, endpoints, nil, "")
		_ = provider.ValidateConfig()

		ctx, cancel := context.WithCancel(t.Context())
		cancel() // Cancel immediately

		notif := &Notification{ID: "test", Type: TypeError}
		err := provider.Send(ctx, notif)
		require.Error(t, err, "expected error for cancelled context")
	})

	t.Run("endpoint timeout", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(200 * time.Millisecond)
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		endpoints := []WebhookEndpoint{
			{
				URL:     server.URL,
				Method:  "POST",
				Timeout: 50 * time.Millisecond, // Very short timeout
			},
		}
		provider, _ := NewWebhookProvider("test", true, endpoints, nil, "")
		_ = provider.ValidateConfig()

		notif := &Notification{ID: "test", Type: TypeError}
		err := provider.Send(t.Context(), notif)
		require.Error(t, err, "expected timeout error")
	})

	t.Run("failover to second endpoint", func(t *testing.T) {
		// First endpoint fails
		server1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer server1.Close()

		// Second endpoint succeeds
		server2Called := false
		server2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			server2Called = true
			w.WriteHeader(http.StatusOK)
		}))
		defer server2.Close()

		endpoints := []WebhookEndpoint{
			{URL: server1.URL, Method: "POST"},
			{URL: server2.URL, Method: "POST"},
		}
		provider, _ := NewWebhookProvider("test", true, endpoints, nil, "")
		_ = provider.ValidateConfig()

		notif := &Notification{ID: "test", Type: TypeError}
		err := provider.Send(t.Context(), notif)
		require.NoError(t, err, "expected no error with failover")

		assert.True(t, server2Called, "expected second endpoint to be called after first failed")
	})

	t.Run("custom template", func(t *testing.T) {
		receivedPayload := ""
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			receivedPayload = string(body)
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		endpoints := []WebhookEndpoint{
			{URL: server.URL, Method: "POST"},
		}
		template := `{"event":"{{.Type}}","msg":"{{.Title}}"}`
		provider, _ := NewWebhookProvider("test", true, endpoints, nil, template)
		_ = provider.ValidateConfig()

		notif := &Notification{
			ID:    "test",
			Type:  TypeError,
			Title: "Test Error",
		}

		err := provider.Send(t.Context(), notif)
		require.NoError(t, err)

		expected := `{"event":"error","msg":"Test Error"}`
		assert.Equal(t, expected, receivedPayload)
	})
}

func TestWebhookProvider_Close(t *testing.T) {
	endpoints := []WebhookEndpoint{
		{URL: "https://example.com/webhook", Method: "POST"},
	}
	provider, _ := NewWebhookProvider("test", true, endpoints, nil, "")

	// Should not panic
	provider.Close()

	// Multiple closes should be safe
	provider.Close()
}

// TestWebhookProvider_validateAndNormalizeMethod tests HTTP method validation and normalization
func TestWebhookProvider_validateAndNormalizeMethod(t *testing.T) {
	t.Parallel()

	// Create a minimal provider for testing
	provider := &WebhookProvider{name: "test"}

	tests := []struct {
		name           string
		inputMethod    string
		expectedMethod string
		wantErr        bool
	}{
		// Valid methods - lowercase to uppercase
		{
			name:           "lowercase_post",
			inputMethod:    "post",
			expectedMethod: "POST",
			wantErr:        false,
		},
		{
			name:           "lowercase_put",
			inputMethod:    "put",
			expectedMethod: "PUT",
			wantErr:        false,
		},
		{
			name:           "lowercase_patch",
			inputMethod:    "patch",
			expectedMethod: "PATCH",
			wantErr:        false,
		},
		// Valid methods - mixed case
		{
			name:           "mixed_case_post",
			inputMethod:    "PoSt",
			expectedMethod: "POST",
			wantErr:        false,
		},
		{
			name:           "mixed_case_put",
			inputMethod:    "PuT",
			expectedMethod: "PUT",
			wantErr:        false,
		},
		{
			name:           "mixed_case_patch",
			inputMethod:    "PaTcH",
			expectedMethod: "PATCH",
			wantErr:        false,
		},
		// Valid methods - already uppercase
		{
			name:           "uppercase_post",
			inputMethod:    "POST",
			expectedMethod: "POST",
			wantErr:        false,
		},
		{
			name:           "uppercase_put",
			inputMethod:    "PUT",
			expectedMethod: "PUT",
			wantErr:        false,
		},
		{
			name:           "uppercase_patch",
			inputMethod:    "PATCH",
			expectedMethod: "PATCH",
			wantErr:        false,
		},
		// Whitespace trimming
		{
			name:           "whitespace_before_post",
			inputMethod:    "  POST",
			expectedMethod: "POST",
			wantErr:        false,
		},
		{
			name:           "whitespace_after_post",
			inputMethod:    "POST  ",
			expectedMethod: "POST",
			wantErr:        false,
		},
		{
			name:           "whitespace_around_post",
			inputMethod:    "  POST  ",
			expectedMethod: "POST",
			wantErr:        false,
		},
		{
			name:           "whitespace_around_lowercase",
			inputMethod:    "  put  ",
			expectedMethod: "PUT",
			wantErr:        false,
		},
		// Empty string defaults to POST
		{
			name:           "empty_defaults_to_post",
			inputMethod:    "",
			expectedMethod: "POST",
			wantErr:        false,
		},
		{
			name:           "whitespace_only_defaults_to_post",
			inputMethod:    "   ",
			expectedMethod: "POST",
			wantErr:        false,
		},
		// Invalid methods
		{
			name:           "invalid_get_lowercase",
			inputMethod:    "get",
			expectedMethod: "GET", // Gets normalized before rejection
			wantErr:        true,
		},
		{
			name:           "invalid_get_uppercase",
			inputMethod:    "GET",
			expectedMethod: "GET",
			wantErr:        true,
		},
		{
			name:           "invalid_delete",
			inputMethod:    "DELETE",
			expectedMethod: "DELETE",
			wantErr:        true,
		},
		{
			name:           "invalid_head",
			inputMethod:    "HEAD",
			expectedMethod: "HEAD",
			wantErr:        true,
		},
		{
			name:           "invalid_options",
			inputMethod:    "OPTIONS",
			expectedMethod: "OPTIONS",
			wantErr:        true,
		},
		{
			name:           "invalid_custom_method",
			inputMethod:    "CUSTOM",
			expectedMethod: "CUSTOM",
			wantErr:        true,
		},
		{
			name:           "invalid_whitespace_get",
			inputMethod:    "  GET  ",
			expectedMethod: "GET",
			wantErr:        true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			endpoint := &WebhookEndpoint{Method: tt.inputMethod}
			err := provider.validateAndNormalizeMethod(0, endpoint)

			// Check method was normalized in-place
			assert.Equal(t, tt.expectedMethod, endpoint.Method, "method should be normalized")

			if tt.wantErr {
				require.Error(t, err, "expected error for invalid method")
				assert.Contains(t, err.Error(), "must be POST, PUT, or PATCH")
			} else {
				require.NoError(t, err, "expected no error for valid method")
			}
		})
	}
}
