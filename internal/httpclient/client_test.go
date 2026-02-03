package httpclient

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNew(t *testing.T) {
	t.Run("default config", func(t *testing.T) {
		cfg := DefaultConfig()
		client := New(&cfg)

		require.NotNil(t, client, "expected non-nil client")
		assert.Equal(t, DefaultTimeout, client.defaultTimeout, "expected default timeout")
		assert.Equal(t, defaultUserAgent, client.userAgent, "expected default user agent")
	})

	t.Run("custom config", func(t *testing.T) {
		cfg := Config{
			DefaultTimeout: 5 * time.Second,
			UserAgent:      "TestAgent/1.0",
		}
		client := New(&cfg)

		assert.Equal(t, 5*time.Second, client.defaultTimeout, "expected timeout 5s")
		assert.Equal(t, "TestAgent/1.0", client.userAgent, "expected user agent 'TestAgent/1.0'")
	})

	t.Run("zero values use defaults", func(t *testing.T) {
		cfg := Config{} // All zero values
		client := New(&cfg)

		assert.Equal(t, DefaultTimeout, client.defaultTimeout, "expected default timeout")
		assert.NotEmpty(t, client.userAgent, "expected non-empty user agent")
	})
}

func TestDo_BasicRequest(t *testing.T) {
	server := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method, "expected GET method")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("success"))
	})

	client := newTestClient(t)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL, http.NoBody)
	require.NoError(t, err, "failed to create request")

	resp, err := client.Do(t.Context(), req)
	require.NoError(t, err, "request failed")
	defer closeResponseBody(t, resp)

	assert.Equal(t, http.StatusOK, resp.StatusCode, "expected status 200")

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err, "failed to read body")
	assert.Equal(t, "success", string(body), "expected body 'success'")
}

func TestDo_UserAgent(t *testing.T) {
	receivedUA := ""
	server := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		receivedUA = r.Header.Get("User-Agent")
		w.WriteHeader(http.StatusOK)
	})

	cfg := Config{UserAgent: "CustomAgent/2.0"}
	client := newTestClientWithConfig(t, &cfg)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL, http.NoBody)
	require.NoError(t, err, "failed to create request")

	resp, err := client.Do(t.Context(), req)
	require.NoError(t, err, "request failed")
	defer closeResponseBody(t, resp)

	assert.Equal(t, "CustomAgent/2.0", receivedUA, "expected User-Agent 'CustomAgent/2.0'")
}

func TestDo_ContextCancellation(t *testing.T) {
	server := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
		w.WriteHeader(http.StatusOK)
	})

	client := newTestClient(t)

	ctx, cancel := context.WithCancel(t.Context())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, http.NoBody)
	require.NoError(t, err, "failed to create request")

	// Cancel immediately
	cancel()

	resp, err := client.Do(ctx, req)
	defer closeResponseBody(t, resp)

	require.Error(t, err, "expected error from cancelled context")
	assert.ErrorIs(t, err, context.Canceled, "expected context.Canceled error")
}

func TestDo_ContextTimeout(t *testing.T) {
	server := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(500 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	})

	client := newTestClient(t)

	// Create context with very short timeout
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, http.NoBody)
	require.NoError(t, err, "failed to create request")

	resp, err := client.Do(ctx, req)
	defer closeResponseBody(t, resp)

	require.Error(t, err, "expected timeout error")
	assert.ErrorIs(t, err, context.DeadlineExceeded, "expected context.DeadlineExceeded error")
}

func TestDo_DefaultTimeout(t *testing.T) {
	server := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	})

	// Create client with very short default timeout
	cfg := Config{DefaultTimeout: 50 * time.Millisecond}
	client := newTestClientWithConfig(t, &cfg)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL, http.NoBody)
	require.NoError(t, err, "failed to create request")

	// Context has no deadline, so default timeout should apply
	resp, err := client.Do(t.Context(), req)
	defer closeResponseBody(t, resp)

	require.Error(t, err, "expected timeout error")
	assert.ErrorIs(t, err, context.DeadlineExceeded, "expected context.DeadlineExceeded error")
}

