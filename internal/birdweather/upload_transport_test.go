package birdweather

import (
	"net/http"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/httpclient"
	"golang.org/x/net/http2"
)

// TestUploadTransportHTTP2ConfigureSucceeds locks in the contract that
// newUploadHTTPClient relies on: http2.ConfigureTransports succeeds on a clone of
// http.DefaultTransport, so the HTTP/2 health-check pings are actually applied and
// the warn-and-continue fallback in newUploadHTTPClient is not taken. http.Transport.Clone
// does not copy the altProto registration, so ConfigureTransports does not error even
// though the default transport is HTTP/2-enabled. A regression here (e.g. a future
// x/net change) would silently disable the connection-reuse-race fix, so guard it.
func TestUploadTransportHTTP2ConfigureSucceeds(t *testing.T) {
	t.Parallel()

	transport := httpclient.CloneDefaultTransport()
	h2, err := http2.ConfigureTransports(transport)
	require.NoError(t, err, "ConfigureTransports must succeed on a cloned DefaultTransport so the health-check pings apply")
	require.NotNil(t, h2)
}

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

	// Note: the HTTP/2 health-check settings (ReadIdleTimeout/PingTimeout) applied
	// via http2.ConfigureTransports live on the unexported *http2.Transport and are
	// not reachable from the *http.Transport, so they are intentionally not asserted
	// here. The dedicated-transport check below is what guards against regressing to
	// the shared DefaultTransport (the original bug).

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
