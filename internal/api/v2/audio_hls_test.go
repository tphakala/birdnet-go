// internal/api/v2/audio_hls_test.go
// Tests for HLS streaming endpoint functionality
package api

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestHLSStreamInfoStruct tests the HLSStreamInfo struct
func TestHLSStreamInfoStruct(t *testing.T) {
	t.Run("new stream info has expected fields", func(t *testing.T) {
		info := &HLSStreamInfo{
			SourceID:     "test_source",
			OutputDir:    "/tmp/hls/stream_test",
			PlaylistPath: "/tmp/hls/stream_test/playlist.m3u8",
			FifoPipe:     "/tmp/hls/stream_test/audio.pcm",
		}

		assert.Equal(t, "test_source", info.SourceID)
		assert.Equal(t, "/tmp/hls/stream_test", info.OutputDir)
		assert.Contains(t, info.PlaylistPath, "playlist.m3u8")
		assert.Contains(t, info.FifoPipe, "audio.pcm")
	})
}

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
			SourceID:  testStreamID,
			OutputDir: "/tmp/test",
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
	const testRemoteAddr = "192.168.1.100:12345"

	t.Run("prefers session ID when provided", func(t *testing.T) {
		c := &Controller{Settings: newValidTestSettings()}
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
		c := &Controller{Settings: newValidTestSettings()}
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
		c := &Controller{Settings: newValidTestSettings()}
		e := echo.New()

		req1 := httptest.NewRequest(http.MethodPost, "/", http.NoBody)
		req1.RemoteAddr = testRemoteAddr
		ctx1 := e.NewContext(req1, httptest.NewRecorder())

		req2 := httptest.NewRequest(http.MethodPost, "/", http.NoBody)
		req2.RemoteAddr = testRemoteAddr // Same IP, same port — session ID differentiates
		ctx2 := e.NewContext(req2, httptest.NewRecorder())

		id1 := c.resolveClientID(ctx1, "550e8400-e29b-41d4-a716-446655440000")
		id2 := c.resolveClientID(ctx2, "660e8400-e29b-41d4-a716-446655440000")
		assert.NotEqual(t, id1, id2)
	})

	t.Run("rejects invalid session ID format", func(t *testing.T) {
		c := &Controller{Settings: newValidTestSettings()}
		e := echo.New()
		req := httptest.NewRequest(http.MethodPost, "/", http.NoBody)
		req.RemoteAddr = testRemoteAddr
		req.Header.Set("User-Agent", "Mozilla/5.0")
		ctx := e.NewContext(req, httptest.NewRecorder())

		// Non-UUID session ID should be rejected
		clientID := c.resolveClientID(ctx, "not-a-valid-uuid")
		// Should fall back to IP+UA-based ID
		assert.Contains(t, clientID, "Browser")
		assert.NotContains(t, clientID, "not-a-valid-uuid")
	})
}
