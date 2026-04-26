package api

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/tphakala/birdnet-go/internal/conf"
)

func TestGetAudioBlockedFields(t *testing.T) {
	t.Parallel()

	blocked := getAudioBlockedFields()

	// FfmpegPath must be blocked to prevent ingress/proxy path contamination
	assert.Equal(t, true, blocked["FfmpegPath"], "FfmpegPath must be blocked from API updates")

	// SoxPath must be blocked for the same reason
	assert.Equal(t, true, blocked["SoxPath"], "SoxPath must be blocked from API updates")

	// SoxAudioTypes was already blocked
	assert.Equal(t, true, blocked["SoxAudioTypes"], "SoxAudioTypes must be blocked from API updates")
}

func TestPerStreamEqualizerChanged(t *testing.T) {
	t.Parallel()

	eq := &conf.EqualizerSettings{
		Enabled: true,
		Filters: []conf.EqualizerFilter{{Type: "HighPass", Frequency: 200}},
	}

	t.Run("no change", func(t *testing.T) {
		t.Parallel()
		old := &conf.Settings{}
		old.Realtime.RTSP.Streams = []conf.StreamConfig{
			{Name: "a", Equalizer: nil},
		}
		cur := &conf.Settings{}
		cur.Realtime.RTSP.Streams = []conf.StreamConfig{
			{Name: "a", Equalizer: nil},
		}
		assert.False(t, perStreamEqualizerChanged(old, cur))
	})

	t.Run("eq added", func(t *testing.T) {
		t.Parallel()
		old := &conf.Settings{}
		old.Realtime.RTSP.Streams = []conf.StreamConfig{
			{Name: "a", Equalizer: nil},
		}
		cur := &conf.Settings{}
		cur.Realtime.RTSP.Streams = []conf.StreamConfig{
			{Name: "a", Equalizer: eq},
		}
		assert.True(t, perStreamEqualizerChanged(old, cur))
	})

	t.Run("eq removed", func(t *testing.T) {
		t.Parallel()
		old := &conf.Settings{}
		old.Realtime.RTSP.Streams = []conf.StreamConfig{
			{Name: "a", Equalizer: eq},
		}
		cur := &conf.Settings{}
		cur.Realtime.RTSP.Streams = []conf.StreamConfig{
			{Name: "a", Equalizer: nil},
		}
		assert.True(t, perStreamEqualizerChanged(old, cur))
	})

	t.Run("stream count changed", func(t *testing.T) {
		t.Parallel()
		old := &conf.Settings{}
		old.Realtime.RTSP.Streams = []conf.StreamConfig{
			{Name: "a"},
		}
		cur := &conf.Settings{}
		cur.Realtime.RTSP.Streams = []conf.StreamConfig{
			{Name: "a"}, {Name: "b"},
		}
		assert.True(t, perStreamEqualizerChanged(old, cur))
	})

	t.Run("both empty", func(t *testing.T) {
		t.Parallel()
		old := &conf.Settings{}
		cur := &conf.Settings{}
		assert.False(t, perStreamEqualizerChanged(old, cur))
	})
}

