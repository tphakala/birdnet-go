package apicore

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSSEManager_BroadcastDetection_StreamTypeIsolation(t *testing.T) {
	t.Parallel()

	m := NewSSEManager()

	soundClient := &SSEClient{
		ID:             "client-sound-only",
		Channel:        make(chan SSEDetectionData, 1),
		SoundLevelChan: make(chan SSESoundLevelData, 1),
		Done:           make(chan struct{}),
		StreamType:     StreamTypeSoundLevels,
	}
	detClient := &SSEClient{
		ID:         "client-det-only",
		Channel:    make(chan SSEDetectionData, 100),
		Done:       make(chan struct{}),
		StreamType: StreamTypeDetections,
	}

	require.True(t, m.AddClient(soundClient))
	require.True(t, m.AddClient(detClient))

	// Broadcast more detections than the buffer size + drop threshold
	for i := range maxConsecutiveDrops + 2 {
		m.BroadcastDetection(&SSEDetectionData{ID: uint(i)})
	}

	// Wait briefly for potential asynchronous eviction logic (if any existed, though RemoveClient is synchronous here)
	time.Sleep(10 * time.Millisecond)

	// Sound-level client should STILL be connected because it ignored the detection broadcast
	assert.Equal(t, 2, m.GetClientCount(), "Sound-level client should not be disconnected by detection broadcast")

	m.mutex.RLock()
	_, ok := m.clients[soundClient.ID]
	m.mutex.RUnlock()
	assert.True(t, ok, "sound client should remain in map")
}
