// internal/api/v2/audio/audio_hls_test.go
// Tests for HLS streaming endpoint functionality
package audio

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/api/v2/apitest"
	"github.com/tphakala/birdnet-go/internal/audiocore"
	"github.com/tphakala/birdnet-go/internal/audiocore/engine"
)

// TestHLSStreamStatusStruct tests the HLSStreamStatus struct
func TestHLSStreamStatusStruct(t *testing.T) {
	t.Run("starting status", func(t *testing.T) {
		status := HLSStreamStatus{
			Status:        "starting",
			Source:        "test_source",
			StreamToken:   "abc123def456abc123def456abc123de",
			PlaylistURL:   "/api/v2/streams/hls/t/abc123def456abc123def456abc123de/playlist.m3u8",
			ActiveClients: 0,
			PlaylistReady: false,
		}

		assert.Equal(t, "test_source", status.Source)
		assert.Contains(t, status.PlaylistURL, "/t/")
		assert.Contains(t, status.PlaylistURL, "playlist.m3u8")
		assert.NotEmpty(t, status.StreamToken)
		assert.Equal(t, "starting", status.Status)
		assert.False(t, status.PlaylistReady)
	})

	t.Run("ready status with clients", func(t *testing.T) {
		status := HLSStreamStatus{
			Status:        "ready",
			Source:        "rtsp%3A%2F%2Fcamera.local%2Fstream", // URL-encoded source
			StreamToken:   "def456abc123def456abc123def456ab",
			PlaylistURL:   "/api/v2/streams/hls/t/def456abc123def456abc123def456ab/playlist.m3u8",
			ActiveClients: 2,
			PlaylistReady: true,
		}

		assert.Equal(t, "ready", status.Status)
		assert.True(t, status.PlaylistReady)
		assert.Equal(t, 2, status.ActiveClients)
		assert.Contains(t, status.PlaylistURL, "/t/")
	})
}

// TestHLSManagerStreamTracking tests the HLS manager stream tracking
func TestHLSManagerStreamTracking(t *testing.T) {
	t.Run("getActiveStreamIDs returns empty for no streams", func(t *testing.T) {
		// Save and restore original state
		originalStreams := hlsMgr.streams
		hlsMgr.streamsMu.Lock()
		hlsMgr.streams = make(map[string]*HLSStreamInfo)
		hlsMgr.streamsMu.Unlock()

		defer func() {
			hlsMgr.streamsMu.Lock()
			hlsMgr.streams = originalStreams
			hlsMgr.streamsMu.Unlock()
		}()

		ids := getActiveStreamIDs()
		assert.Empty(t, ids)
	})

	t.Run("getActiveStreamIDs returns all stream IDs", func(t *testing.T) {
		// Save and restore original state
		originalStreams := hlsMgr.streams
		hlsMgr.streamsMu.Lock()
		hlsMgr.streams = map[string]*HLSStreamInfo{
			"source1": {SourceID: "source1"},
			"source2": {SourceID: "source2"},
		}
		hlsMgr.streamsMu.Unlock()

		defer func() {
			hlsMgr.streamsMu.Lock()
			hlsMgr.streams = originalStreams
			hlsMgr.streamsMu.Unlock()
		}()

		ids := getActiveStreamIDs()
		assert.Len(t, ids, 2)
		assert.Contains(t, ids, "source1")
		assert.Contains(t, ids, "source2")
	})
}

// TestHLSManagerClientTracking tests the HLS client tracking
func TestHLSManagerClientTracking(t *testing.T) {
	t.Run("getStreamClientCount returns 0 for unknown stream", func(t *testing.T) {
		count := getStreamClientCount("nonexistent_stream")
		assert.Equal(t, 0, count)
	})

	t.Run("getStreamClientCount returns correct count", func(t *testing.T) {
		// Save and restore original state
		originalClients := hlsMgr.clients
		hlsMgr.clientsMu.Lock()
		hlsMgr.clients = map[string]map[string]bool{
			"source1": {
				"client1": true,
				"client2": true,
			},
		}
		hlsMgr.clientsMu.Unlock()

		defer func() {
			hlsMgr.clientsMu.Lock()
			hlsMgr.clients = originalClients
			hlsMgr.clientsMu.Unlock()
		}()

		count := getStreamClientCount("source1")
		assert.Equal(t, 2, count)
	})
}

