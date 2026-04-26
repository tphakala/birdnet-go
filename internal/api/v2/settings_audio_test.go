package api

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/tphakala/birdnet-go/internal/audiocore"
	"github.com/tphakala/birdnet-go/internal/conf"
)

type fakeRegistry struct {
	byConn  map[string]*audiocore.AudioSource
	updates map[string]string
}

func (f *fakeRegistry) GetByConnection(c string) (*audiocore.AudioSource, bool) {
	s, ok := f.byConn[c]
	return s, ok
}

func (f *fakeRegistry) UpdateDisplayName(id, name string) bool {
	if f.updates == nil {
		f.updates = map[string]string{}
	}
	f.updates[id] = name
	return true
}

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
func TestSyncAudioSourceNames(t *testing.T) {
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
		assert.False(t, syncAudioSourceNames(old, cur, nil))
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
		assert.True(t, syncAudioSourceNames(old, cur, nil))
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
		assert.False(t, syncAudioSourceNames(old, cur, nil),
			"should be false when device also changed (handled by reconfiguration)")
	})

	t.Run("rename with new source added", func(t *testing.T) {
		t.Parallel()
		old := &conf.Settings{}
		old.Realtime.Audio.Sources = []conf.AudioSourceConfig{
			{Name: "A", Device: "hw:0,0"},
		}
		cur := &conf.Settings{}
		cur.Realtime.Audio.Sources = []conf.AudioSourceConfig{
			{Name: "A-renamed", Device: "hw:0,0"},
			{Name: "B", Device: "hw:1,0"},
		}
		assert.True(t, syncAudioSourceNames(old, cur, nil),
			"should detect rename even when a new source was added")
	})

	t.Run("reordered sources with rename", func(t *testing.T) {
		t.Parallel()
		old := &conf.Settings{}
		old.Realtime.Audio.Sources = []conf.AudioSourceConfig{
			{Name: "Mic A", Device: "hw:0,0"},
			{Name: "Mic B", Device: "hw:1,0"},
		}
		cur := &conf.Settings{}
		cur.Realtime.Audio.Sources = []conf.AudioSourceConfig{
			{Name: "Mic B", Device: "hw:1,0"},
			{Name: "Garden Mic", Device: "hw:0,0"},
		}
		assert.True(t, syncAudioSourceNames(old, cur, nil),
			"should detect rename even when sources are reordered")
	})

	t.Run("both empty", func(t *testing.T) {
		t.Parallel()
		old := &conf.Settings{}
		cur := &conf.Settings{}
		assert.False(t, syncAudioSourceNames(old, cur, nil))
	})
}

//nolint:dupl // parallel test for streams mirrors audio source test by design
func TestSyncStreamNames(t *testing.T) {
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
		assert.False(t, syncStreamNames(old, cur, nil))
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
		assert.True(t, syncStreamNames(old, cur, nil))
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
		assert.False(t, syncStreamNames(old, cur, nil),
			"should be false when URL also changed (handled by reconfiguration)")
	})

	t.Run("rename with new stream added", func(t *testing.T) {
		t.Parallel()
		old := &conf.Settings{}
		old.Realtime.RTSP.Streams = []conf.StreamConfig{
			{Name: "A", URL: "rtsp://a/stream"},
		}
		cur := &conf.Settings{}
		cur.Realtime.RTSP.Streams = []conf.StreamConfig{
			{Name: "A-renamed", URL: "rtsp://a/stream"},
			{Name: "B", URL: "rtsp://b/stream"},
		}
		assert.True(t, syncStreamNames(old, cur, nil),
			"should detect rename even when a new stream was added")
	})

	t.Run("reordered streams with rename", func(t *testing.T) {
		t.Parallel()
		old := &conf.Settings{}
		old.Realtime.RTSP.Streams = []conf.StreamConfig{
			{Name: "Stream A", URL: "rtsp://a/stream"},
			{Name: "Stream B", URL: "rtsp://b/stream"},
		}
		cur := &conf.Settings{}
		cur.Realtime.RTSP.Streams = []conf.StreamConfig{
			{Name: "Stream B", URL: "rtsp://b/stream"},
			{Name: "Garden Cam", URL: "rtsp://a/stream"},
		}
		assert.True(t, syncStreamNames(old, cur, nil),
			"should detect rename even when streams are reordered")
	})

	t.Run("both empty", func(t *testing.T) {
		t.Parallel()
		old := &conf.Settings{}
		cur := &conf.Settings{}
		assert.False(t, syncStreamNames(old, cur, nil))
	})
}

func TestResolveEQOverrideMatchesByName(t *testing.T) {
	t.Parallel()

	eq := &conf.EqualizerSettings{
		Enabled: true,
		Filters: []conf.EqualizerFilter{{Type: "HighPass", Frequency: 300, Q: 0.7}},
	}

	settings := &conf.Settings{}
	settings.Realtime.Audio.Sources = []conf.AudioSourceConfig{
		{Name: "New Name", Device: "hw:0,0", Equalizer: eq},
	}
	settings.Realtime.RTSP.Streams = []conf.StreamConfig{
		{Name: "Renamed Stream", URL: "rtsp://cam/stream", Equalizer: eq},
	}

	assert.Equal(t, eq, settings.ResolveEQOverride("New Name"),
		"EQ override should resolve for the new source name")
	assert.Nil(t, settings.ResolveEQOverride("Old Name"),
		"EQ override should not resolve for the old source name")

	assert.Equal(t, eq, settings.ResolveEQOverride("Renamed Stream"),
		"EQ override should resolve for the new stream name")
	assert.Nil(t, settings.ResolveEQOverride("Old Stream Name"),
		"EQ override should not resolve for the old stream name")
}

func TestSyncAudioSourceNames_UpdatesRegistry(t *testing.T) {
	t.Parallel()

	reg := &fakeRegistry{byConn: map[string]*audiocore.AudioSource{
		"hw:0,0": {ID: "src-1"},
	}}

	old := &conf.Settings{}
	old.Realtime.Audio.Sources = []conf.AudioSourceConfig{
		{Name: "Front Yard", Device: "hw:0,0"},
		{Name: "Side Mic", Device: "hw:1,0"},
	}
	cur := &conf.Settings{}
	cur.Realtime.Audio.Sources = []conf.AudioSourceConfig{
		{Name: "Garden Mic", Device: "hw:0,0"},
		{Name: "Side Renamed", Device: "hw:1,0"},
	}

	assert.True(t, syncAudioSourceNames(old, cur, reg))
	assert.Equal(t, map[string]string{"src-1": "Garden Mic"}, reg.updates,
		"should update only sources found in the registry")
}

func TestSyncStreamNames_UpdatesRegistry(t *testing.T) {
	t.Parallel()

	reg := &fakeRegistry{byConn: map[string]*audiocore.AudioSource{
		"rtsp://a/stream": {ID: "strm-1"},
	}}

	old := &conf.Settings{}
	old.Realtime.RTSP.Streams = []conf.StreamConfig{
		{Name: "Cam A", URL: "rtsp://a/stream"},
		{Name: "Cam B", URL: "rtsp://b/stream"},
	}
	cur := &conf.Settings{}
	cur.Realtime.RTSP.Streams = []conf.StreamConfig{
		{Name: "Garden Cam", URL: "rtsp://a/stream"},
		{Name: "Cam B Renamed", URL: "rtsp://b/stream"},
	}

	assert.True(t, syncStreamNames(old, cur, reg))
	assert.Equal(t, map[string]string{"strm-1": "Garden Cam"}, reg.updates,
		"should update only streams found in the registry")
}
