package audiocore

import (
	"fmt"
	"slices"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestRegistry creates a SourceRegistry with the package logger for tests.
func newTestRegistry(t *testing.T) *SourceRegistry {
	t.Helper()
	return NewSourceRegistry(GetLogger())
}

// TestSourceRegistry_RegisterAndGet verifies that a registered source can be
// retrieved by both ID and connection string.
func TestSourceRegistry_RegisterAndGet(t *testing.T) {
	t.Parallel()
	r := newTestRegistry(t)

	cfg := &SourceConfig{
		ConnectionString: "rtsp://192.168.1.10/stream",
		SampleRate:       48000,
		BitDepth:         16,
		Channels:         1,
	}

	src, err := r.Register(cfg)
	require.NoError(t, err)
	require.NotNil(t, src)
	assert.NotEmpty(t, src.ID)
	assert.Equal(t, SourceTypeRTSP, src.Type)
	assert.Equal(t, 48000, src.SampleRate)
	assert.Equal(t, 16, src.BitDepth)
	assert.Equal(t, 1, src.Channels)

	// Retrieve by ID
	got, ok := r.Get(src.ID)
	require.True(t, ok)
	assert.Equal(t, src.ID, got.ID)

	// Retrieve by connection string
	gotByConn, ok := r.GetByConnection(cfg.ConnectionString)
	require.True(t, ok)
	assert.Equal(t, src.ID, gotByConn.ID)
}

// TestSourceRegistry_RegisterDuplicate verifies that registering the same
// connection string twice returns the existing source.
func TestSourceRegistry_RegisterDuplicate(t *testing.T) {
	t.Parallel()
	r := newTestRegistry(t)

	cfg := &SourceConfig{
		ConnectionString: "rtsp://192.168.1.20/cam1",
	}

	first, err := r.Register(cfg)
	require.NoError(t, err)

	second, err := r.Register(cfg)
	require.NoError(t, err)

	assert.Equal(t, first.ID, second.ID, "duplicate registration must return existing source")

	// Should still be only one source in the registry
	list := r.List()
	assert.Len(t, list, 1)
}

// TestSourceRegistry_Unregister verifies that a source can be removed.
func TestSourceRegistry_Unregister(t *testing.T) {
	t.Parallel()
	r := newTestRegistry(t)

	src, err := r.Register(&SourceConfig{ConnectionString: "rtsp://192.168.1.30/stream"})
	require.NoError(t, err)

	err = r.Unregister(src.ID)
	require.NoError(t, err)

	_, ok := r.Get(src.ID)
	assert.False(t, ok, "source should no longer exist after unregister")

	_, ok = r.GetByConnection("rtsp://192.168.1.30/stream")
	assert.False(t, ok, "connection map should be cleaned up after unregister")
}

// TestSourceRegistry_UnregisterNotFound verifies that unregistering a
// non-existent ID returns ErrSourceNotFound.
func TestSourceRegistry_UnregisterNotFound(t *testing.T) {
	t.Parallel()
	r := newTestRegistry(t)

	err := r.Unregister("nonexistent-id")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrSourceNotFound)
}

// TestSourceRegistry_List verifies that List returns all sources in sorted order.
func TestSourceRegistry_List(t *testing.T) {
	t.Parallel()
	r := newTestRegistry(t)

	conns := []string{
		"rtsp://cam3.example.com/stream",
		"rtsp://cam1.example.com/stream",
		"rtsp://cam2.example.com/stream",
	}
	for _, c := range conns {
		_, err := r.Register(&SourceConfig{ConnectionString: c})
		require.NoError(t, err)
	}

	list := r.List()
	assert.Len(t, list, 3)

	// Verify sorted by DisplayName
	names := make([]string, len(list))
	for i, s := range list {
		names[i] = s.DisplayName
	}
	assert.True(t, slices.IsSorted(names), "List() should return sources sorted by DisplayName")
}

// TestSourceRegistry_EventListeners verifies that SourceAdded and SourceRemoved
// events fire when a source is registered and unregistered.
func TestSourceRegistry_EventListeners(t *testing.T) {
	t.Parallel()
	r := newTestRegistry(t)

	var mu sync.Mutex
	var received []SourceEvent

	r.AddListener(func(e SourceEvent) {
		mu.Lock()
		defer mu.Unlock()
		received = append(received, e)
	})

	src, err := r.Register(&SourceConfig{ConnectionString: "rtsp://events.example.com/stream"})
	require.NoError(t, err)

	err = r.Unregister(src.ID)
	require.NoError(t, err)

	mu.Lock()
	defer mu.Unlock()

	require.Len(t, received, 2)
	assert.Equal(t, SourceAdded, received[0].Type)
	assert.Equal(t, src.ID, received[0].SourceID)
	assert.Equal(t, SourceRemoved, received[1].Type)
	assert.Equal(t, src.ID, received[1].SourceID)
}

