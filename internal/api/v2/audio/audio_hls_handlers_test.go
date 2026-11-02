// audio_hls_handlers_test.go: handler level tests for the HLS heartbeat, stop,
// status and playlist endpoints.
//
// The HLS manager is process-global, so every test here swaps its stream,
// client, activity and token maps for fresh ones (isolateHLSManager) and none of
// them is parallel.
package audio

import (
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	// hlsTestSourceID is a source id that needs URL escaping in a path.
	hlsTestSourceID = "rtsp://camera.lan:554/live"

	// hlsTestSessionA and hlsTestSessionB are two distinct, valid browser session
	// ids; they make two clients on the same address.
	hlsTestSessionA = "session-aaaaaaaaaaaa"
	hlsTestSessionB = "session-bbbbbbbbbbbb"

	// hlsTestRemoteAddr is the address httptest gives every request.
	hlsTestRemoteAddr = "192.0.2.1"

	// hlsTestFallbackClientID is the client id generateClientID gives a request
	// without a session or a recognised user agent.
	hlsTestFallbackClientID = hlsTestRemoteAddr + "-HLSPlayer"

	// hlsTestTarget is the request target of every test request. The handlers are
	// called directly, so routing never reads it.
	hlsTestTarget = "/"

	// hlsTestUnknownToken is a stream token no test registers.
	hlsTestUnknownToken = "not-a-known-token"

	// hlsTestReadySeconds of audio satisfy the two segment readiness gate at the
	// default segment length.
	hlsTestReadySeconds = 6

	// hlsPDTLayout is the layout of the program date time in a rendered playlist
	// for a UTC time, which is what the tests feed the muxer.
	hlsPDTLayout = "2006-01-02T15:04:05.000Z"

	// hlsStatusOKBody is the heartbeat acknowledgement and hlsStatusStoppedBody
	// the stop acknowledgement.
	hlsStatusOKBody      = `{"status":"ok"}`
	hlsStatusStoppedBody = `{"status":"stopped"}`
)

// isolateHLSManager replaces the stream, client, activity and token maps of the
// global HLS manager with empty ones for the duration of the test and restores
// the originals when it ends.
func isolateHLSManager(t *testing.T) {
	t.Helper()

	hlsMgr.streamsMu.Lock()
	origStreams := hlsMgr.streams
	hlsMgr.streams = make(map[string]*HLSStreamInfo)
	hlsMgr.streamsMu.Unlock()

	hlsMgr.clientsMu.Lock()
	origClients := hlsMgr.clients
	hlsMgr.clients = make(map[string]map[string]bool)
	hlsMgr.clientsMu.Unlock()

	hlsMgr.activityMu.Lock()
	origActivity, origClientActivity := hlsMgr.activity, hlsMgr.clientActivity
	hlsMgr.activity = make(map[string]time.Time)
	hlsMgr.clientActivity = make(map[string]time.Time)
	hlsMgr.activityMu.Unlock()

	hlsMgr.tokensMu.Lock()
	origTokens, origSourceTokens := hlsMgr.tokens, hlsMgr.sourceTokens
	hlsMgr.tokens = make(map[string]string)
	hlsMgr.sourceTokens = make(map[string]string)
	hlsMgr.tokensMu.Unlock()

	t.Cleanup(func() {
		hlsMgr.streamsMu.Lock()
		hlsMgr.streams = origStreams
		hlsMgr.streamsMu.Unlock()

		hlsMgr.clientsMu.Lock()
		hlsMgr.clients = origClients
		hlsMgr.clientsMu.Unlock()

		hlsMgr.activityMu.Lock()
		hlsMgr.activity, hlsMgr.clientActivity = origActivity, origClientActivity
		hlsMgr.activityMu.Unlock()

		hlsMgr.tokensMu.Lock()
		hlsMgr.tokens, hlsMgr.sourceTokens = origTokens, origSourceTokens
		hlsMgr.tokensMu.Unlock()
	})
}

// registerHLSTestStream adds a stream for sourceID to the manager and returns
// its token. The stream has no muxer, so it is not native and cleanup never
// closes anything.
func registerHLSTestStream(sourceID string) string {
	return registerHLSStream(sourceID, &HLSStreamInfo{SourceID: sourceID})
}

// registerHLSStream adds stream for sourceID to the manager and returns its token.
func registerHLSStream(sourceID string, stream *HLSStreamInfo) string {
	hlsMgr.streamsMu.Lock()
	hlsMgr.streams[sourceID] = stream
	hlsMgr.streamsMu.Unlock()
	return getOrCreateStreamToken(sourceID)
}