//nolint:dupl // parallel test for audio sources mirrors stream test by design
func TestAudioSourceNameChanged(t *testing.T) {
	t.Parallel()

	t.Run("same names", func(t *testing.T) {
		t.Parallel()
		old := &conf.Settings{}
		old.Realtime.Audio.Sources = []conf.AudioSourceConfig{
			{Name: "Front Yard", Device: "hw:0,0"},
		}
		cur := &conf.Settings{}
		cur.Realtime.Audio.Sources = []conf.AudioSourceConfig{
			{Name: "Front Yard", Device: "hw:0,0"},
		}
		assert.False(t, audioSourceNameChanged(old, cur))
	})

	t.Run("name changed same device", func(t *testing.T) {
		t.Parallel()
		old := &conf.Settings{}
		old.Realtime.Audio.Sources = []conf.AudioSourceConfig{
			{Name: "Front Yard", Device: "hw:0,0"},
		}
		cur := &conf.Settings{}
		cur.Realtime.Audio.Sources = []conf.AudioSourceConfig{
			{Name: "Garden Mic", Device: "hw:0,0"},
		}
		assert.True(t, audioSourceNameChanged(old, cur))
	})

	t.Run("name and device both changed", func(t *testing.T) {
		t.Parallel()
		old := &conf.Settings{}
		old.Realtime.Audio.Sources = []conf.AudioSourceConfig{
			{Name: "Front Yard", Device: "hw:0,0"},
		}
		cur := &conf.Settings{}
		cur.Realtime.Audio.Sources = []conf.AudioSourceConfig{
			{Name: "Garden Mic", Device: "hw:1,0"},
		}
		assert.False(t, audioSourceNameChanged(old, cur),
			"should be false when device also changed (handled by reconfiguration)")
	})

	t.Run("length mismatch", func(t *testing.T) {
		t.Parallel()
		old := &conf.Settings{}
		old.Realtime.Audio.Sources = []conf.AudioSourceConfig{
			{Name: "A", Device: "hw:0,0"},
		}
		cur := &conf.Settings{}
		cur.Realtime.Audio.Sources = []conf.AudioSourceConfig{
			{Name: "A", Device: "hw:0,0"},
			{Name: "B", Device: "hw:1,0"},
		}
		assert.False(t, audioSourceNameChanged(old, cur),
			"should be false on length mismatch (handled by device change detection)")
	})

	t.Run("both empty", func(t *testing.T) {
		t.Parallel()
		old := &conf.Settings{}
		cur := &conf.Settings{}
		assert.False(t, audioSourceNameChanged(old, cur))
	})
}

//nolint:dupl // parallel test for streams mirrors audio source test by design
func TestStreamNameChanged(t *testing.T) {
	t.Parallel()

	t.Run("same names", func(t *testing.T) {
		t.Parallel()
		old := &conf.Settings{}
		old.Realtime.RTSP.Streams = []conf.StreamConfig{
			{Name: "Backyard Cam", URL: "rtsp://192.168.1.10/stream"},
		}
		cur := &conf.Settings{}
		cur.Realtime.RTSP.Streams = []conf.StreamConfig{
			{Name: "Backyard Cam", URL: "rtsp://192.168.1.10/stream"},
		}
		assert.False(t, streamNameChanged(old, cur))
	})

	t.Run("name changed same URL", func(t *testing.T) {
		t.Parallel()
		old := &conf.Settings{}
		old.Realtime.RTSP.Streams = []conf.StreamConfig{
			{Name: "Backyard Cam", URL: "rtsp://192.168.1.10/stream"},
		}
		cur := &conf.Settings{}
		cur.Realtime.RTSP.Streams = []conf.StreamConfig{
			{Name: "Garden Cam", URL: "rtsp://192.168.1.10/stream"},
		}
		assert.True(t, streamNameChanged(old, cur))
	})

	t.Run("name and URL both changed", func(t *testing.T) {
		t.Parallel()
		old := &conf.Settings{}
		old.Realtime.RTSP.Streams = []conf.StreamConfig{
			{Name: "Backyard Cam", URL: "rtsp://192.168.1.10/stream"},
		}
		cur := &conf.Settings{}
		cur.Realtime.RTSP.Streams = []conf.StreamConfig{
			{Name: "Garden Cam", URL: "rtsp://192.168.1.20/stream"},
		}
		assert.False(t, streamNameChanged(old, cur),
			"should be false when URL also changed (handled by reconfiguration)")
	})

	t.Run("length mismatch", func(t *testing.T) {
		t.Parallel()
		old := &conf.Settings{}
		old.Realtime.RTSP.Streams = []conf.StreamConfig{
			{Name: "A", URL: "rtsp://a/stream"},
		}
		cur := &conf.Settings{}
		cur.Realtime.RTSP.Streams = []conf.StreamConfig{
			{Name: "A", URL: "rtsp://a/stream"},
			{Name: "B", URL: "rtsp://b/stream"},
		}
		assert.False(t, streamNameChanged(old, cur),
			"should be false on length mismatch (handled by stream reconfiguration)")
	})

	t.Run("both empty", func(t *testing.T) {
		t.Parallel()
		old := &conf.Settings{}
		cur := &conf.Settings{}
		assert.False(t, streamNameChanged(old, cur))
	})
}
