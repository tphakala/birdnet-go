//nolint:dupl // Table-driven tests have similar structures by design
package notification

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// Test configuration constants to avoid magic numbers.
const (
	testMaxRetries     = 3
	testRetryDelay     = 10 * time.Millisecond
	testDefaultTimeout = 200 * time.Millisecond
)

// MockTelegramServer simulates Telegram API for testing notification delivery.
// It tracks how many messages are received to detect duplicate sends.
//
// This mock is designed for future integration tests that need to verify
// actual HTTP behavior with Shoutrrr providers. Current unit tests use
// fakeProvider for faster, more isolated testing.
type MockTelegramServer struct {
	server        *httptest.Server
	messageCount  atomic.Int32
	responseDelay time.Duration
	failAfterSend bool // If true, return error after actually receiving the message

	mu       sync.Mutex // Protects messages slice
	messages []string
}

// NewMockTelegramServer creates a mock Telegram API server.
func NewMockTelegramServer() *MockTelegramServer {
	m := &MockTelegramServer{}
	m.server = httptest.NewServer(http.HandlerFunc(m.handleRequest))
	return m
}

// SetResponseDelay configures delay before responding (simulates slow network).
func (m *MockTelegramServer) SetResponseDelay(d time.Duration) {
	m.responseDelay = d
}

// SetFailAfterSend makes the server return an error after receiving the message.
// This simulates the bug where message is delivered but response fails.
func (m *MockTelegramServer) SetFailAfterSend(fail bool) {
	m.failAfterSend = fail
}

// MessageCount returns how many messages were received.
func (m *MockTelegramServer) MessageCount() int {
	return int(m.messageCount.Load())
}

// Messages returns a copy of all received messages (thread-safe).
func (m *MockTelegramServer) Messages() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	// Return a copy to avoid race conditions with caller
	result := make([]string, len(m.messages))
	copy(result, m.messages)
	return result
}

// URL returns the server URL for use in Shoutrrr configuration.
func (m *MockTelegramServer) URL() string {
	return m.server.URL
}

// Close shuts down the mock server.
func (m *MockTelegramServer) Close() {
	m.server.Close()
}

func (m *MockTelegramServer) handleRequest(w http.ResponseWriter, r *http.Request) {
	// Read and store the message (this is the "message delivered" moment)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "failed to read request body", http.StatusInternalServerError)
		return
	}

	m.messageCount.Add(1)

	// Thread-safe append to messages slice
	m.mu.Lock()
	m.messages = append(m.messages, string(body))
	m.mu.Unlock()

	// Simulate response delay
	if m.responseDelay > 0 {
		time.Sleep(m.responseDelay)
	}

	// Return error after message was received (simulates the bug)
	if m.failAfterSend {
		w.WriteHeader(http.StatusGatewayTimeout)
		return
	}

	// Normal success response
	w.Header().Set("Content-Type", "application/json")
	response := map[string]any{
		"ok": true,
		"result": map[string]any{
			"message_id": m.messageCount.Load(),
		},
	}
	_ = json.NewEncoder(w).Encode(response)
}

// retryTestCase defines a test case for retry behavior.
type retryTestCase struct {
	name          string
	sendFunc      func(count int32) error // Returns error based on attempt count
	expectedCount int                     // Expected number of send attempts
	description   string                  // Assertion message
}

// runRetryTest executes a retry test case with standard dispatcher configuration.
func runRetryTest(t *testing.T, tc retryTestCase) {
	t.Helper()

	var sendAttempts atomic.Int32

	fp := &fakeProvider{
		name:    "test-" + tc.name,
		enabled: true,
		types:   map[Type]bool{TypeDetection: true},
		recvCh:  make(chan *Notification, 10),
		sendFunc: func(_ context.Context, _ *Notification) error {
			count := sendAttempts.Add(1)
			return tc.sendFunc(count)
		},
	}

	d := &pushDispatcher{
		providers: []enhancedProvider{{
			prov:   fp,
			filter: conf.PushFilterConfig{},
			name:   fp.name,
		}},
		log:            GetLogger(),
		enabled:        true,
		maxRetries:     testMaxRetries,
		retryDelay:     testRetryDelay,
		defaultTimeout: testDefaultTimeout,
	}

	notif := &Notification{
		ID:       "test-" + tc.name,
		Type:     TypeDetection,
		Title:    "Test",
		Message:  "Message",
		Priority: PriorityMedium,
	}

	ctx := t.Context()
	ep := &d.providers[0]
	d.retryLoop(ctx, notif, ep)

	assert.Equal(t, tc.expectedCount, int(sendAttempts.Load()), tc.description)
}