// hlsClientIDs returns the clients tracked for sourceID.
func hlsClientIDs(sourceID string) []string {
	hlsMgr.clientsMu.RLock()
	defer hlsMgr.clientsMu.RUnlock()
	return slices.Collect(maps.Keys(hlsMgr.clients[sourceID]))
}

// hlsHasActivity reports whether the manager recorded stream activity.
func hlsHasActivity(sourceID string) bool {
	hlsMgr.activityMu.Lock()
	defer hlsMgr.activityMu.Unlock()
	_, ok := hlsMgr.activity[sourceID]
	return ok
}

// hlsHasStream reports whether the manager still holds a stream for sourceID.
func hlsHasStream(sourceID string) bool {
	hlsMgr.streamsMu.RLock()
	defer hlsMgr.streamsMu.RUnlock()
	_, ok := hlsMgr.streams[sourceID]
	return ok
}

// hlsTrackedNothing reports whether the manager tracks no client, no client
// activity and no stream activity.
func hlsTrackedNothing() bool {
	hlsMgr.clientsMu.RLock()
	clients := len(hlsMgr.clients)
	hlsMgr.clientsMu.RUnlock()
	hlsMgr.activityMu.Lock()
	activity := len(hlsMgr.activity) + len(hlsMgr.clientActivity)
	hlsMgr.activityMu.Unlock()
	return clients == 0 && activity == 0
}

// ageHLSClientActivity moves the last activity of every client of sourceID back
// beyond the premature disconnect window.
func ageHLSClientActivity(sourceID string) {
	hlsMgr.activityMu.Lock()
	defer hlsMgr.activityMu.Unlock()
	old := time.Now().Add(-(hlsPrematureDisconnectThreshold + time.Second))
	for key := range hlsMgr.clientActivity {
		if strings.HasPrefix(key, sourceID+":") {
			hlsMgr.clientActivity[key] = old
		}
	}
}

// postHeartbeat sends body to the heartbeat handler and returns the response.
func postHeartbeat(t *testing.T, h *Handler, e *echo.Echo, query, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, hlsTestTarget+query, strings.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	require.NoError(t, h.HLSHeartbeat(e.NewContext(req, rec)))
	return rec
}

// heartbeatBody builds the JSON a browser sends for token and session.
func heartbeatBody(token, session string) string {
	return `{"stream_token":"` + token + `","session_id":"` + session + `"}`
}

// postStop sends a stop request for sourceID with the given session and returns
// the response.
func postStop(t *testing.T, h *Handler, e *echo.Echo, sourceID, session string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, hlsTestTarget,
		strings.NewReader(`{"session_id":"`+session+`"}`))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)
	ctx.SetParamNames("sourceID")
	ctx.SetParamValues(url.PathEscape(sourceID))
	require.NoError(t, h.StopHLSStream(ctx))
	return rec
}

