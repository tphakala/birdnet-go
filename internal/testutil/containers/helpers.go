//go:build integration

package containers

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"time"
)

// WaitForHTTP waits for an HTTP endpoint to respond with a 200 status code.
// It retries every 500ms until the endpoint is ready or the timeout is reached.
func WaitForHTTP(url string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	client := &http.Client{
		Timeout: 2 * time.Second,
	}

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout waiting for HTTP endpoint %s: %w", url, ctx.Err())
		case <-ticker.C:
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
			if err != nil {
				continue
			}

			resp, err := client.Do(req)
			if err != nil {
				continue
			}
			_ = resp.Body.Close()

			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
	}
}

// WaitForTCP waits for a TCP port to be available.
// It retries every 500ms until the port is open or the timeout is reached.
func WaitForTCP(host string, port int, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	address := net.JoinHostPort(host, strconv.Itoa(port))
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout waiting for TCP port %s: %w", address, ctx.Err())
		case <-ticker.C:
			conn, err := net.DialTimeout("tcp", address, 2*time.Second)
			if err == nil {
				_ = conn.Close()
				return nil
			}
		}
	}
}

// RetryWithBackoff retries a function with exponential backoff.
// It starts with initialDelay and doubles it on each retry up to maxDelay.
// Returns the last error if maxAttempts is reached.
func RetryWithBackoff(
	ctx context.Context,
	maxAttempts int,
	initialDelay time.Duration,
	maxDelay time.Duration,
	fn func() error,
) error {
	var lastErr error
	delay := initialDelay

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		lastErr = fn()
		if lastErr == nil {
			return nil
		}

		if attempt == maxAttempts {
			break
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("retry cancelled: %w (last error: %w)", ctx.Err(), lastErr)
		case <-time.After(delay):
			// Double the delay for next attempt, up to maxDelay
			delay *= 2
			if delay > maxDelay {
				delay = maxDelay
			}
		}
	}

	return fmt.Errorf("max attempts (%d) reached: %w", maxAttempts, lastErr)
}

// PortIsAvailable checks if a port is available for binding on the host.
func PortIsAvailable(port int) bool {
	address := fmt.Sprintf(":%d", port)
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return false
	}
	_ = listener.Close()
	return true
}

// GetFreePort finds and returns an available port on the host.
// It does this by binding to port 0 and letting the OS assign a port.
func GetFreePort() (int, error) {
	//nolint:gosec // G102: Binding to :0 is intentional for finding free ports
	listener, err := net.Listen("tcp", ":0")
	if err != nil {
		return 0, err
	}
	defer func() { _ = listener.Close() }()

	addr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		return 0, fmt.Errorf("listener address is not TCP: %T", listener.Addr())
	}
	return addr.Port, nil
}
