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
)

func TestNewWebhookProvider(t *testing.T) {
	t.Run("default configuration", func(t *testing.T) {
		endpoints := []WebhookEndpoint{
			{URL: "https://example.com/webhook", Method: "POST"},
		}
		provider, err := NewWebhookProvider("test-webhook", true, endpoints, nil, "")
		if err != nil {
			t.Fatalf("failed to create provider: %v", err)
		}

		if provider.GetName() != "test-webhook" {
			t.Errorf("expected name 'test-webhook', got %q", provider.GetName())
		}
		if !provider.IsEnabled() {
			t.Error("expected provider to be enabled")
		}
		// Should support all types by default
		if !provider.SupportsType(TypeError) {
			t.Error("expected to support 'error' type")
		}
		if !provider.SupportsType(TypeDetection) {
			t.Error("expected to support 'detection' type")
		}
	})

	t.Run("with custom types", func(t *testing.T) {
		endpoints := []WebhookEndpoint{
			{URL: "https://example.com/webhook", Method: "POST"},
		}
		provider, err := NewWebhookProvider("test", true, endpoints, []string{"error", "warning"}, "")
		if err != nil {
			t.Fatalf("failed to create provider: %v", err)
		}

		if !provider.SupportsType(TypeError) {
			t.Error("expected to support 'error' type")
		}
		if !provider.SupportsType(TypeWarning) {
			t.Error("expected to support 'warning' type")
		}
		if provider.SupportsType(TypeDetection) {
			t.Error("expected NOT to support 'detection' type")
		}
	})

	t.Run("with custom template", func(t *testing.T) {
		endpoints := []WebhookEndpoint{
			{URL: "https://example.com/webhook", Method: "POST"},
		}
		template := `{"event": "{{.Type}}", "title": "{{.Title}}"}`
		provider, err := NewWebhookProvider("test", true, endpoints, nil, template)
		if err != nil {
			t.Fatalf("failed to create provider: %v", err)
		}

		if provider.template == nil {
			t.Error("expected template to be parsed")
		}
	})

	t.Run("invalid template", func(t *testing.T) {
		endpoints := []WebhookEndpoint{
			{URL: "https://example.com/webhook", Method: "POST"},
		}
		template := `{{.InvalidField`
		_, err := NewWebhookProvider("test", true, endpoints, nil, template)
		if err == nil {
			t.Error("expected error for invalid template")
		}
	})
}

func TestWebhookProvider_ValidateConfig(t *testing.T) {
	t.Run("valid configuration", func(t *testing.T) {
		endpoints := []WebhookEndpoint{
			{URL: "https://example.com/webhook", Method: "POST"},
		}
		provider, _ := NewWebhookProvider("test", true, endpoints, nil, "")
		err := provider.ValidateConfig()
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
	})

	t.Run("disabled provider", func(t *testing.T) {
		endpoints := []WebhookEndpoint{}
		provider, _ := NewWebhookProvider("test", false, endpoints, nil, "")
		err := provider.ValidateConfig()
		if err != nil {
			t.Errorf("disabled provider should not validate, got %v", err)
		}
	})

	t.Run("no endpoints", func(t *testing.T) {
		provider, _ := NewWebhookProvider("test", true, []WebhookEndpoint{}, nil, "")
		err := provider.ValidateConfig()
		if err == nil {
			t.Error("expected error for no endpoints")
		}
	})

	t.Run("empty URL", func(t *testing.T) {
		endpoints := []WebhookEndpoint{
			{URL: "", Method: "POST"},
		}
		provider, _ := NewWebhookProvider("test", true, endpoints, nil, "")
		err := provider.ValidateConfig()
		if err == nil {
			t.Error("expected error for empty URL")
		}
	})

	t.Run("invalid URL scheme", func(t *testing.T) {
		endpoints := []WebhookEndpoint{
			{URL: "ftp://example.com/webhook", Method: "POST"},
		}
		provider, _ := NewWebhookProvider("test", true, endpoints, nil, "")
		err := provider.ValidateConfig()
		if err == nil {
			t.Error("expected error for invalid URL scheme")
		}
	})

	t.Run("invalid HTTP method", func(t *testing.T) {
		endpoints := []WebhookEndpoint{
			{URL: "https://example.com/webhook", Method: "GET"},
		}
		provider, _ := NewWebhookProvider("test", true, endpoints, nil, "")
		err := provider.ValidateConfig()
		if err == nil {
			t.Error("expected error for invalid HTTP method")
		}
	})

	t.Run("default method to POST", func(t *testing.T) {
		endpoints := []WebhookEndpoint{
			{URL: "https://example.com/webhook", Method: ""},
		}
		provider, _ := NewWebhookProvider("test", true, endpoints, nil, "")
		err := provider.ValidateConfig()
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
		if provider.endpoints[0].Method != "POST" {
			t.Errorf("expected default method POST, got %s", provider.endpoints[0].Method)
		}
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
			if tt.wantErr && err == nil {
				t.Error("expected error but got none")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("expected no error, got %v", err)
			}
			if tt.wantErr && err != nil && tt.errorMsg != "" {
				if !strings.Contains(err.Error(), tt.errorMsg) {
					t.Errorf("expected error containing %q, got %q", tt.errorMsg, err.Error())
				}
			}
		})
	}
}

