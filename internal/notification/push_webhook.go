// Package notification webhook provider implementation
//
// LLM Implementation Notes - Go 1.24/1.25 Modern Patterns:
//
// 1. Context Management (Go 1.24+):
//   - All HTTP calls must use context.Context for cancellation
//   - Use req.WithContext(ctx) to propagate cancellation
//   - Never block indefinitely - always have timeouts
//
// 2. JSON Handling (Go 1.25):
//   - Prefer `omitzero` tag over `omitempty` for cleaner payloads
//   - Consider encoding/json/v2 for performance (when stable)
//
// 3. HTTP Best Practices (Go 1.24+):
//   - Use http.NoBody instead of nil for requests without body
//   - Always close response bodies to prevent connection leaks
//   - Use connection pooling (handled by httpclient package)
//
// 4. Error Handling:
//   - Wrap errors with %w for proper error chains
//   - Check context.Canceled and context.DeadlineExceeded explicitly
//   - Return structured errors for better categorization in metrics
//
// 5. Concurrency:
//   - All methods must be safe for concurrent use
//   - Use sync.RWMutex for read-heavy operations
//   - Prefer atomic operations for simple counters
package notification

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"text/template"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/httpclient"
	"github.com/tphakala/birdnet-go/internal/secrets"
)

const (
	// defaultWebhookTimeout is the default timeout for webhook HTTP requests
	defaultWebhookTimeout = 30 * time.Second

	// maxErrorBodySize limits error response body reading to prevent memory issues
	maxErrorBodySize = 1024

	// Webhook authentication type constants
	authTypeNone   = "none"
	authTypeBearer = "bearer"
	authTypeBasic  = "basic"
	authTypeCustom = "custom"
)

// WebhookProvider sends notifications to HTTP/HTTPS webhooks with customizable templates,
// authentication, and retry logic. Supports multiple endpoints for failover.
//
// Thread-safe for concurrent use.
type WebhookProvider struct {
	name      string
	enabled   bool
	endpoints []WebhookEndpoint
	types     map[string]bool
	client    *httpclient.Client
	template  *template.Template // Optional custom JSON template
	telemetry *NotificationTelemetry
}

// WebhookEndpoint represents a single webhook destination with its configuration.
type WebhookEndpoint struct {
	URL     string
	Method  string // POST, PUT, PATCH
	Headers map[string]string
	Timeout time.Duration
	Auth    WebhookAuth
}

// WebhookAuth holds resolved authentication credentials for webhook requests.
// All secret values are resolved at provider initialization time.
type WebhookAuth struct {
	Type   string // "none", "bearer", "basic", "custom"
	Token  string // Resolved bearer token
	User   string // Resolved username
	Pass   string // Resolved password
	Header string // Custom header name
	Value  string // Resolved custom header value
}

// resolveWebhookAuth converts conf.WebhookAuthConfig to WebhookAuth with resolved secrets.
// This resolves environment variables and reads files at initialization time.
func resolveWebhookAuth(cfg *conf.WebhookAuthConfig) (*WebhookAuth, error) {
	auth := &WebhookAuth{
		Type: strings.ToLower(cfg.Type),
	}

	// Empty or "none" type needs no resolution
	if auth.Type == "" || auth.Type == authTypeNone {
		return auth, nil
	}

	var err error

	switch auth.Type {
	case authTypeBearer:
		auth.Token, err = secrets.MustResolve("bearer token", cfg.TokenFile, cfg.Token)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve bearer token: %w", err)
		}

	case authTypeBasic:
		auth.User, err = secrets.MustResolve("basic auth user", cfg.UserFile, cfg.User)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve basic auth user: %w", err)
		}
		auth.Pass, err = secrets.MustResolve("basic auth pass", cfg.PassFile, cfg.Pass)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve basic auth pass: %w", err)
		}

	case authTypeCustom:
		auth.Header = cfg.Header // Header name is not a secret
		auth.Value, err = secrets.MustResolve("custom header value", cfg.ValueFile, cfg.Value)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve custom header value: %w", err)
		}

	default:
		return nil, fmt.Errorf("unsupported auth type: %s", auth.Type)
	}

	return auth, nil
}

// WebhookPayload is the default JSON structure sent to webhooks.
// Uses `omitzero` (Go 1.24+) to omit empty fields instead of `omitempty`.
//
// LLM Note: `omitzero` is more precise than `omitempty` as it only omits
// true zero values (0, false, nil, "") instead of all "empty-ish" values.
type WebhookPayload struct {
	ID        string         `json:"id"`
	Type      string         `json:"type"`
	Priority  string         `json:"priority,omitzero"`
	Title     string         `json:"title"`
	Message   string         `json:"message"`
	Component string         `json:"component,omitzero"`
	Timestamp string         `json:"timestamp"`
	Metadata  map[string]any `json:"metadata,omitzero"`
}

