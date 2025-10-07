package httpclient

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestNew(t *testing.T) {
	t.Run("default config", func(t *testing.T) {
		cfg := DefaultConfig()
		client := New(&cfg)

		if client == nil {
			t.Fatal("expected non-nil client")
		}
		if client.defaultTimeout != DefaultTimeout {
			t.Errorf("expected timeout %v, got %v", DefaultTimeout, client.defaultTimeout)
		}
		if client.userAgent != defaultUserAgent {
			t.Errorf("expected user agent %q, got %q", defaultUserAgent, client.userAgent)
		}
	})

	t.Run("custom config", func(t *testing.T) {
		cfg := Config{
			DefaultTimeout: 5 * time.Second,
			UserAgent:      "TestAgent/1.0",
		}
		client := New(&cfg)

		if client.defaultTimeout != 5*time.Second {
			t.Errorf("expected timeout 5s, got %v", client.defaultTimeout)
		}
		if client.userAgent != "TestAgent/1.0" {
			t.Errorf("expected user agent 'TestAgent/1.0', got %q", client.userAgent)
		}
	})

	t.Run("zero values use defaults", func(t *testing.T) {
		cfg := Config{} // All zero values
		client := New(&cfg)

		if client.defaultTimeout != DefaultTimeout {
			t.Errorf("expected default timeout, got %v", client.defaultTimeout)
		}
		if client.userAgent == "" {
			t.Error("expected non-empty user agent")
		}
	})
}

func TestDo_BasicRequest(t *testing.T) {
	// Create test server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("success"))
	}))
	defer server.Close()

	cfg := DefaultConfig()
	client := New(&cfg)
	defer client.Close()

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL, http.NoBody)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}

	resp, err := client.Do(context.Background(), req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil {
			t.Logf("failed to close response body: %v", cerr)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed to read body: %v", err)
	}
	if string(body) != "success" {
		t.Errorf("expected body 'success', got %q", string(body))
	}
}

func TestDo_UserAgent(t *testing.T) {
	receivedUA := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedUA = r.Header.Get("User-Agent")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg := Config{UserAgent: "CustomAgent/2.0"}
	client := New(&cfg)
	defer client.Close()

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL, http.NoBody)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}

	resp, err := client.Do(context.Background(), req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil {
			t.Logf("failed to close response body: %v", cerr)
		}
	}()

	if receivedUA != "CustomAgent/2.0" {
		t.Errorf("expected User-Agent 'CustomAgent/2.0', got %q", receivedUA)
	}
}

func TestDo_ContextCancellation(t *testing.T) {
	// Server that delays response
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg := DefaultConfig()
	client := New(&cfg)
	defer client.Close()

	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, http.NoBody)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}

	// Cancel immediately
	cancel()

	resp, err := client.Do(ctx, req)
	if resp != nil && resp.Body != nil {
		defer func() {
			if cerr := resp.Body.Close(); cerr != nil {
				t.Logf("failed to close response body: %v", cerr)
			}
		}()
	}
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}

	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled error, got: %v", err)
	}
}

func TestDo_ContextTimeout(t *testing.T) {
	// Server that delays response
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(500 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg := DefaultConfig()
	client := New(&cfg)
	defer client.Close()

	// Create context with very short timeout
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, http.NoBody)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}

	resp, err := client.Do(ctx, req)
	if resp != nil && resp.Body != nil {
		defer func() {
			if cerr := resp.Body.Close(); cerr != nil {
				t.Logf("failed to close response body: %v", cerr)
			}
		}()
	}
	if err == nil {
		t.Fatal("expected timeout error")
	}

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected context.DeadlineExceeded error, got: %v", err)
	}
}

