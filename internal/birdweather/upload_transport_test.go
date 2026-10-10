package birdweather

import (
	"net/http"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// TestNewUploadHTTPClient_DedicatedTransport verifies the upload client uses its
// own transport (cloned from the default) rather than sharing the global
// http.DefaultTransport pool, so closing its idle connections cannot disturb
// other components and its HTTP/2 health-check settings apply only here.
func TestNewUploadHTTPClient_DedicatedTransport(t *testing.T) {
	t.Parallel()

	client := newUploadHTTPClient()
	require.NotNil(t, client)
	assert.Equal(t, httpClientTimeout, client.Timeout)

	transport, ok := client.Transport.(*http.Transport)
	require.True(t, ok, "upload client must use a *http.Transport")
	require.NotNil(t, transport)

	// The HTTP/2 health-check settings now live on the exported *http.HTTP2Config
	// (stdlib, Go 1.24+), so assert them directly. A regression here (e.g. the ping
	// config not being applied) would silently disable the connection-reuse-race fix.
	require.NotNil(t, transport.HTTP2, "upload transport must configure HTTP/2 health checks")
	assert.Equal(t, http2SendPingTimeout, transport.HTTP2.SendPingTimeout,
		"idle PING must be sent after http2SendPingTimeout")
	assert.Equal(t, http2PingTimeout, transport.HTTP2.PingTimeout,
		"connection must be dropped if no PONG arrives within http2PingTimeout")

	defaultTransport, ok := http.DefaultTransport.(*http.Transport)
	require.True(t, ok)
	assert.NotSame(t, defaultTransport, transport,
		"upload client must own a dedicated transport, not the shared DefaultTransport")
}

// TestBwClientClose_KeepsHTTPClient verifies Close() does not nil out the HTTP
// client. In-flight uploads read b.HTTPClient without the processor's mutex, so
// nil-ing it would race and risk a nil dereference.
func TestBwClientClose_KeepsHTTPClient(t *testing.T) {
	t.Parallel()

	b := &BwClient{
		Settings:   &conf.Settings{},
		HTTPClient: newUploadHTTPClient(),
	}

	b.Close()

	assert.NotNil(t, b.HTTPClient, "Close() must not nil HTTPClient (in-flight uploads read it lock-free)")
}

// TestBwClientClose_ConcurrentReadNoRace exercises the exact access pattern of
// the original bug: an in-flight upload reading b.HTTPClient concurrently with a
// reconfigure calling Close(). Run under -race; the old code (b.HTTPClient = nil
// inside Close) tripped the detector here.
func TestBwClientClose_ConcurrentReadNoRace(t *testing.T) {
	t.Parallel()

	b := &BwClient{
		Settings:   &conf.Settings{},
		HTTPClient: newUploadHTTPClient(),
	}

	var wg sync.WaitGroup
	const (
		concurrentHTTPClientReaders = 8
		httpClientReadIterations    = 200
	)
	// Readers mimic upload goroutines that obtained *BwClient and dereference
	// its HTTPClient field without holding any lock.
	for range concurrentHTTPClientReaders {
		wg.Go(func() {
			for range httpClientReadIterations {
				_ = b.HTTPClient
			}
		})
	}
	// Concurrent reconfigure/disconnect closing the same client.
	wg.Go(func() {
		b.Close()
	})

	wg.Wait()
	assert.NotNil(t, b.HTTPClient)
}