// TestShouldCleanupStream tests the stream cleanup logic
func TestShouldCleanupStream(t *testing.T) {
	t.Run("nonexistent stream should not be cleaned up", func(t *testing.T) {
		// Save and restore original state
		originalActivity := hlsMgr.activity
		hlsMgr.activityMu.Lock()
		hlsMgr.activity = make(map[string]time.Time)
		hlsMgr.activityMu.Unlock()

		defer func() {
			hlsMgr.activityMu.Lock()
			hlsMgr.activity = originalActivity
			hlsMgr.activityMu.Unlock()
		}()

		shouldCleanup := shouldCleanupStream("nonexistent")
		assert.False(t, shouldCleanup)
	})

	t.Run("recently active stream should not be cleaned up", func(t *testing.T) {
		// Save and restore original state
		originalActivity := hlsMgr.activity
		hlsMgr.activityMu.Lock()
		hlsMgr.activity = map[string]time.Time{
			"recent_stream": time.Now(),
		}
		hlsMgr.activityMu.Unlock()

		defer func() {
			hlsMgr.activityMu.Lock()
			hlsMgr.activity = originalActivity
			hlsMgr.activityMu.Unlock()
		}()

		shouldCleanup := shouldCleanupStream("recent_stream")
		assert.False(t, shouldCleanup)
	})

	t.Run("stream within grace period should not be cleaned up", func(t *testing.T) {
		// Save and restore original state
		originalActivity := hlsMgr.activity
		hlsMgr.activityMu.Lock()
		hlsMgr.activity = map[string]time.Time{
			"grace_stream": time.Now().Add(-5 * time.Second), // Within 10 second grace period
		}
		hlsMgr.activityMu.Unlock()

		defer func() {
			hlsMgr.activityMu.Lock()
			hlsMgr.activity = originalActivity
			hlsMgr.activityMu.Unlock()
		}()

		shouldCleanup := shouldCleanupStream("grace_stream")
		assert.False(t, shouldCleanup)
	})
}

// TestFindInactiveStreams tests finding inactive streams
func TestFindInactiveStreams(t *testing.T) {
	t.Run("empty stream list returns empty", func(t *testing.T) {
		inactive := findInactiveStreams([]string{})
		assert.Empty(t, inactive)
	})

	t.Run("all active streams returns empty", func(t *testing.T) {
		// Save and restore original state
		originalActivity := hlsMgr.activity
		hlsMgr.activityMu.Lock()
		hlsMgr.activity = map[string]time.Time{
			"stream1": time.Now(),
			"stream2": time.Now(),
		}
		hlsMgr.activityMu.Unlock()

		defer func() {
			hlsMgr.activityMu.Lock()
			hlsMgr.activity = originalActivity
			hlsMgr.activityMu.Unlock()
		}()

		inactive := findInactiveStreams([]string{"stream1", "stream2"})
		assert.Empty(t, inactive)
	})
}

// TestRemoveStreamFromManager tests stream removal
func TestRemoveStreamFromManager(t *testing.T) {
	t.Run("remove nonexistent stream returns nil", func(t *testing.T) {
		stream := removeStreamFromManager("nonexistent_stream_xyz")
		assert.Nil(t, stream)
	})

	t.Run("remove existing stream returns stream info", func(t *testing.T) {
		testStreamID := "test_remove_stream_" + time.Now().String()
		testStream := &HLSStreamInfo{
			SourceID: testStreamID,
		}

		// Add stream
		hlsMgr.streamsMu.Lock()
		hlsMgr.streams[testStreamID] = testStream
		hlsMgr.streamsMu.Unlock()

		// Remove stream
		removed := removeStreamFromManager(testStreamID)
		assert.NotNil(t, removed)
		assert.Equal(t, testStreamID, removed.SourceID)

		// Verify it's gone
		hlsMgr.streamsMu.Lock()
		_, exists := hlsMgr.streams[testStreamID]
		hlsMgr.streamsMu.Unlock()
		assert.False(t, exists)
	})
}