func TestDo_DefaultTimeout(t *testing.T) {
	// Server that delays response
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	// Create client with very short default timeout
	cfg := Config{DefaultTimeout: 50 * time.Millisecond}
	client := New(&cfg)
	defer client.Close()

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL, http.NoBody)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}

	// Context has no deadline, so default timeout should apply
	resp, err := client.Do(context.Background(), req)
	if resp != nil && resp.Body != nil {
		defer func() {
			if cerr := resp.Body.Close(); cerr != nil {
				t.Logf("failed to close response body: %v", cerr)
			}
		}()
	}
	if err == nil {
		t.Fatal("expected timeout error")
	}

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected context.DeadlineExceeded error, got: %v", err)
	}
}

func TestDo_ContextTimeoutOverridesDefault(t *testing.T) {
	// Server with minimal delay
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(20 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	// Client with very short default timeout
	cfg := Config{DefaultTimeout: 10 * time.Millisecond}
	client := New(&cfg)
	defer client.Close()

	// But context has longer timeout - should succeed
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, http.NoBody)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}

	resp, err := client.Do(ctx, req)
	if err != nil {
		t.Fatalf("request should succeed with context timeout: %v", err)
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil {
			t.Logf("failed to close response body: %v", cerr)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}
}

func TestDo_ConcurrentRequests(t *testing.T) {
	var requestCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg := DefaultConfig()
	client := New(&cfg)
	defer client.Close()

	// Launch 50 concurrent requests
	concurrency := 50
	var wg sync.WaitGroup
	wg.Add(concurrency)

	errChan := make(chan error, concurrency)

	for i := 0; i < concurrency; i++ {
		go func() {
			defer wg.Done()

			req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL, http.NoBody)
			if err != nil {
				errChan <- err
				return
			}

			resp, err := client.Do(context.Background(), req)
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
		t.Errorf("concurrent request failed: %v", err)
	}

	// Verify all requests were received
	if count := requestCount.Load(); count != int32(concurrency) {
		t.Errorf("expected %d requests, got %d", concurrency, count)
	}
}

func TestDo_Hooks(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg := DefaultConfig()
	client := New(&cfg)
	defer client.Close()

	var beforeCalled, afterCalled bool
	var capturedResp *http.Response
	var capturedErr error

	client.SetBeforeRequestHook(func(r *http.Request) {
		beforeCalled = true
		if r.URL.String() != server.URL {
			t.Errorf("before hook: unexpected URL %s", r.URL.String())
		}
	})

	client.SetAfterResponseHook(func(r *http.Request, resp *http.Response, err error) {
		afterCalled = true
		capturedResp = resp
		capturedErr = err
	})

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL, http.NoBody)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}

	resp, err := client.Do(context.Background(), req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil {
			t.Logf("failed to close response body: %v", cerr)
		}
	}()

	if !beforeCalled {
		t.Error("before hook was not called")
	}
	if !afterCalled {
		t.Error("after hook was not called")
	}
	if capturedResp == nil {
		t.Error("after hook did not capture response")
	}
	if capturedErr != nil {
		t.Errorf("after hook captured unexpected error: %v", capturedErr)
	}
}

func TestGet(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("get success"))
	}))
	defer server.Close()

	cfg := DefaultConfig()
	client := New(&cfg)
	defer client.Close()

	resp, err := client.Get(context.Background(), server.URL)
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil {
			t.Logf("failed to close response body: %v", cerr)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed to read body: %v", err)
	}
	if string(body) != "get success" {
		t.Errorf("expected body 'get success', got %q", string(body))
	}
}

func TestPost(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("expected Content-Type application/json, got %q", ct)
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()

	cfg := DefaultConfig()
	client := New(&cfg)
	defer client.Close()

	resp, err := client.Post(context.Background(), server.URL, "application/json", nil)
	if err != nil {
		t.Fatalf("POST failed: %v", err)
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil {
			t.Logf("failed to close response body: %v", cerr)
		}
	}()

	if resp.StatusCode != http.StatusCreated {
		t.Errorf("expected status 201, got %d", resp.StatusCode)
	}
}

func TestClose(t *testing.T) {
	cfg := DefaultConfig()
	client := New(&cfg)

	// Close should not panic
	client.Close()

	// Multiple closes should be safe
	client.Close()
}