func TestHLSHeartbeat(t *testing.T) {
	h, e := newNativeTestHandler(t)

	t.Run("invalid body is rejected", func(t *testing.T) {
		isolateHLSManager(t)

		rec := postHeartbeat(t, h, e, "", `{invalid`)

		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.True(t, hlsTrackedNothing())
	})

	t.Run("unknown token is acknowledged without side effects", func(t *testing.T) {
		isolateHLSManager(t)

		rec := postHeartbeat(t, h, e, "", heartbeatBody(hlsTestUnknownToken, hlsTestSessionA))

		assert.Equal(t, http.StatusOK, rec.Code)
		assert.JSONEq(t, hlsStatusOKBody, rec.Body.String())
		assert.True(t, hlsTrackedNothing())
	})

	t.Run("known token without a stream is acknowledged without side effects", func(t *testing.T) {
		isolateHLSManager(t)
		token := getOrCreateStreamToken(hlsTestSourceID)

		rec := postHeartbeat(t, h, e, "", heartbeatBody(token, hlsTestSessionA))

		assert.Equal(t, http.StatusOK, rec.Code)
		assert.JSONEq(t, hlsStatusOKBody, rec.Body.String())
		assert.True(t, hlsTrackedNothing())
	})

	t.Run("heartbeat for a live stream registers the client and refreshes activity", func(t *testing.T) {
		isolateHLSManager(t)
		token := registerHLSTestStream(hlsTestSourceID)

		rec := postHeartbeat(t, h, e, "", heartbeatBody(token, hlsTestSessionA))

		assert.Equal(t, http.StatusOK, rec.Code)
		assert.JSONEq(t, hlsStatusOKBody, rec.Body.String())
		assert.True(t, hlsHasActivity(hlsTestSourceID))
		clients := hlsClientIDs(hlsTestSourceID)
		require.Len(t, clients, 1)
		assert.Contains(t, clients[0], hlsTestSessionA, "the client is identified by its session")
	})

	t.Run("a heartbeat without a session still registers the client", func(t *testing.T) {
		isolateHLSManager(t)
		token := registerHLSTestStream(hlsTestSourceID)

		rec := postHeartbeat(t, h, e, "", `{"stream_token":"`+token+`"}`)

		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, []string{hlsTestFallbackClientID}, hlsClientIDs(hlsTestSourceID),
			"without a session the client is identified from its address and user agent; httptest requests carry no user agent")
	})

	t.Run("a premature disconnect announcement keeps the client", func(t *testing.T) {
		isolateHLSManager(t)
		token := registerHLSTestStream(hlsTestSourceID)
		postHeartbeat(t, h, e, "", heartbeatBody(token, hlsTestSessionA))

		rec := postHeartbeat(t, h, e, "?disconnect=true", heartbeatBody(token, hlsTestSessionA))

		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Len(t, hlsClientIDs(hlsTestSourceID), 1, "a client that was just active has not left")
	})

	for _, query := range []string{"?disconnect=true", "?status=disconnect"} {
		t.Run("a disconnect announcement removes a quiet client "+query, func(t *testing.T) {
			isolateHLSManager(t)
			token := registerHLSTestStream(hlsTestSourceID)
			postHeartbeat(t, h, e, "", heartbeatBody(token, hlsTestSessionA))
			postHeartbeat(t, h, e, "", heartbeatBody(token, hlsTestSessionB))
			require.Len(t, hlsClientIDs(hlsTestSourceID), 2)
			ageHLSClientActivity(hlsTestSourceID)

			rec := postHeartbeat(t, h, e, query, heartbeatBody(token, hlsTestSessionA))

			assert.Equal(t, http.StatusOK, rec.Code)
			remaining := hlsClientIDs(hlsTestSourceID)
			require.Len(t, remaining, 1)
			assert.Contains(t, remaining[0], hlsTestSessionB, "only the announcing client leaves")
		})
	}
}

func TestStopHLSStream(t *testing.T) {
	h, e := newNativeTestHandler(t)

	t.Run("the last client stops the stream and drops its token", func(t *testing.T) {
		isolateHLSManager(t)
		token := registerHLSTestStream(hlsTestSourceID)
		postHeartbeat(t, h, e, "", heartbeatBody(token, hlsTestSessionA))

		rec := postStop(t, h, e, hlsTestSourceID, hlsTestSessionA)

		assert.Equal(t, http.StatusOK, rec.Code)
		assert.JSONEq(t, hlsStatusStoppedBody, rec.Body.String())
		assert.False(t, hlsHasStream(hlsTestSourceID), "the stream is removed")
		assert.Empty(t, resolveStreamToken(token), "its token no longer resolves")
		assert.True(t, hlsTrackedNothing(), "its clients, client activity and activity are dropped")
	})

	t.Run("another client keeps the stream running", func(t *testing.T) {
		isolateHLSManager(t)
		token := registerHLSTestStream(hlsTestSourceID)
		postHeartbeat(t, h, e, "", heartbeatBody(token, hlsTestSessionA))
		postHeartbeat(t, h, e, "", heartbeatBody(token, hlsTestSessionB))

		rec := postStop(t, h, e, hlsTestSourceID, hlsTestSessionA)

		assert.Equal(t, http.StatusOK, rec.Code)
		assert.True(t, hlsHasStream(hlsTestSourceID), "a stream with a remaining client keeps running")
		assert.Equal(t, hlsTestSourceID, resolveStreamToken(token))
		remaining := hlsClientIDs(hlsTestSourceID)
		require.Len(t, remaining, 1)
		assert.Contains(t, remaining[0], hlsTestSessionB)
	})

	t.Run("an unknown source is answered as stopped", func(t *testing.T) {
		isolateHLSManager(t)

		rec := postStop(t, h, e, hlsTestSourceID, hlsTestSessionA)

		assert.Equal(t, http.StatusOK, rec.Code)
		assert.JSONEq(t, hlsStatusStoppedBody, rec.Body.String())
	})
}