// TestHLSManagerConcurrency tests concurrent access to HLS manager
func TestHLSManagerConcurrency(t *testing.T) {
	t.Run("concurrent stream operations", func(t *testing.T) {
		const numGoroutines = 10
		var wg sync.WaitGroup
		wg.Add(numGoroutines)

		for i := range numGoroutines {
			go func(id int) {
				defer wg.Done()
				streamID := fmt.Sprintf("concurrent_test_%s_%d", time.Now().String(), id)

				// Add stream
				hlsMgr.streamsMu.Lock()
				hlsMgr.streams[streamID] = &HLSStreamInfo{SourceID: streamID}
				hlsMgr.streamsMu.Unlock()

				// Remove stream
				_ = removeStreamFromManager(streamID)
			}(i)
		}

		wg.Wait()
	})

	t.Run("concurrent activity updates", func(t *testing.T) {
		const numGoroutines = 10
		var wg sync.WaitGroup
		wg.Add(numGoroutines)

		for i := range numGoroutines {
			go func(id int) {
				defer wg.Done()
				streamID := fmt.Sprintf("activity_test_%s_%d", time.Now().String(), id)

				// Update activity
				hlsMgr.activityMu.Lock()
				hlsMgr.activity[streamID] = time.Now()
				hlsMgr.activityMu.Unlock()

				// Read activity
				hlsMgr.activityMu.Lock()
				delete(hlsMgr.activity, streamID)
				hlsMgr.activityMu.Unlock()
			}(i)
		}

		wg.Wait()
	})

	t.Run("concurrent client tracking", func(t *testing.T) {
		const numGoroutines = 10
		var wg sync.WaitGroup
		wg.Add(numGoroutines)

		for i := range numGoroutines {
			go func(id int) {
				defer wg.Done()
				streamID := fmt.Sprintf("client_test_%s_%d", time.Now().String(), id)
				clientID := fmt.Sprintf("client_%d", id)

				// Add client
				hlsMgr.clientsMu.Lock()
				if hlsMgr.clients[streamID] == nil {
					hlsMgr.clients[streamID] = make(map[string]bool)
				}
				hlsMgr.clients[streamID][clientID] = true
				hlsMgr.clientsMu.Unlock()

				// Remove client
				hlsMgr.clientsMu.Lock()
				delete(hlsMgr.clients, streamID)
				hlsMgr.clientsMu.Unlock()
			}(i)
		}

		wg.Wait()
	})
}

// TestHLSHeartbeatRequest tests the heartbeat request struct JSON binding
func TestHLSHeartbeatRequest(t *testing.T) {
	t.Run("heartbeat with stream token only", func(t *testing.T) {
		body := []byte(`{"stream_token":"abc123def456abc123def456abc123de"}`)
		var req HLSHeartbeatRequest
		require.NoError(t, json.Unmarshal(body, &req))

		assert.Equal(t, "abc123def456abc123def456abc123de", req.StreamToken)
		assert.Empty(t, req.SessionID)
	})

	t.Run("heartbeat with session id", func(t *testing.T) {
		body := []byte(`{"stream_token":"abc123def456abc123def456abc123de","session_id":"550e8400-e29b-41d4-a716-446655440000"}`)
		var req HLSHeartbeatRequest
		require.NoError(t, json.Unmarshal(body, &req))

		assert.Equal(t, "abc123def456abc123def456abc123de", req.StreamToken)
		assert.Equal(t, "550e8400-e29b-41d4-a716-446655440000", req.SessionID)
	})
}

// TestStreamTokenGeneration tests crypto-random token generation
func TestStreamTokenGeneration(t *testing.T) {
	t.Run("generates 32-character hex string", func(t *testing.T) {
		token, err := generateStreamToken()
		require.NoError(t, err)
		assert.Len(t, token, 32)

		// Verify it's valid hex
		_, err = hex.DecodeString(token)
		assert.NoError(t, err)
	})

	t.Run("generates unique tokens", func(t *testing.T) {
		const tokenCount = 100
		tokens := make(map[string]bool, tokenCount)

		for range tokenCount {
			token, err := generateStreamToken()
			require.NoError(t, err)
			assert.False(t, tokens[token], "duplicate token generated: %s", token)
			tokens[token] = true
		}

		assert.Len(t, tokens, tokenCount)
	})
}