func TestDo_ContextTimeoutOverridesDefault(t *testing.T) {
	server := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(20 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	})

	// Client with very short default timeout
	cfg := Config{DefaultTimeout: 10 * time.Millisecond}
	client := newTestClientWithConfig(t, &cfg)

	// But context has longer timeout - should succeed
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, http.NoBody)
	require.NoError(t, err, "failed to create request")

	resp, err := client.Do(ctx, req)
	require.NoError(t, err, "request should succeed with context timeout")
	defer closeResponseBody(t, resp)

	assert.Equal(t, http.StatusOK, resp.StatusCode, "expected status 200")
}

func TestDo_ConcurrentRequests(t *testing.T) {
	var requestCount atomic.Int32
	server := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		requestCount.Add(1)
		w.WriteHeader(http.StatusOK)
	})

	client := newTestClient(t)

	// Launch 50 concurrent requests
	concurrency := 50
	var wg sync.WaitGroup
	wg.Add(concurrency)

	errChan := make(chan error, concurrency)

	for range concurrency {
		go func() {
			defer wg.Done()

			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL, http.NoBody)
			if err != nil {
				errChan <- err
				return
			}

			resp, err := client.Do(t.Context(), req)
			if err != nil {
				errChan <- err
				return
			}
			defer func() {
				if cerr := resp.Body.Close(); cerr != nil {
					errChan <- fmt.Errorf("failed to close response body: %w", cerr)
				}
			}()

			if resp.StatusCode != http.StatusOK {
				errChan <- fmt.Errorf("expected status 200, got %d", resp.StatusCode)
			}
		}()
	}

	wg.Wait()
	close(errChan)

	// Check for errors
	for err := range errChan {
		require.NoError(t, err, "concurrent request failed")
	}

	// Verify all requests were received
	count := requestCount.Load()
	assert.Equal(t, int32(concurrency), count, "expected all requests to be received")
}

func TestDo_Hooks(t *testing.T) {
	server := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	client := newTestClient(t)

	var beforeCalled, afterCalled bool
	var capturedResp *http.Response
	var capturedErr error

	client.SetBeforeRequestHook(func(r *http.Request) {
		beforeCalled = true
		assert.Equal(t, server.URL, r.URL.String(), "before hook: unexpected URL")
	})

	client.SetAfterResponseHook(func(r *http.Request, resp *http.Response, err error) {
		afterCalled = true
		capturedResp = resp
		capturedErr = err
	})

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL, http.NoBody)
	require.NoError(t, err, "failed to create request")

	resp, err := client.Do(t.Context(), req)
	require.NoError(t, err, "request failed")
	defer closeResponseBody(t, resp)

	assert.True(t, beforeCalled, "before hook was not called")
	assert.True(t, afterCalled, "after hook was not called")
	assert.NotNil(t, capturedResp, "after hook did not capture response")
	assert.NoError(t, capturedErr, "after hook captured unexpected error")
}

func TestGet(t *testing.T) {
	server := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method, "expected GET method")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("get success"))
	})

	client := newTestClient(t)

	resp, err := client.Get(t.Context(), server.URL)
	require.NoError(t, err, "GET failed")
	defer closeResponseBody(t, resp)

	assert.Equal(t, http.StatusOK, resp.StatusCode, "expected status 200")

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err, "failed to read body")
	assert.Equal(t, "get success", string(body), "expected body 'get success'")
}

func TestPost(t *testing.T) {
	server := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method, "expected POST method")
		ct := r.Header.Get("Content-Type")
		assert.Equal(t, "application/json", ct, "expected Content-Type application/json")
		w.WriteHeader(http.StatusCreated)
	})

	client := newTestClient(t)

	resp, err := client.Post(t.Context(), server.URL, "application/json", nil)
	require.NoError(t, err, "POST failed")
	defer closeResponseBody(t, resp)

	assert.Equal(t, http.StatusCreated, resp.StatusCode, "expected status 201")
}

func TestClose(t *testing.T) {
	cfg := DefaultConfig()
	client := New(&cfg)

	// Close should not panic
	client.Close()

	// Multiple closes should be safe
	client.Close()
}
