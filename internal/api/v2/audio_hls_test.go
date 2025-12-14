// internal/api/v2/audio_hls_test.go
// Tests for HLS streaming endpoint functionality
package api

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
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
			PlaylistURL:   "/api/v2/streams/hls/abc123/playlist.m3u8",
			ActiveClients: 0,
			PlaylistReady: false,
		}

		assert.Equal(t, "test_source", status.Source)
		assert.Contains(t, status.PlaylistURL, "playlist.m3u8")
		assert.Equal(t, "starting", status.Status)
		assert.False(t, status.PlaylistReady)
	})

	t.Run("ready status with clients", func(t *testing.T) {
		status := HLSStreamStatus{
			Status:        "ready",
			Source:        "rtsp%3A%2F%2Fcamera.local%2Fstream", // URL-encoded source
			PlaylistURL:   "/api/v2/streams/hls/def456/playlist.m3u8",
			ActiveClients: 2,
			PlaylistReady: true,
		}

		assert.Equal(t, "ready", status.Status)
		assert.True(t, status.PlaylistReady)
		assert.Equal(t, 2, status.ActiveClients)
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

// TestHLSHeartbeatRequest tests the heartbeat request struct
func TestHLSHeartbeatRequest(t *testing.T) {
	t.Run("heartbeat with source only", func(t *testing.T) {
		req := HLSHeartbeatRequest{
			SourceID: "test_source",
		}

		assert.Equal(t, "test_source", req.SourceID)
		assert.Empty(t, req.ClientID)
	})

	t.Run("heartbeat with client id", func(t *testing.T) {
		req := HLSHeartbeatRequest{
			SourceID: "test_source",
			ClientID: "client_123",
		}

		assert.Equal(t, "test_source", req.SourceID)
		assert.Equal(t, "client_123", req.ClientID)
	})
}
