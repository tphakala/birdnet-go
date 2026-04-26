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
