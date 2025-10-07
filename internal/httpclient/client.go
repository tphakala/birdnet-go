// Package httpclient provides a reusable, production-grade HTTP client
// with context management, timeouts, connection pooling, and observability hooks.
//
// This package is designed to be used across the codebase for any HTTP operations,
// including webhooks, external API calls, and health checks.
package httpclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	// DefaultTimeout is the default timeout for HTTP requests if not specified.
	DefaultTimeout = 30 * time.Second

	// Default connection pool settings
	defaultMaxIdleConns        = 100
	defaultMaxIdleConnsPerHost = 10
	defaultIdleConnTimeout     = 90 * time.Second

	// Default timeouts for various HTTP operations
	defaultTLSHandshakeTimeout    = 10 * time.Second
	defaultResponseHeaderTimeout  = 10 * time.Second
	defaultExpectContinueTimeout  = 1 * time.Second
	defaultDialTimeout            = 30 * time.Second
	defaultDialKeepAlive          = 30 * time.Second

	// Default User-Agent
	defaultUserAgent = "BirdNET-Go"
)

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
	// Protected by hookMu for concurrent access safety
	hookMu        sync.RWMutex
	beforeRequest func(*http.Request)
	afterResponse func(*http.Request, *http.Response, error)
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
		UserAgent:              defaultUserAgent,
		MaxIdleConns:           defaultMaxIdleConns,
		MaxIdleConnsPerHost:    defaultMaxIdleConnsPerHost,
		IdleConnTimeout:        defaultIdleConnTimeout,
		TLSHandshakeTimeout:    defaultTLSHandshakeTimeout,
		ResponseHeaderTimeout:  defaultResponseHeaderTimeout,
		ExpectContinueTimeout:  defaultExpectContinueTimeout,
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
		cfg.UserAgent = defaultUserAgent
	}
	if cfg.MaxIdleConns == 0 {
		cfg.MaxIdleConns = defaultMaxIdleConns
	}
	if cfg.MaxIdleConnsPerHost == 0 {
		cfg.MaxIdleConnsPerHost = defaultMaxIdleConnsPerHost
	}
	if cfg.IdleConnTimeout == 0 {
		cfg.IdleConnTimeout = defaultIdleConnTimeout
	}
	if cfg.TLSHandshakeTimeout == 0 {
		cfg.TLSHandshakeTimeout = defaultTLSHandshakeTimeout
	}
	if cfg.ResponseHeaderTimeout == 0 {
		cfg.ResponseHeaderTimeout = defaultResponseHeaderTimeout
	}
	if cfg.ExpectContinueTimeout == 0 {
		cfg.ExpectContinueTimeout = defaultExpectContinueTimeout
	}

	// Create transport with tuned settings
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   defaultDialTimeout,
			KeepAlive: defaultDialKeepAlive,
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

	// Call before hook if set (read lock for concurrent safety)
	c.hookMu.RLock()
	beforeHook := c.beforeRequest
	c.hookMu.RUnlock()
	if beforeHook != nil {
		beforeHook(req)
	}

	// Execute request
	resp, err := c.client.Do(req)

	// Call after hook if set (read lock for concurrent safety)
	c.hookMu.RLock()
	afterHook := c.afterResponse
	c.hookMu.RUnlock()
	if afterHook != nil {
		afterHook(req, resp, err)
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
// Handles multiple body types:
//   - nil: uses http.NoBody
//   - io.Reader: uses directly
//   - []byte or string: wraps in appropriate reader
//   - other: marshals to JSON
func (c *Client) Post(ctx context.Context, url, contentType string, body interface{}) (*http.Response, error) {
	var bodyReader io.Reader = http.NoBody
	var shouldSetJSON bool

	if body != nil {
		switch v := body.(type) {
		case io.Reader:
			bodyReader = v
		case []byte:
			bodyReader = bytes.NewReader(v)
		case string:
			bodyReader = strings.NewReader(v)
		default:
			// Marshal to JSON
			data, err := json.Marshal(v)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal body: %w", err)
			}
			bodyReader = bytes.NewReader(data)
			shouldSetJSON = true
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("failed to create POST request: %w", err)
	}

	// Set content type
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	} else if shouldSetJSON {
		req.Header.Set("Content-Type", "application/json")
	}

	return c.Do(ctx, req)
}

// SetBeforeRequestHook sets a function to be called before each request.
// Useful for logging, metrics, or request modification.
// Safe to call concurrently with Do() and other hook setters.
func (c *Client) SetBeforeRequestHook(fn func(*http.Request)) {
	c.hookMu.Lock()
	defer c.hookMu.Unlock()
	c.beforeRequest = fn
}

// SetAfterResponseHook sets a function to be called after each request.
// Useful for logging, metrics, or response inspection.
// Safe to call concurrently with Do() and other hook setters.
func (c *Client) SetAfterResponseHook(fn func(*http.Request, *http.Response, error)) {
	c.hookMu.Lock()
	defer c.hookMu.Unlock()
	c.afterResponse = fn
}

// Close closes idle connections in the connection pool.
// Should be called when the client is no longer needed.
func (c *Client) Close() {
	c.client.CloseIdleConnections()
}