func TestWebhookProvider_Send(t *testing.T) {
	t.Run("successful send", func(t *testing.T) {
		receivedPayload := ""
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != "POST" {
				t.Errorf("expected POST, got %s", r.Method)
			}
			if ct := r.Header.Get("Content-Type"); ct != "application/json" {
				t.Errorf("expected Content-Type application/json, got %q", ct)
			}
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

		err := provider.Send(context.Background(), notif)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}

		if receivedPayload == "" {
			t.Error("expected to receive payload")
		}

		// Verify payload structure
		var payload WebhookPayload
		if err := json.Unmarshal([]byte(receivedPayload), &payload); err != nil {
			t.Fatalf("failed to unmarshal payload: %v", err)
		}
		if payload.ID != "test-123" {
			t.Errorf("expected ID 'test-123', got %q", payload.ID)
		}
		if payload.Type != "error" {
			t.Errorf("expected type 'error', got %q", payload.Type)
		}
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
		err := provider.Send(context.Background(), notif)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}

		if receivedHeaders.Get("X-Custom-Header") != "custom-value" {
			t.Errorf("expected X-Custom-Header 'custom-value', got %q", receivedHeaders.Get("X-Custom-Header"))
		}
		if receivedHeaders.Get("X-Another") != "test" {
			t.Errorf("expected X-Another 'test', got %q", receivedHeaders.Get("X-Another"))
		}
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
		err := provider.Send(context.Background(), notif)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}

		expected := "Bearer secret-token-123"
		if receivedAuth != expected {
			t.Errorf("expected Authorization %q, got %q", expected, receivedAuth)
		}
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
		err := provider.Send(context.Background(), notif)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}

		if !strings.HasPrefix(receivedAuth, "Basic ") {
			t.Errorf("expected Basic auth, got %q", receivedAuth)
		}
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
		err := provider.Send(context.Background(), notif)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}

		if receivedValue != "api-key-123" {
			t.Errorf("expected X-API-Key 'api-key-123', got %q", receivedValue)
		}
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
		err := provider.Send(context.Background(), notif)
		if err == nil {
			t.Error("expected error for server error response")
		}
		if !strings.Contains(err.Error(), "500") {
			t.Errorf("expected error to contain status code, got %v", err)
		}
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

		ctx, cancel := context.WithCancel(context.Background())
		cancel() // Cancel immediately

		notif := &Notification{ID: "test", Type: TypeError}
		err := provider.Send(ctx, notif)
		if err == nil {
			t.Error("expected error for cancelled context")
		}
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
		err := provider.Send(context.Background(), notif)
		if err == nil {
			t.Error("expected timeout error")
		}
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
		err := provider.Send(context.Background(), notif)
		if err != nil {
			t.Fatalf("expected no error with failover, got %v", err)
		}

		if !server2Called {
			t.Error("expected second endpoint to be called after first failed")
		}
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

		err := provider.Send(context.Background(), notif)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}

		expected := `{"event":"error","msg":"Test Error"}`
		if receivedPayload != expected {
			t.Errorf("expected payload %q, got %q", expected, receivedPayload)
		}
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
