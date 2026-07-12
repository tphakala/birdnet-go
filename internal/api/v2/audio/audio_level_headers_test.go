// internal/api/v2/audio/audio_level_headers_test.go
// Tests that the audio-level SSE endpoint emits the shared SSE headers,
// including X-Accel-Buffering: no, so nginx and compatible reverse proxies
// stream events immediately instead of buffering them (issue #3862).
package audio

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/api/v2/apitest"
	"github.com/tphakala/birdnet-go/internal/audiocore"
)

// TestStreamAudioLevelSetsProxyBufferingHeader verifies that StreamAudioLevel
// emits X-Accel-Buffering: no (via the shared apicore.SetSSEHeaders helper) so
// nginx and compatible reverse proxies stream the audio-level events immediately
// instead of buffering them. Regression guard for issue #3862: this endpoint set
// its SSE headers inline and previously missed the buffering header.
func TestStreamAudioLevelSetsProxyBufferingHeader(t *testing.T) {
	// Not parallel: StreamAudioLevel mutates the package-global audioLevelMgr
	// connection counters.
	e := echo.New()
	core := apitest.NewCore(t, apitest.WithEcho(e), apitest.WithoutSettingsPublish())
	h := &Handler{Core: core}

	// Inject a non-nil channel so the handler passes its early availability guard.
	// Set the field directly (rather than via SetAudioLevelChan) to avoid starting
	// the broadcaster goroutine; the stream exits on the cancelled request context.
	h.audioLevelChan = make(chan audiocore.AudioLevelData, 1)

	// A pre-cancelled request context makes the handler set headers, send the
	// initial update, then return as soon as it enters its event loop.
	reqCtx, cancel := context.WithCancel(t.Context())
	cancel()
	req := httptest.NewRequest(http.MethodGet, "/api/v2/streams/audio-level", http.NoBody).WithContext(reqCtx)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	require.NoError(t, h.StreamAudioLevel(c))

	assert.Equal(t, "no", rec.Header().Get("X-Accel-Buffering"),
		"audio-level SSE must set X-Accel-Buffering: no so reverse proxies do not buffer the stream")
	assert.True(t, strings.HasPrefix(rec.Header().Get("Content-Type"), "text/event-stream"),
		"audio-level SSE must use the text/event-stream content type")
	assert.Equal(t, "no-cache", rec.Header().Get("Cache-Control"))
	assert.Equal(t, "keep-alive", rec.Header().Get("Connection"))
}