// TestRetryLoop_TimeoutErrors tests that timeout errors are NOT retried.
// This addresses issue #1706 where Telegram notifications are sent 4 times.
func TestRetryLoop_TimeoutErrors(t *testing.T) {
	t.Parallel()

	tests := []retryTestCase{
		{
			name: "context_deadline_exceeded",
			sendFunc: func(_ int32) error {
				return context.DeadlineExceeded
			},
			expectedCount: 1,
			description:   "context.DeadlineExceeded should not be retried",
		},
		{
			name: "router_timeout",
			sendFunc: func(_ int32) error {
				// Matches shoutrrr/pkg/router/router.go:143
				return fmt.Errorf("failed to send: timed out: using telegram")
			},
			expectedCount: 1,
			description:   "Shoutrrr router timeout should not be retried",
		},
		{
			name: "gateway_timeout_504",
			sendFunc: func(_ int32) error {
				return fmt.Errorf("got unexpected HTTP status: 504 Gateway Time-out")
			},
			expectedCount: 1,
			description:   "HTTP 504 Gateway Timeout should not be retried",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			runRetryTest(t, tc)
		})
	}
}

// TestRetryLoop_RetryableErrors tests that real failures ARE retried.
func TestRetryLoop_RetryableErrors(t *testing.T) {
	t.Parallel()

	tests := []retryTestCase{
		{
			name: "connection_refused_then_success",
			sendFunc: func(count int32) error {
				if count < 3 {
					return fmt.Errorf("dial tcp 127.0.0.1:443: connect: connection refused")
				}
				return nil
			},
			expectedCount: 3,
			description:   "connection refused should be retried until success",
		},
		{
			name: "network_error_then_success",
			sendFunc: func(count int32) error {
				if count < 3 {
					return fmt.Errorf("network error: connection refused")
				}
				return nil
			},
			expectedCount: 3,
			description:   "network errors should be retried until success",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			runRetryTest(t, tc)
		})
	}
}

// TestIsTimeoutError tests the isTimeoutError helper function.
func TestIsTimeoutError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		// Timeout errors (should NOT retry)
		{"nil_error", nil, false},
		{"context_deadline_exceeded", context.DeadlineExceeded, true},
		{"context_canceled", context.Canceled, true},
		{"wrapped_deadline_exceeded", fmt.Errorf("request failed: %w", context.DeadlineExceeded), true},
		{"wrapped_canceled", fmt.Errorf("request failed: %w", context.Canceled), true},
		{"shoutrrr_router_timeout", fmt.Errorf("failed to send: timed out: using telegram"), true},
		{"generic_timeout", fmt.Errorf("operation timeout"), true},
		{"http_504_gateway_timeout", fmt.Errorf("got unexpected HTTP status: 504 Gateway Timeout"), true},
		{"http_504_gateway_time_out", fmt.Errorf("got unexpected HTTP status: 504 Gateway Time-out"), true},
		{"deadline_exceeded_string", fmt.Errorf("deadline exceeded while waiting for response"), true},

		// Non-timeout errors (SHOULD retry)
		{"connection_refused", fmt.Errorf("dial tcp 127.0.0.1:443: connect: connection refused"), false},
		{"dns_lookup_failed", fmt.Errorf("lookup api.telegram.org: no such host"), false},
		{"http_500_internal_error", fmt.Errorf("got unexpected HTTP status: 500 Internal Server Error"), false},
		{"http_502_bad_gateway", fmt.Errorf("got unexpected HTTP status: 502 Bad Gateway"), false},
		{"http_503_service_unavailable", fmt.Errorf("got unexpected HTTP status: 503 Service Unavailable"), false},
		{"generic_network_error", fmt.Errorf("network error: connection reset by peer"), false},
		{"telegram_rate_limit", fmt.Errorf("telegram: Too Many Requests: retry after 30"), false},

		// False positive prevention - should NOT match as timeout
		{"port_number_5040", fmt.Errorf("dial tcp 127.0.0.1:5040: connect: connection refused"), false},
		{"error_code_504x", fmt.Errorf("error code 5041: invalid request"), false},
		{"gateway_timestamp", fmt.Errorf("gateway timestamp mismatch: expected 2024-01-01"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result := isTimeoutError(tt.err)
			assert.Equal(t, tt.expected, result,
				"isTimeoutError(%v) = %v, want %v", tt.err, result, tt.expected)
		})
	}
}

// TestMockTelegramServer_ThreadSafety verifies the mock server handles concurrent requests safely.
func TestMockTelegramServer_ThreadSafety(t *testing.T) {
	t.Parallel()

	server := NewMockTelegramServer()
	defer server.Close()

	const numRequests = 10
	var wg sync.WaitGroup
	wg.Add(numRequests)

	// Send concurrent requests
	for i := range numRequests {
		go func(id int) {
			defer wg.Done()
			resp, err := http.Post(server.URL(), "application/json",
				http.NoBody)
			if err != nil {
				t.Errorf("request %d failed: %v", id, err)
				return
			}
			_ = resp.Body.Close()
		}(i)
	}

	wg.Wait()

	// Verify all messages were received
	assert.Equal(t, numRequests, server.MessageCount(),
		"all concurrent requests should be counted")
	assert.Len(t, server.Messages(), numRequests,
		"all concurrent messages should be stored")
}
