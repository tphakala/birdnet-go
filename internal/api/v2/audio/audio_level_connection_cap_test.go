// internal/api/v2/audio/audio_level_connection_cap_test.go
// Tests that the audio-level SSE connection cap counts one client, not one
// address: every address in an IPv6 /64 shares a single budget.
package audio

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/api/v2/apitest"
	"github.com/tphakala/birdnet-go/internal/audiocore"
)

const audioLevelStreamPath = "/api/v2/streams/audio-level"

// flushSignalRecorder closes flushed on the first Flush. The handler flushes
// for the first time after it has counted its connection slot, so a closed
// channel means the slot is held.
type flushSignalRecorder struct {
	*httptest.ResponseRecorder
	flushed chan struct{}
	once    sync.Once
}

func (r *flushSignalRecorder) Flush() {
	r.ResponseRecorder.Flush()
	r.once.Do(func() { close(r.flushed) })
}

// holdAudioLevelConnection opens a stream from remoteAddr and returns once the
// handler holds its slot. The returned release func ends the stream and waits
// for the handler to give the slot back; it is safe to call more than once.
func holdAudioLevelConnection(t *testing.T, h *Handler, e *echo.Echo, remoteAddr string) (release func()) {
	t.Helper()

	reqCtx, cancel := context.WithCancel(t.Context())
	req := httptest.NewRequest(http.MethodGet, audioLevelStreamPath, http.NoBody).WithContext(reqCtx)
	req.RemoteAddr = remoteAddr
	rec := &flushSignalRecorder{ResponseRecorder: httptest.NewRecorder(), flushed: make(chan struct{})}
	c := e.NewContext(req, rec)

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = h.StreamAudioLevel(c)
	}()

	var once sync.Once
	release = func() {
		once.Do(func() {
			cancel()
			<-done
		})
	}
	t.Cleanup(release)

	select {
	case <-rec.flushed:
	case <-done:
		require.Failf(t, "connection not held", "handler returned before holding the slot for %s (status %d)", remoteAddr, rec.Code)
	}
	return release
}

// audioLevelConnectStatus makes one short-lived request from remoteAddr and
// returns the status: 200 when admitted, 429 when the cap refuses it.
func audioLevelConnectStatus(t *testing.T, h *Handler, e *echo.Echo, remoteAddr string) int {
	t.Helper()

	reqCtx, cancel := context.WithCancel(t.Context())
	cancel()
	req := httptest.NewRequest(http.MethodGet, audioLevelStreamPath, http.NoBody).WithContext(reqCtx)
	req.RemoteAddr = remoteAddr
	rec := httptest.NewRecorder()

	require.NoError(t, h.StreamAudioLevel(e.NewContext(req, rec)))
	return rec.Code
}

// TestStreamAudioLevelCapsConnectionsPerClient verifies the connection cap is
// per client: one IPv4 address or one IPv6 /64, so a client cannot multiply its
// budget by rotating addresses inside its prefix.
func TestStreamAudioLevelCapsConnectionsPerClient(t *testing.T) {
	// Not parallel: StreamAudioLevel mutates the package-global audioLevelMgr
	// connection counters.
	e := echo.New()
	core := apitest.NewCore(t, apitest.WithEcho(e), apitest.WithoutSettingsPublish())
	h := &Handler{Core: core}
	// Set the field directly to avoid starting the broadcaster goroutine.
	h.audioLevelChan = make(chan audiocore.AudioLevelData, 1)

	const (
		sixthInPrefix = "[2001:db8:1:2:dead:beef:0:6]:40006"
		otherPrefix   = "[2001:db8:1:3::1]:40007"
		ipv4Peer      = "192.0.2.10:40008"
		prefixAddrFmt = "[2001:db8:1:2::%d]:4000%d"
	)

	// Fill the budget with distinct addresses from one /64.
	releases := make([]func(), 0, audioLevelMaxConnectionsPerClient)
	for i := range audioLevelMaxConnectionsPerClient {
		host := i + 1
		releases = append(releases, holdAudioLevelConnection(t, h, e, fmt.Sprintf(prefixAddrFmt, host, host)))
	}

	assert.Equal(t, http.StatusTooManyRequests, audioLevelConnectStatus(t, h, e, sixthInPrefix),
		"another address in the same /64 must share the budget")
	assert.Equal(t, http.StatusOK, audioLevelConnectStatus(t, h, e, otherPrefix),
		"a different /64 has its own budget")
	assert.Equal(t, http.StatusOK, audioLevelConnectStatus(t, h, e, ipv4Peer),
		"an IPv4 peer is independent of the IPv6 keys")

	releases[0]()
	assert.Equal(t, http.StatusOK, audioLevelConnectStatus(t, h, e, sixthInPrefix),
		"releasing a connection must free a slot under the same key it was counted under")
}