// NewWebhookProvider creates a new webhook provider with the given configuration.
//
// Parameters:
//   - name: Provider name for logging/identification
//   - enabled: Whether the provider is enabled
//   - endpoints: List of webhook endpoints (supports failover)
//   - supportedTypes: Notification types this provider handles
//   - templateStr: Optional Go template for custom JSON payloads
func NewWebhookProvider(name string, enabled bool, endpoints []WebhookEndpoint, supportedTypes []string, templateStr string) (*WebhookProvider, error) {
	wp := &WebhookProvider{
		name:      strings.TrimSpace(name),
		enabled:   enabled,
		endpoints: slices.Clone(endpoints),
		types:     make(map[string]bool),
	}

	if wp.name == "" {
		wp.name = "webhook"
	}

	// Set supported types
	if len(supportedTypes) == 0 {
		// Default: support all types
		wp.types["error"] = true
		wp.types["warning"] = true
		wp.types["info"] = true
		wp.types["detection"] = true
		wp.types["system"] = true
	} else {
		for _, t := range supportedTypes {
			wp.types[t] = true
		}
	}

	// Parse custom template if provided
	if templateStr != "" {
		tmpl, err := template.New("webhook").Parse(templateStr)
		if err != nil {
			return nil, fmt.Errorf("failed to parse webhook template: %w", err)
		}

		// Validate template with dummy notification to catch errors early
		// This prevents runtime failures when the template is actually used
		testNotification := &Notification{
			ID:        "test-id",
			Type:      TypeInfo,
			Priority:  PriorityLow,
			Title:     "test",
			Message:   "test message",
			Component: "test-component",
			Timestamp: time.Now(),
			Metadata: map[string]any{
				"test_key":   "test_value",
				"confidence": TestConfidenceValue,
				"species":    "Test Species",
			},
		}
		if err := tmpl.Execute(io.Discard, testNotification); err != nil {
			return nil, fmt.Errorf("template validation failed: %w", err)
		}

		wp.template = tmpl
	}

	// Create HTTP client with production settings
	cfg := httpclient.DefaultConfig()
	cfg.UserAgent = "BirdNET-Go-Webhook/1.0"
	cfg.DefaultTimeout = defaultWebhookTimeout
	wp.client = httpclient.New(&cfg)

	return wp, nil
}

// GetName returns the provider name for logging and configuration.
func (w *WebhookProvider) GetName() string {
	return w.name
}

// IsEnabled returns whether this provider is currently enabled.
func (w *WebhookProvider) IsEnabled() bool {
	return w.enabled
}

// SupportsType checks if this provider handles the given notification type.
func (w *WebhookProvider) SupportsType(t Type) bool {
	return w.types[string(t)]
}

// GetEndpoints returns a deep copy of the webhook endpoints for this provider.
// Returns a defensive copy to prevent external modification of internal state.
// Both the slice and the Headers maps within each endpoint are cloned.
func (w *WebhookProvider) GetEndpoints() []WebhookEndpoint {
	endpoints := slices.Clone(w.endpoints)
	for i := range endpoints {
		// Deep copy the Headers map to prevent external mutation
		if endpoints[i].Headers != nil {
			endpoints[i].Headers = maps.Clone(endpoints[i].Headers)
		}
	}
	return endpoints
}

// ValidateConfig validates the webhook provider configuration.
// Called once during initialization to catch configuration errors early.
func (w *WebhookProvider) ValidateConfig() error {
	if !w.enabled {
		return nil
	}

	if len(w.endpoints) == 0 {
		return fmt.Errorf("at least one webhook endpoint is required")
	}

	for i := range w.endpoints {
		if err := w.validateEndpoint(i, &w.endpoints[i]); err != nil {
			return err
		}
	}

	return nil
}

// validateEndpoint validates a single webhook endpoint configuration.
func (w *WebhookProvider) validateEndpoint(index int, endpoint *WebhookEndpoint) error {
	if endpoint.URL == "" {
		return fmt.Errorf("endpoint %d: URL is required", index)
	}

	if err := w.validateAndNormalizeMethod(index, endpoint); err != nil {
		return err
	}

	if err := validateEndpointURL(index, endpoint.URL); err != nil {
		return err
	}

	if endpoint.Timeout < 0 {
		return fmt.Errorf("endpoint %d: timeout must be >= 0", index)
	}

	if err := validateResolvedWebhookAuth(&endpoint.Auth); err != nil {
		return fmt.Errorf("endpoint %d: %w", index, err)
	}

	return nil
}

