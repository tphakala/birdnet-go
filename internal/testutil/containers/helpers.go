//go:build integration

package containers

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
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

// containerRuntimeHealthTimeout bounds the daemon health probe in
// containerRuntimeError so an unresponsive runtime fails fast.
const containerRuntimeHealthTimeout = 10 * time.Second

// containerRuntimeError returns nil when a Docker-compatible container runtime
// is reachable, or the reason it is not. It bounds the daemon health probe so an
// unresponsive runtime (socket present but not answering) fails fast instead of
// hanging. NewDockerProvider builds the client; Health pings the daemon and
// closes that client via an internal defer, so there is nothing to clean up here
// on either path.
func containerRuntimeError(ctx context.Context) error {
	provider, err := testcontainers.NewDockerProvider()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, containerRuntimeHealthTimeout)
	defer cancel()
	return provider.Health(ctx)
}

// SkipIfContainerRuntimeUnavailable keeps container-backed tests usable on a host
// with no Docker-compatible runtime. Call it before creating any container.
//
// The integration build tag compiles these tests, but a developer host without
// Docker (for example a rootless Podman setup with no DOCKER_HOST) has no
// provider to start containers with, so testcontainers fails when it tries. There
// it skips, so the tests that call it stay green locally while still running
// wherever a runtime is present.
//
// On CI a runtime is promised, so its absence is a real breakage rather than an
// expected environment: when the CI environment variable is set, a missing or
// unhealthy runtime fails loudly instead of skipping green and masking the
// outage. Suites that build their container in TestMain (which has no
// *testing.T) use SkipTestMainIfContainerRuntimeUnavailable instead.
func SkipIfContainerRuntimeUnavailable(tb testing.TB) {
	tb.Helper()
	err := containerRuntimeError(tb.Context())
	if err == nil {
		return
	}
	// GitHub Actions and most CI systems set CI; there a container runtime is
	// expected, so surface its absence as a failure rather than a silent skip.
	if os.Getenv("CI") != "" {
		tb.Fatalf("container runtime required on CI but unavailable: %v", err)
	}
	tb.Skipf("skipping: container runtime unavailable (%v)", err)
}

// SkipTestMainIfContainerRuntimeUnavailable is the TestMain counterpart of
// SkipIfContainerRuntimeUnavailable, for suites that build their container in
// TestMain and therefore have no *testing.T to skip with. It reports whether the
// suite should exit early because no container runtime is reachable, and the exit
// code to use. Call it before creating any container and, when skip is true,
// os.Exit(code) (or return code from a TestMain helper) without running the
// suite:
//
//	func TestMain(m *testing.M) {
//		if code, skip := containers.SkipTestMainIfContainerRuntimeUnavailable(); skip {
//			os.Exit(code)
//		}
//		// ... build container, m.Run(), cleanup ...
//	}
//
// In local dev a missing runtime returns (0, true) so the package is skipped
// cleanly; on CI (the CI env var is set) it logs the reason and returns
// (1, true) so a broken runtime fails loudly instead of passing green.
func SkipTestMainIfContainerRuntimeUnavailable() (code int, skip bool) {
	err := containerRuntimeError(context.Background())
	if err == nil {
		return 0, false
	}
	if os.Getenv("CI") != "" {
		fmt.Fprintf(os.Stderr, "container runtime required on CI but unavailable: %v\n", err)
		return 1, true
	}
	fmt.Fprintf(os.Stderr, "skipping: container runtime unavailable (%v)\n", err)
	return 0, true
}