func TestGetHLSStatus(t *testing.T) {
	h, e := newNativeTestHandler(t)

	get := func(t *testing.T) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, hlsTestTarget, http.NoBody)
		rec := httptest.NewRecorder()
		require.NoError(t, h.GetHLSStatus(e.NewContext(req, rec)))
		return rec
	}

	t.Run("no streams gives an empty list", func(t *testing.T) {
		isolateHLSManager(t)

		rec := get(t)

		assert.Equal(t, http.StatusOK, rec.Code)
		assert.JSONEq(t, `{"streams":[],"count":0}`, rec.Body.String())
	})

	t.Run("a stream nobody has joined reports no clients", func(t *testing.T) {
		isolateHLSManager(t)
		registerHLSTestStream(hlsTestSourceID)

		rec := get(t)

		var body struct {
			Streams []map[string]any `json:"streams"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
		require.Len(t, body.Streams, 1)
		assert.InDelta(t, 0, body.Streams[0]["active_clients"], 0)
	})

	t.Run("a stream with enough audio is reported ready", func(t *testing.T) {
		isolateHLSManager(t)
		registerHLSStream(hlsTestSourceID, newNativeTestStream(t, hlsTestReadySeconds))

		rec := get(t)

		var body struct {
			Streams []map[string]any `json:"streams"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
		require.Len(t, body.Streams, 1)
		assert.Equal(t, true, body.Streams[0]["playlist_ready"],
			"enough audio to satisfy the readiness gate")
	})

	t.Run("a stream is reported with its fields and without its token", func(t *testing.T) {
		isolateHLSManager(t)
		token := registerHLSTestStream(hlsTestSourceID)
		postHeartbeat(t, h, e, "", heartbeatBody(token, hlsTestSessionA))
		postHeartbeat(t, h, e, "", heartbeatBody(token, hlsTestSessionB))

		rec := get(t)

		assert.Equal(t, http.StatusOK, rec.Code)
		var body struct {
			Count   int              `json:"count"`
			Streams []map[string]any `json:"streams"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
		require.Equal(t, 1, body.Count)
		require.Len(t, body.Streams, 1)
		stream := body.Streams[0]
		assert.Equal(t, url.PathEscape(hlsTestSourceID), stream["source"])
		assert.Equal(t, "/api/v2/streams/hls/t/"+token+"/playlist.m3u8", stream["playlist_url"],
			"the playlist URL is the address clients fetch, spelled out here so it cannot drift unnoticed")
		assert.InDelta(t, 2, stream["active_clients"], 0)
		assert.Equal(t, false, stream["playlist_ready"], "a stream without a muxer is not ready")
		assert.NotContains(t, stream, "stream_token", "the token must not leak through the status")
	})
}

func TestServeHLSPlaylist(t *testing.T) {
	h, e := newNativeTestHandler(t)

	get := func(t *testing.T, token string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, hlsTestTarget, http.NoBody)
		rec := httptest.NewRecorder()
		ctx := e.NewContext(req, rec)
		ctx.SetParamNames("streamToken")
		ctx.SetParamValues(token)
		require.NoError(t, h.ServeHLSPlaylist(ctx))
		return rec
	}

	t.Run("unknown token is not found", func(t *testing.T) {
		isolateHLSManager(t)

		assert.Equal(t, http.StatusNotFound, get(t, hlsTestUnknownToken).Code)
	})

	t.Run("known token without a stream is not found", func(t *testing.T) {
		isolateHLSManager(t)
		token := getOrCreateStreamToken(hlsTestSourceID)

		assert.Equal(t, http.StatusNotFound, get(t, token).Code)
		assert.False(t, hlsHasActivity(hlsTestSourceID), "a missing stream records no activity")
	})

	t.Run("a live stream serves its playlist and refreshes stream activity", func(t *testing.T) {
		isolateHLSManager(t)
		t.Cleanup(func() { lastFreshnessCheck.Delete(hlsTestSourceID) })
		stream := newNativeTestStream(t, hlsTestReadySeconds)
		token := registerHLSStream(hlsTestSourceID, stream)

		rec := get(t, token)

		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Contains(t, rec.Body.String(), "#EXT-X-PROGRAM-DATE-TIME:"+stream.streamEpoch.UTC().Format(hlsPDTLayout),
			"the playlist comes from the stream's own muxer")
		assert.True(t, hlsHasActivity(hlsTestSourceID), "serving the playlist keeps the stream alive")
		assert.Empty(t, hlsClientIDs(hlsTestSourceID), "the playlist handler must not register a client")
	})
}
