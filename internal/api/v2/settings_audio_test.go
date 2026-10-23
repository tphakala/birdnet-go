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

// syncNamesEntry is one named audio endpoint for the rename detector tables:
// conn is the sound card device or the stream URL, whichever the kind keys on.
type syncNamesEntry struct {
	name string
	conn string
}

// TestSyncNames covers the rename detectors for sound card sources and RTSP
// streams. They share one contract: a rename is detected by matching the old and
// new entry on the connection (device or URL), so it survives a reordered list
// and an added entry, and a change of the connection itself is not a rename.
func TestSyncNames(t *testing.T) {
	t.Parallel()

	kinds := []struct {
		name string
		// build returns settings holding the entries as sources or streams.
		build func(entries []syncNamesEntry) *conf.Settings
		sync  func(old, cur *conf.Settings) bool
	}{
		{
			name: "audio sources",
			build: func(entries []syncNamesEntry) *conf.Settings {
				s := &conf.Settings{}
				for _, en := range entries {
					s.Realtime.Audio.Sources = append(s.Realtime.Audio.Sources,
						conf.AudioSourceConfig{Name: en.name, Device: en.conn})
				}
				return s
			},
			sync: func(old, cur *conf.Settings) bool { return syncAudioSourceNames(old, cur, nil) },
		},
		{
			name: "streams",
			build: func(entries []syncNamesEntry) *conf.Settings {
				s := &conf.Settings{}
				for _, en := range entries {
					s.Realtime.RTSP.Streams = append(s.Realtime.RTSP.Streams,
						conf.StreamConfig{Name: en.name, URL: en.conn})
				}
				return s
			},
			sync: func(old, cur *conf.Settings) bool { return syncStreamNames(old, cur, nil) },
		},
	}

	tests := []struct {
		name string
		old  []syncNamesEntry
		cur  []syncNamesEntry
		want bool
	}{
		{
			name: "same names",
			old:  []syncNamesEntry{{"Front Yard", "conn-a"}},
			cur:  []syncNamesEntry{{"Front Yard", "conn-a"}},
			want: false,
		},
		{
			name: "name changed on the same connection",
			old:  []syncNamesEntry{{"Front Yard", "conn-a"}},
			cur:  []syncNamesEntry{{"Garden Mic", "conn-a"}},
			want: true,
		},
		{
			// A changed connection is handled by reconfiguration, not a rename.
			name: "name and connection both changed",
			old:  []syncNamesEntry{{"Front Yard", "conn-a"}},
			cur:  []syncNamesEntry{{"Garden Mic", "conn-b"}},
			want: false,
		},
		{
			name: "rename with a new entry added",
			old:  []syncNamesEntry{{"A", "conn-a"}},
			cur:  []syncNamesEntry{{"A-renamed", "conn-a"}, {"B", "conn-b"}},
			want: true,
		},
		{
			name: "rename with the entries reordered",
			old:  []syncNamesEntry{{"Mic A", "conn-a"}, {"Mic B", "conn-b"}},
			cur:  []syncNamesEntry{{"Mic B", "conn-b"}, {"Garden Mic", "conn-a"}},
			want: true,
		},
		{
			name: "both empty",
			want: false,
		},
	}

	for _, kind := range kinds {
		t.Run(kind.name, func(t *testing.T) {
			t.Parallel()
			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					t.Parallel()
					assert.Equal(t, tt.want, kind.sync(kind.build(tt.old), kind.build(tt.cur)))
				})
			}
		})
	}
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