// TestSourceRegistry_UpdateState verifies that UpdateState changes the source
// state and fires a SourceStateChanged event.
func TestSourceRegistry_UpdateState(t *testing.T) {
	t.Parallel()
	r := newTestRegistry(t)

	var mu sync.Mutex
	var received []SourceEvent

	r.AddListener(func(e SourceEvent) {
		mu.Lock()
		defer mu.Unlock()
		received = append(received, e)
	})

	src, err := r.Register(&SourceConfig{ConnectionString: "rtsp://state.example.com/stream"})
	require.NoError(t, err)

	err = r.UpdateState(src.ID, SourceRunning)
	require.NoError(t, err)

	// Verify state was updated
	got, ok := r.Get(src.ID)
	require.True(t, ok)
	assert.Equal(t, SourceRunning, got.State)

	mu.Lock()
	defer mu.Unlock()

	// First event is SourceAdded, second is SourceStateChanged
	require.Len(t, received, 2)
	assert.Equal(t, SourceStateChanged, received[1].Type)
	assert.Equal(t, src.ID, received[1].SourceID)
}

// TestSourceRegistry_UpdateState_NotFound verifies that UpdateState returns
// ErrSourceNotFound for an unknown ID.
func TestSourceRegistry_UpdateState_NotFound(t *testing.T) {
	t.Parallel()
	r := newTestRegistry(t)

	err := r.UpdateState("nonexistent-id", SourceRunning)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrSourceNotFound)
}

// TestSourceRegistry_ConcurrentAccess verifies the registry is safe under
// concurrent goroutines with the race detector.
func TestSourceRegistry_ConcurrentAccess(t *testing.T) {
	t.Parallel()
	r := newTestRegistry(t)

	const goroutines = 20
	var wg sync.WaitGroup

	for i := range goroutines {
		wg.Go(func() {
			conn := fmt.Sprintf("rtsp://cam%d.example.com/stream", i)
			src, err := r.Register(&SourceConfig{ConnectionString: conn})
			if err != nil {
				return
			}
			_ = r.List()
			_, _ = r.Get(src.ID)
			_, _ = r.GetByConnection(conn)
		})
	}
	wg.Wait()

	list := r.List()
	assert.LessOrEqual(t, len(list), goroutines)
}

// TestSourceRegistry_RegisterCopiesGain verifies that the Gain field from SourceConfig
// is correctly copied into the registered AudioSource.
func TestSourceRegistry_RegisterCopiesGain(t *testing.T) {
	t.Parallel()
	r := newTestRegistry(t)

	cfg := &SourceConfig{
		ConnectionString: "hw:1,0",
		Type:             SourceTypeAudioCard,
		SampleRate:       48000,
		BitDepth:         16,
		Channels:         1,
		Gain:             6.5,
	}

	src, err := r.Register(cfg)
	require.NoError(t, err)
	require.NotNil(t, src)
	assert.InDelta(t, 6.5, src.Gain, 1e-9, "registered AudioSource.Gain must match SourceConfig.Gain")
}

// TestSourceRegistry_RegisterDefaultGain verifies that Gain defaults to 0 when not set.
func TestSourceRegistry_RegisterDefaultGain(t *testing.T) {
	t.Parallel()
	r := newTestRegistry(t)

	cfg := &SourceConfig{
		ConnectionString: "rtsp://192.168.1.50/stream",
		SampleRate:       48000,
		BitDepth:         16,
		Channels:         1,
	}

	src, err := r.Register(cfg)
	require.NoError(t, err)
	require.NotNil(t, src)
	assert.InDelta(t, 0.0, src.Gain, 1e-9, "Gain should default to 0 when not specified")
}

// TestSourceRegistry_TypeDetection verifies that source types are detected from connection strings.
func TestSourceRegistry_TypeDetection(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		conn     string
		wantType SourceType
	}{
		{"RTSP", "rtsp://192.168.1.1/stream", SourceTypeRTSP},
		{"RTSPS", "rtsps://192.168.1.1/stream", SourceTypeRTSP},
		{"HTTP", "http://example.com/audio.mp3", SourceTypeHTTP},
		{"HTTPS", "https://example.com/audio.mp3", SourceTypeHTTP},
		{"HLS", "https://example.com/playlist.m3u8", SourceTypeHLS},
		{"RTMP", "rtmp://rtmp.example.com/live", SourceTypeRTMP},
		{"UDP", "udp://239.0.0.1:1234", SourceTypeUDP},
		{"AudioCard", "hw:0,0", SourceTypeAudioCard},
		{"Default", "default", SourceTypeAudioCard},
		{"File", "/path/to/audio.wav", SourceTypeFile},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r := newTestRegistry(t)
			src, err := r.Register(&SourceConfig{ConnectionString: tt.conn})
			require.NoError(t, err)
			assert.Equal(t, tt.wantType, src.Type)
		})
	}
}