// TestStreamTokenMapping tests token creation, resolution, and removal
func TestStreamTokenMapping(t *testing.T) {
	// Save and restore original token state
	originalTokens := hlsMgr.tokens
	originalSourceTokens := hlsMgr.sourceTokens
	hlsMgr.tokensMu.Lock()
	hlsMgr.tokens = make(map[string]string)
	hlsMgr.sourceTokens = make(map[string]string)
	hlsMgr.tokensMu.Unlock()

	t.Cleanup(func() {
		hlsMgr.tokensMu.Lock()
		hlsMgr.tokens = originalTokens
		hlsMgr.sourceTokens = originalSourceTokens
		hlsMgr.tokensMu.Unlock()
	})

	t.Run("getOrCreate creates and returns token", func(t *testing.T) {
		token, err := getOrCreateStreamToken("source_a")
		require.NoError(t, err)
		assert.Len(t, token, 32)

		// Verify resolve works
		resolved := resolveStreamToken(token)
		assert.Equal(t, "source_a", resolved)
	})

	t.Run("getOrCreate is idempotent", func(t *testing.T) {
		token1, err := getOrCreateStreamToken("source_a")
		require.NoError(t, err)

		token2, err := getOrCreateStreamToken("source_a")
		require.NoError(t, err)

		assert.Equal(t, token1, token2, "same sourceID should return same token")
	})

	t.Run("different sources get different tokens", func(t *testing.T) {
		tokenA, err := getOrCreateStreamToken("source_a")
		require.NoError(t, err)

		tokenB, err := getOrCreateStreamToken("source_b")
		require.NoError(t, err)

		assert.NotEqual(t, tokenA, tokenB)
	})

	t.Run("remove clears both mappings", func(t *testing.T) {
		token, err := getOrCreateStreamToken("source_c")
		require.NoError(t, err)

		// Verify it exists
		assert.Equal(t, "source_c", resolveStreamToken(token))

		// Remove it
		removeStreamToken("source_c")

		// Verify both directions are gone
		assert.Empty(t, resolveStreamToken(token))

		// A new call should create a different token
		newToken, err := getOrCreateStreamToken("source_c")
		require.NoError(t, err)
		assert.NotEqual(t, token, newToken)
	})

	t.Run("resolve unknown token returns empty", func(t *testing.T) {
		resolved := resolveStreamToken("nonexistent_token_value")
		assert.Empty(t, resolved)
	})

	t.Run("remove nonexistent source is safe", func(t *testing.T) {
		// Should not panic
		removeStreamToken("nonexistent_source_xyz")
	})
}

// TestGetOrCreateStreamTokenConcurrency tests that concurrent token requests
// for the same source all return the same token.
func TestGetOrCreateStreamTokenConcurrency(t *testing.T) {
	t.Run("concurrent token requests for same source return same token", func(t *testing.T) {
		// Save and restore original state
		originalTokens := hlsMgr.tokens
		originalSourceTokens := hlsMgr.sourceTokens
		hlsMgr.tokensMu.Lock()
		hlsMgr.tokens = make(map[string]string)
		hlsMgr.sourceTokens = make(map[string]string)
		hlsMgr.tokensMu.Unlock()

		t.Cleanup(func() {
			hlsMgr.tokensMu.Lock()
			hlsMgr.tokens = originalTokens
			hlsMgr.sourceTokens = originalSourceTokens
			hlsMgr.tokensMu.Unlock()
		})

		const numGoroutines = 20
		type result struct {
			token string
			err   error
		}
		results := make(chan result, numGoroutines)
		var wg sync.WaitGroup
		wg.Add(numGoroutines)

		for range numGoroutines {
			go func() {
				defer wg.Done()
				token, err := getOrCreateStreamToken("concurrent_source")
				results <- result{token: token, err: err}
			}()
		}

		wg.Wait()
		close(results)

		// Verify all goroutines succeeded and got the same non-empty token
		var firstToken string
		for r := range results {
			require.NoError(t, r.err)
			if firstToken == "" {
				firstToken = r.token
				require.NotEmpty(t, firstToken, "token should not be empty")
			}
			assert.Equal(t, firstToken, r.token, "all concurrent requests should get same token")
		}
	})
}

