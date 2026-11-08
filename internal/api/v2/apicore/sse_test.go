package apicore

import (
	"testing"

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

	// Sound-level client should STILL be connected because it ignored the detection broadcast
	assert.Equal(t, 2, m.GetClientCount(), "Sound-level client should not be disconnected by detection broadcast")

	m.mutex.RLock()
	_, ok := m.clients[soundClient.ID]
	m.mutex.RUnlock()
	assert.True(t, ok, "sound client should remain in map")
}

// newTestSSEClient returns a client that owns all three data channels, each
// buffered for one item, so a misrouted broadcast is visible in any of them.
func newTestSSEClient(id, streamType string) *SSEClient {
	return &SSEClient{
		ID:             id,
		Channel:        make(chan SSEDetectionData, 1),
		SoundLevelChan: make(chan SSESoundLevelData, 1),
		PendingChan:    make(chan any, 1),
		Done:           make(chan struct{}),
		StreamType:     streamType,
	}
}

// TestSSEManager_Broadcast_RoutesByStreamType pins which stream types receive
// which broadcast. Every client owns all three channels, so a broadcast that
// ignores the stream type shows up as an item in a channel that must stay empty.
func TestSSEManager_Broadcast_RoutesByStreamType(t *testing.T) {
	t.Parallel()

	type delivery struct{ detection, soundLevel, pending bool }

	broadcasts := []struct {
		name string
		send func(m *SSEManager)
		want map[string]delivery
	}{
		{
			name: "detection",
			send: func(m *SSEManager) { m.BroadcastDetection(&SSEDetectionData{ID: 1}) },
			want: map[string]delivery{
				StreamTypeDetections:  {detection: true},
				StreamTypeSoundLevels: {},
				StreamTypeAll:         {detection: true},
			},
		},
		{
			name: "sound level",
			send: func(m *SSEManager) { m.BroadcastSoundLevel(&SSESoundLevelData{EventType: "sound_level"}) },
			want: map[string]delivery{
				StreamTypeDetections:  {},
				StreamTypeSoundLevels: {soundLevel: true},
				StreamTypeAll:         {soundLevel: true},
			},
		},
		{
			name: "pending",
			send: func(m *SSEManager) { m.BroadcastPending("snapshot") },
			want: map[string]delivery{
				StreamTypeDetections:  {pending: true},
				StreamTypeSoundLevels: {},
				StreamTypeAll:         {pending: true},
			},
		},
	}

	for _, bc := range broadcasts {
		t.Run(bc.name, func(t *testing.T) {
			t.Parallel()

			m := NewSSEManager()
			clients := make(map[string]*SSEClient, len(bc.want))
			for streamType := range bc.want {
				c := newTestSSEClient(streamType, streamType)
				require.True(t, m.AddClient(c))
				clients[streamType] = c
			}

			bc.send(m)

			for streamType, want := range bc.want {
				c := clients[streamType]
				got := delivery{
					detection:  len(c.Channel) == 1,
					soundLevel: len(c.SoundLevelChan) == 1,
					pending:    len(c.PendingChan) == 1,
				}
				assert.Equal(t, want, got, "stream type %q", streamType)
			}
		})
	}
}

// TestSSEManager_BroadcastOverflow_OnlyDisconnectsTargetStreams checks that a
// sound level or pending broadcast never counts as a dropped message for a
// client of the other stream, even when that client's channel for the
// broadcast is full, while a stalled client of the target stream is removed
// after maxConsecutiveDrops.
func TestSSEManager_BroadcastOverflow_OnlyDisconnectsTargetStreams(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		target    string
		bystander string
		send      func(m *SSEManager)
	}{
		{
			name:      "sound level",
			target:    StreamTypeSoundLevels,
			bystander: StreamTypeDetections,
			send:      func(m *SSEManager) { m.BroadcastSoundLevel(&SSESoundLevelData{}) },
		},
		{
			name:      "pending",
			target:    StreamTypeDetections,
			bystander: StreamTypeSoundLevels,
			send:      func(m *SSEManager) { m.BroadcastPending("snapshot") },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			m := NewSSEManager()
			target := newTestSSEClient("target", tt.target)
			bystander := newTestSSEClient("bystander", tt.bystander)
			// Fill the channels the broadcasts write to on both clients, so a
			// delivery to the bystander would count as a dropped message.
			for _, c := range []*SSEClient{target, bystander} {
				c.SoundLevelChan <- SSESoundLevelData{}
				c.PendingChan <- "stale"
			}
			require.True(t, m.AddClient(target))
			require.True(t, m.AddClient(bystander))

			connected := func(id string) bool {
				m.mutex.RLock()
				defer m.mutex.RUnlock()
				_, ok := m.clients[id]
				return ok
			}

			for range maxConsecutiveDrops - 1 {
				tt.send(m)
			}
			assert.True(t, connected(target.ID), "a stalled client stays until the drop threshold is reached")

			tt.send(m)
			assert.False(t, connected(target.ID), "a stalled client of the target stream is removed at the threshold")
			assert.True(t, connected(bystander.ID), "a client of the other stream is left alone")
		})
	}
}