// validateAndNormalizeMethod validates and normalizes the HTTP method.
func (w *WebhookProvider) validateAndNormalizeMethod(index int, endpoint *WebhookEndpoint) error {
	method := strings.ToUpper(strings.TrimSpace(endpoint.Method))
	if method == "" {
		method = http.MethodPost
	}
	endpoint.Method = method

	if method != http.MethodPost && method != http.MethodPut && method != http.MethodPatch {
		return fmt.Errorf("endpoint %d: method must be POST, PUT, or PATCH, got %s", index, method)
	}
	return nil
}

// validateEndpointURL validates the URL format and scheme.
func validateEndpointURL(index int, urlStr string) error {
	u, err := url.Parse(urlStr)
	if err != nil {
		return fmt.Errorf("endpoint %d: invalid URL: %w", index, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("endpoint %d: URL scheme must be http or https, got %s", index, u.Scheme)
	}
	if u.Host == "" {
		return fmt.Errorf("endpoint %d: URL host is required", index)
	}
	return nil
}

// validateResolvedWebhookAuth validates webhook authentication after secrets are resolved.
// At this point, all environment variables and file references have been resolved to actual values.
func validateResolvedWebhookAuth(auth *WebhookAuth) error {
	authType := strings.ToLower(auth.Type)
	if authType == "" {
		authType = authTypeNone
		auth.Type = authType
	}

	switch authType {
	case authTypeNone:
		return nil
	case authTypeBearer:
		if auth.Token == "" {
			return fmt.Errorf("bearer auth requires token (secret resolution may have failed)")
		}
	case authTypeBasic:
		if auth.User == "" || auth.Pass == "" {
			return fmt.Errorf("basic auth requires user and pass (secret resolution may have failed)")
		}
	case authTypeCustom:
		if auth.Header == "" {
			return fmt.Errorf("custom auth requires header name")
		}
		if strings.ContainsAny(auth.Header, "\r\n:") {
			return fmt.Errorf("custom auth header contains invalid characters")
		}
		if auth.Value == "" {
			return fmt.Errorf("custom auth requires value (secret resolution may have failed)")
		}
		if strings.ContainsAny(auth.Value, "\r\n") {
			return fmt.Errorf("custom auth value contains invalid characters")
		}
	default:
		return fmt.Errorf("unsupported auth type: %s", authType)
	}

	return nil
}

// Send sends a notification to all configured webhook endpoints.
// Attempts each endpoint in order until one succeeds, or returns error if all fail.
//
// Context handling (Go 1.24+ best practice):
//   - Respects context cancellation for immediate cleanup
//   - Applies per-endpoint timeout if configured
//   - Propagates context through entire HTTP request lifecycle
func (w *WebhookProvider) Send(ctx context.Context, n *Notification) error {
	if len(w.endpoints) == 0 {
		return fmt.Errorf("no webhook endpoints configured")
	}

	// Build payload once (reused for all endpoints)
	payload, err := w.buildPayload(n)
	if err != nil {
		return fmt.Errorf("failed to build webhook payload: %w", err)
	}

	// Try each endpoint until one succeeds (use index to avoid copying)
	errs := make([]error, 0, len(w.endpoints))
	for i := range w.endpoints {
		endpoint := &w.endpoints[i] // Use pointer to avoid copying

		// Create endpoint-specific context with timeout
		endpointCtx := ctx
		var cancel context.CancelFunc
		if endpoint.Timeout > 0 {
			endpointCtx, cancel = context.WithTimeout(ctx, endpoint.Timeout)
		}

		err := w.sendToEndpoint(endpointCtx, endpoint, payload)

		// Cancel timeout context if created
		if cancel != nil {
			cancel()
		}

		if err == nil {
			return nil // Success!
		}

		errs = append(errs, fmt.Errorf("endpoint %d (%s): %w", i, endpoint.URL, err))

		// Check if context was cancelled - if so, stop trying other endpoints
		if ctx.Err() != nil {
			return fmt.Errorf("context cancelled while sending webhook: %w", ctx.Err())
		}
	}

	return fmt.Errorf("all webhook endpoints failed: %w", errors.Join(errs...))
}

// buildPayload constructs the JSON payload to send to the webhook.
// Uses custom template if configured, otherwise uses default structure.
func (w *WebhookProvider) buildPayload(n *Notification) ([]byte, error) {
	if w.template != nil {
		// Use custom template
		var buf bytes.Buffer
		err := w.template.Execute(&buf, n)
		if err != nil {
			return nil, fmt.Errorf("template execution failed: %w", err)
		}
		b := buf.Bytes()
		if !json.Valid(b) {
			return nil, fmt.Errorf("template output is not valid JSON")
		}
		return b, nil
	}

	// Use default payload structure
	payload := WebhookPayload{
		ID:        n.ID,
		Type:      string(n.Type),
		Priority:  string(n.Priority),
		Title:     n.Title,
		Message:   n.Message,
		Component: n.Component,
		Timestamp: n.Timestamp.Format(time.RFC3339),
		Metadata:  n.Metadata,
	}

	// Marshal to JSON
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("json marshal failed: %w", err)
	}

	return data, nil
}