// TestResolveClientID tests the resolveClientID method
func TestResolveClientID(t *testing.T) {
	t.Parallel()
	const testRemoteAddr = "192.168.1.100:12345"

	t.Run("prefers session ID when provided", func(t *testing.T) {
		t.Parallel()
		c := &Handler{Core: &apicore.Core{}}
		c.Settings.Store(apitest.NewValidTestSettings())
		e := echo.New()
		req := httptest.NewRequest(http.MethodPost, "/", http.NoBody)
		req.RemoteAddr = testRemoteAddr
		ctx := e.NewContext(req, httptest.NewRecorder())

		validUUID := "550e8400-e29b-41d4-a716-446655440000"
		clientID := c.resolveClientID(ctx, validUUID)
		assert.Contains(t, clientID, "192.168.1.100")
		assert.Contains(t, clientID, validUUID)
	})

	t.Run("falls back to generateClientID when no session", func(t *testing.T) {
		t.Parallel()
		c := &Handler{Core: &apicore.Core{}}
		c.Settings.Store(apitest.NewValidTestSettings())
		e := echo.New()
		req := httptest.NewRequest(http.MethodPost, "/", http.NoBody)
		req.RemoteAddr = testRemoteAddr
		req.Header.Set("User-Agent", "Mozilla/5.0")
		ctx := e.NewContext(req, httptest.NewRecorder())

		clientID := c.resolveClientID(ctx, "")
		assert.Contains(t, clientID, "192.168.1.100")
		assert.Contains(t, clientID, "Browser")
		assert.NotContains(t, clientID, "uuid")
	})

	t.Run("different sessions from same IP get different IDs", func(t *testing.T) {
		t.Parallel()
		c := &Handler{Core: &apicore.Core{}}
		c.Settings.Store(apitest.NewValidTestSettings())
		e := echo.New()

		req1 := httptest.NewRequest(http.MethodPost, "/", http.NoBody)
		req1.RemoteAddr = testRemoteAddr
		ctx1 := e.NewContext(req1, httptest.NewRecorder())

		req2 := httptest.NewRequest(http.MethodPost, "/", http.NoBody)
		req2.RemoteAddr = testRemoteAddr // Same IP, same port - session ID differentiates
		ctx2 := e.NewContext(req2, httptest.NewRecorder())

		id1 := c.resolveClientID(ctx1, "550e8400-e29b-41d4-a716-446655440000")
		id2 := c.resolveClientID(ctx2, "660e8400-e29b-41d4-a716-446655440000")
		assert.NotEqual(t, id1, id2)
	})

	t.Run("accepts safe non-UUID session ID", func(t *testing.T) {
		t.Parallel()
		c := &Handler{Core: &apicore.Core{}}
		c.Settings.Store(apitest.NewValidTestSettings())
		e := echo.New()
		req := httptest.NewRequest(http.MethodPost, "/", http.NoBody)
		req.RemoteAddr = testRemoteAddr
		req.Header.Set("User-Agent", "Mozilla/5.0")
		ctx := e.NewContext(req, httptest.NewRecorder())

		// A safe but non-canonical token (an alternate or older client may send one)
		// must still identify a distinct client; otherwise concurrent consumers on
		// the same host collapse to one client ID and tear down each other's shared
		// stream.
		safeSession := "client-1712345678901-abc123xyz"
		clientID := c.resolveClientID(ctx, safeSession)
		assert.Contains(t, clientID, "192.168.1.100")
		assert.Contains(t, clientID, safeSession)
		assert.NotContains(t, clientID, "Browser")
	})

	t.Run("session ID matching a fallback client type does not collide", func(t *testing.T) {
		t.Parallel()
		// A crafted session ID equal to generateClientID's fallback type string
		// (e.g. "HLSPlayer", which is itself a safe token) must not resolve to the
		// same client ID as an unrecognized-UA client with no session on the same
		// IP. The session namespace prefix keeps the two identity spaces disjoint.
		c := &Handler{Core: &apicore.Core{}}
		c.Settings.Store(apitest.NewValidTestSettings())
		e := echo.New()

		reqSession := httptest.NewRequest(http.MethodPost, "/", http.NoBody)
		reqSession.RemoteAddr = testRemoteAddr
		reqSession.Header.Set("User-Agent", "curl/8.0") // unrecognized UA
		ctxSession := e.NewContext(reqSession, httptest.NewRecorder())

		reqFallback := httptest.NewRequest(http.MethodPost, "/", http.NoBody)
		reqFallback.RemoteAddr = testRemoteAddr          // same IP
		reqFallback.Header.Set("User-Agent", "curl/8.0") // same unrecognized UA -> "HLSPlayer" type
		ctxFallback := e.NewContext(reqFallback, httptest.NewRecorder())

		sessionID := c.resolveClientID(ctxSession, "HLSPlayer")
		fallbackID := c.resolveClientID(ctxFallback, "")
		assert.NotEqual(t, sessionID, fallbackID)
	})

	t.Run("rejects unsafe or malformed session ID", func(t *testing.T) {
		t.Parallel()
		c := &Handler{Core: &apicore.Core{}}
		c.Settings.Store(apitest.NewValidTestSettings())
		e := echo.New()

		for _, unsafe := range []string{
			"short",                // below minimum length
			"has spaces here",      // whitespace not allowed
			"path/../traversal",    // slashes not allowed
			"semi;colon;injection", // punctuation not allowed
			strings.Repeat("a", hlsSessionIDMaxLen+1), // above maximum length
		} {
			req := httptest.NewRequest(http.MethodPost, "/", http.NoBody)
			req.RemoteAddr = testRemoteAddr
			req.Header.Set("User-Agent", "Mozilla/5.0")
			ctx := e.NewContext(req, httptest.NewRecorder())

			clientID := c.resolveClientID(ctx, unsafe)
			// Should fall back to IP+UA-based ID and never echo the unsafe token.
			assert.Contains(t, clientID, "Browser", "input %q should fall back", unsafe)
			assert.NotContains(t, clientID, unsafe)
		}
	})
}

