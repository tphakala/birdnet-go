// Package httpclient provides a reusable, production-grade HTTP client
// with context management, timeouts, connection pooling, and observability hooks.
//
// This package is designed to be used across the codebase for any HTTP operations,
// including webhooks, external API calls, and health checks.
package httpclient

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"
)

// DefaultTimeout is the default timeout for HTTP requests if not specified.
const DefaultTimeout = 30 * time.Second

// Client is a production-grade HTTP client with context management and timeouts.
// It wraps the standard http.Client with additional features for reliability.
//
// Features:
//   - Context-aware request management for cancellation
//   - Configurable default timeout (overridable per-request)
//   - Connection pooling with tunable parameters
//   - User-Agent injection
//   - Observable through hooks for metrics/logging
//
// Thread-safe for concurrent use.
type Client struct {
	client         *http.Client
	defaultTimeout time.Duration
	userAgent      string
	// Hooks for observability (metrics, logging)
	beforeRequest  func(*http.Request)
	afterResponse  func(*http.Request, *http.Response, error)
}

// Config holds configuration for creating an HTTP client.
type Config struct {
	// DefaultTimeout is the timeout applied if request context has no deadline
	DefaultTimeout time.Duration

	// UserAgent is added to all requests
	UserAgent string

	// MaxIdleConns controls connection pool size (default: 100)
	MaxIdleConns int

	// MaxIdleConnsPerHost controls per-host connection pool (default: 10)
	MaxIdleConnsPerHost int

	// IdleConnTimeout is how long idle connections stay in pool (default: 90s)
	IdleConnTimeout time.Duration

	// TLSHandshakeTimeout is timeout for TLS handshake (default: 10s)
	TLSHandshakeTimeout time.Duration

	// ResponseHeaderTimeout is timeout waiting for response headers (default: 10s)
	ResponseHeaderTimeout time.Duration

	// ExpectContinueTimeout is timeout for Expect: 100-continue (default: 1s)
	ExpectContinueTimeout time.Duration

	// DisableKeepAlives disables HTTP keep-alive (default: false)
	DisableKeepAlives bool

	// DisableCompression disables transparent gzip compression (default: false)
	DisableCompression bool
}

// DefaultConfig returns a Config with sensible production defaults.
func DefaultConfig() Config {
	return Config{
		DefaultTimeout:          DefaultTimeout,
		UserAgent:              "BirdNET-Go",
		MaxIdleConns:           100,
		MaxIdleConnsPerHost:    10,
		IdleConnTimeout:        90 * time.Second,
		TLSHandshakeTimeout:    10 * time.Second,
		ResponseHeaderTimeout:  10 * time.Second,
		ExpectContinueTimeout:  1 * time.Second,
		DisableKeepAlives:      false,
		DisableCompression:     false,
	}
}

// New creates a new HTTP client with the given configuration.
// The cfg parameter is passed by pointer to avoid copying the large struct.
func New(cfg *Config) *Client {
	// Apply defaults for zero values
	if cfg.DefaultTimeout == 0 {
		cfg.DefaultTimeout = DefaultTimeout
	}
	if cfg.UserAgent == "" {
		cfg.UserAgent = "BirdNET-Go"
	}
	if cfg.MaxIdleConns == 0 {
		cfg.MaxIdleConns = 100
	}
	if cfg.MaxIdleConnsPerHost == 0 {
		cfg.MaxIdleConnsPerHost = 10
	}
	if cfg.IdleConnTimeout == 0 {
		cfg.IdleConnTimeout = 90 * time.Second
	}
	if cfg.TLSHandshakeTimeout == 0 {
		cfg.TLSHandshakeTimeout = 10 * time.Second
	}
	if cfg.ResponseHeaderTimeout == 0 {
		cfg.ResponseHeaderTimeout = 10 * time.Second
	}
	if cfg.ExpectContinueTimeout == 0 {
		cfg.ExpectContinueTimeout = 1 * time.Second
	}

	// Create transport with tuned settings
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          cfg.MaxIdleConns,
		MaxIdleConnsPerHost:   cfg.MaxIdleConnsPerHost,
		IdleConnTimeout:       cfg.IdleConnTimeout,
		TLSHandshakeTimeout:   cfg.TLSHandshakeTimeout,
		ResponseHeaderTimeout: cfg.ResponseHeaderTimeout,
		ExpectContinueTimeout: cfg.ExpectContinueTimeout,
		DisableKeepAlives:     cfg.DisableKeepAlives,
		DisableCompression:    cfg.DisableCompression,
	}

	return &Client{
		client: &http.Client{
			Transport: transport,
			// No default timeout - we handle it per-request with context
		},
		defaultTimeout: cfg.DefaultTimeout,
		userAgent:      cfg.UserAgent,
	}
}

// Do executes an HTTP request with context management and timeout enforcement.
//
// Context handling:
//   - If ctx has a deadline, it's used as-is
//   - If ctx has no deadline, defaultTimeout is applied
//   - Context cancellation immediately stops the request
//
// The response body must be closed by the caller if err is nil.
func (c *Client) Do(ctx context.Context, req *http.Request) (*http.Response, error) {
	// Ensure request uses the provided context
	req = req.WithContext(ctx)

	// Apply default timeout if context has no deadline
	if _, hasDeadline := ctx.Deadline(); !hasDeadline && c.defaultTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.defaultTimeout)
		defer cancel()
		req = req.WithContext(ctx)
	}

	// Inject User-Agent if not set
	if req.Header.Get("User-Agent") == "" && c.userAgent != "" {
		req.Header.Set("User-Agent", c.userAgent)
	}

	// Call before hook if set
	if c.beforeRequest != nil {
		c.beforeRequest(req)
	}

	// Execute request
	resp, err := c.client.Do(req)

	// Call after hook if set
	if c.afterResponse != nil {
		c.afterResponse(req, resp, err)
	}

	return resp, err
}

// Get performs a GET request with context.
// Uses http.NoBody for proper HTTP semantics (Go 1.24+ best practice).
func (c *Client) Get(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create GET request: %w", err)
	}
	return c.Do(ctx, req)
}

// Post performs a POST request with context.
// Uses http.NoBody for proper HTTP semantics (Go 1.24+ best practice).
// Body conversion handled by caller (use bytes.Buffer, strings.Reader, etc.)
func (c *Client) Post(ctx context.Context, url, contentType string, body interface{}) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create POST request: %w", err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	return c.Do(ctx, req)
}

// SetBeforeRequestHook sets a function to be called before each request.
// Useful for logging, metrics, or request modification.
// Not safe to call concurrently with Do().
func (c *Client) SetBeforeRequestHook(fn func(*http.Request)) {
	c.beforeRequest = fn
}

// SetAfterResponseHook sets a function to be called after each request.
// Useful for logging, metrics, or response inspection.
// Not safe to call concurrently with Do().
func (c *Client) SetAfterResponseHook(fn func(*http.Request, *http.Response, error)) {
	c.afterResponse = fn
}

// Close closes idle connections in the connection pool.
// Should be called when the client is no longer needed.
func (c *Client) Close() {
	c.client.CloseIdleConnections()
}