// sendToEndpoint sends the payload to a single webhook endpoint.
// endpoint is passed by pointer to avoid copying the large struct (144 bytes).
//
// Go 1.24+ HTTP best practices:
//   - Uses http.NoBody for requests without body (though webhooks need body)
//   - Always closes response body to prevent connection leaks
//   - Properly propagates context for cancellation
func (w *WebhookProvider) sendToEndpoint(ctx context.Context, endpoint *WebhookEndpoint, payload []byte) error {
	// Create HTTP request with context
	// LLM Note: Use bytes.NewReader for request body to support retries
	req, err := http.NewRequestWithContext(ctx, endpoint.Method, endpoint.URL, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers
	req.Header.Set("Content-Type", "application/json")
	for key, value := range endpoint.Headers {
		req.Header.Set(key, value)
	}

	// Apply authentication
	if err := applyWebhookAuth(req, &endpoint.Auth); err != nil {
		return fmt.Errorf("failed to apply auth: %w", err)
	}

	// Execute request
	// The httpclient.Do method handles context propagation and timeouts
	resp, err := w.client.Do(ctx, req)
	if err != nil {
		// Check for specific error types for better categorization
		isTimeout := errors.Is(err, context.DeadlineExceeded)
		isCancelled := errors.Is(err, context.Canceled)

		// Report telemetry for failed requests
		if w.telemetry != nil {
			w.telemetry.WebhookRequestError(
				w.name,
				err,
				0, // No status code for network errors
				endpoint.URL,
				endpoint.Method,
				endpoint.Auth.Type,
				isTimeout,
				isCancelled,
			)
		}

		if isCancelled {
			return fmt.Errorf("request cancelled: %w", err)
		}
		if isTimeout {
			return fmt.Errorf("request timed out: %w", err)
		}
		return fmt.Errorf("request failed: %w", err)
	}
	defer func() {
		// Always close response body to prevent connection leaks
		_, _ = io.Copy(io.Discard, resp.Body) // Drain body before closing
		_ = resp.Body.Close()
	}()

	// Check response status
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Read error response body for better diagnostics
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodySize))
		httpErr := fmt.Errorf("webhook returned status %d: %s", resp.StatusCode, string(body))

		// Report telemetry for HTTP errors
		if w.telemetry != nil {
			w.telemetry.WebhookRequestError(
				w.name,
				httpErr,
				resp.StatusCode,
				endpoint.URL,
				endpoint.Method,
				endpoint.Auth.Type,
				false, // Not a timeout
				false, // Not cancelled
			)
		}

		return httpErr
	}

	return nil
}

// applyWebhookAuth applies authentication to the HTTP request.
func applyWebhookAuth(req *http.Request, auth *WebhookAuth) error {
	authType := strings.ToLower(auth.Type)

	switch authType {
	case authTypeNone, "":
		return nil
	case authTypeBearer:
		req.Header.Set("Authorization", "Bearer "+auth.Token)
	case authTypeBasic:
		req.SetBasicAuth(auth.User, auth.Pass)
	case authTypeCustom:
		req.Header.Set(auth.Header, auth.Value)
	default:
		return fmt.Errorf("unsupported auth type: %s", authType)
	}

	return nil
}

// SetTelemetry sets the telemetry integration for the webhook provider.
// This allows telemetry to be injected after provider creation.
func (w *WebhookProvider) SetTelemetry(telemetry *NotificationTelemetry) {
	w.telemetry = telemetry
}

// Close releases resources used by the webhook provider.
// Should be called when the provider is no longer needed.
func (w *WebhookProvider) Close() {
	if w.client != nil {
		w.client.Close()
	}
}