// TestIsSafeSessionID tests the isSafeSessionID validation helper directly.
func TestIsSafeSessionID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		sessionID string
		want      bool
	}{
		{name: "canonical uuid", sessionID: "550e8400-e29b-41d4-a716-446655440000", want: true},
		{name: "fallback token", sessionID: "fallback-1712345678901-abc123xyz", want: true},
		{name: "alphanumeric with dot and underscore", sessionID: "a.b_c-1234", want: true},
		{name: "uppercase and mixed case", sessionID: "ABCdef-1234", want: true},
		{name: "minimum length", sessionID: strings.Repeat("a", hlsSessionIDMinLen), want: true},
		{name: "maximum length", sessionID: strings.Repeat("a", hlsSessionIDMaxLen), want: true},
		{name: "empty", sessionID: "", want: false},
		{name: "one below minimum length", sessionID: strings.Repeat("a", hlsSessionIDMinLen-1), want: false},
		{name: "one above maximum length", sessionID: strings.Repeat("a", hlsSessionIDMaxLen+1), want: false},
		{name: "space", sessionID: "abc def ghi", want: false},
		{name: "slash", sessionID: "abc/def/ghi", want: false},
		{name: "semicolon", sessionID: "abc;def;ghi", want: false},
		{name: "plus and at signs", sessionID: "abc+def@ghi", want: false},
		{name: "non-ascii", sessionID: "abcdéfghij", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, isSafeSessionID(tt.sessionID))
		})
	}
}

// TestHLSConsumer_WriteDoesNotRetainFrameData verifies that hlsConsumer.Write
// copies the frame data before forwarding it on the channel, so the caller's
// buffer can be safely reused after Write returns. This is the correctness
// invariant that lets the audiocore router pool its output slices without
// racing against the feed loop's asynchronous reads.
func TestHLSConsumer_WriteDoesNotRetainFrameData(t *testing.T) {
	t.Parallel()

	ch := make(chan audioChunk, 1)
	h := &hlsConsumer{
		id:       "test",
		sourceID: "src",
		feed:     &audioFeed{ch: ch},
		rate:     48000,
		depth:    16,
		channels: 1,
	}

	captured := time.Date(2026, 7, 23, 9, 0, 0, 0, time.UTC)
	original := []byte{0x01, 0x02, 0x03, 0x04}
	frame := audiocore.AudioFrame{
		SourceID:   "src",
		Data:       original,
		SampleRate: 48000,
		BitDepth:   16,
		Channels:   1,
		Timestamp:  captured,
	}

	require.NoError(t, h.Write(frame))

	// Mutate the caller's slice after Write returns. If hlsConsumer retained
	// the slice header, the mutation would be visible on the channel.
	for i := range original {
		original[i] = 0xFF
	}

	select {
	case received := <-ch:
		assert.Equal(t, []byte{0x01, 0x02, 0x03, 0x04}, received.data,
			"hlsConsumer must copy frame.Data, not retain the caller's slice")
		assert.Equal(t, captured, received.timestamp,
			"the capture time must survive the hop to the feed loop; the native muxer anchors PDT to it")
	case <-time.After(time.Second):
		t.Fatal("no frame delivered to channel")
	}
}

// TestStartHLSStream_NoCaptureBuffer verifies that starting a live-audio (HLS)
// stream for a source that has no capture buffer returns a diagnostic 404 rather
// than the old opaque "Audio source not found" with a nil error (issue #3766).
// The handler must not panic while gathering diagnostics, whether or not other
// sources are registered, and the response must carry the improved user-facing
// message.
func TestStartHLSStream_NoCaptureBuffer(t *testing.T) {
	const (
		wantMsg  = "Audio source not available for live streaming"
		unknown  = "rtsp_unknown_source"
		startURL = "/api/v2/streams/hls/" + unknown + "/start"
	)

	// newHandlerWithEngine returns a Handler backed by a real (but idle) audio
	// engine. No capture buffers are ever allocated, so CaptureBuffer always misses.
	newHandlerWithEngine := func(t *testing.T) *Handler {
		t.Helper()
		eng := engine.New(t.Context(), &engine.Config{}, nil)
		t.Cleanup(eng.Stop)
		h := &Handler{Core: apitest.NewCore(t)}
		h.Engine.Store(eng)
		return h
	}

	callStart := func(t *testing.T, h *Handler) (*httptest.ResponseRecorder, error) {
		t.Helper()
		e := echo.New()
		req := httptest.NewRequest(http.MethodPost, startURL, http.NoBody)
		rec := httptest.NewRecorder()
		ctx := e.NewContext(req, rec)
		ctx.SetParamNames("sourceID")
		ctx.SetParamValues(unknown)
		return rec, h.StartHLSStream(ctx)
	}

	t.Run("empty engine returns diagnostic 404", func(t *testing.T) {
		h := newHandlerWithEngine(t)
		rec, err := callStart(t, h)
		apitest.AssertControllerError(t, err, rec, http.StatusNotFound, wantMsg)
	})

	t.Run("other registered sources still return diagnostic 404", func(t *testing.T) {
		h := newHandlerWithEngine(t)

		// Register a couple of sources so the diagnostics-gathering loop over the
		// registry actually runs. None of them have a capture buffer, matching the
		// #3766 scenario where a source is configured but not yet streamable.
		registry := h.Engine.Load().Registry()
		for _, cfg := range []*audiocore.SourceConfig{
			{ID: "rtsp_a", DisplayName: "Camera A", Type: audiocore.SourceTypeRTSP, ConnectionString: "rtsp://user:pass@cam-a.local/stream"},
			{ID: "card_b", DisplayName: "Backyard Mic", Type: audiocore.SourceTypeAudioCard, ConnectionString: "hw:0,0"},
		} {
			_, regErr := registry.Register(cfg)
			require.NoError(t, regErr)
		}

		rec, err := callStart(t, h)
		apitest.AssertControllerError(t, err, rec, http.StatusNotFound, wantMsg)
	})
}
