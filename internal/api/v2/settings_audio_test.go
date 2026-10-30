package api

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/audiocore"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/notification"
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

func TestEqualizerSaveToast(t *testing.T) {
	t.Parallel()

	const (
		batRate      = 256000
		notchHz      = 60000
		notchWidthHz = 500
		audibleHz    = 1000
		camURL       = "rtsp://cam.example/stream"
	)
	notch := func(hz float64) conf.EqualizerSettings {
		return conf.EqualizerSettings{
			Enabled: true,
			Filters: []conf.EqualizerFilter{{Type: "BandReject", Frequency: hz, Width: notchWidthHz, Passes: 1}},
		}
	}
	notchPtr := func(hz float64) *conf.EqualizerSettings {
		eq := notch(hz)
		return &eq
	}
	cards := func(birdRate int) []conf.AudioSourceConfig {
		return []conf.AudioSourceConfig{
			{Name: "Bird", Device: "hw:0,0", SampleRate: birdRate},
			{Name: "Bat", Device: "hw:1,0", SampleRate: batRate},
		}
	}
	settings := func(global conf.EqualizerSettings, sources []conf.AudioSourceConfig, streams ...conf.StreamConfig) *conf.Settings {
		s := &conf.Settings{}
		s.Realtime.Audio.Equalizer = global
		s.Realtime.Audio.Sources = sources
		s.Realtime.RTSP.Streams = streams
		return s
	}
	camRegistry := func(rate int) *fakeRegistry {
		return &fakeRegistry{byConn: map[string]*audiocore.AudioSource{
			camURL: {SampleRate: rate},
		}}
	}

	tests := []struct {
		name        string
		old, cur    *conf.Settings
		registry    *fakeRegistry
		nilRegistry bool
		wantWarning bool
		wantCount   int
		wantSources string
	}{
		{
			name: "audible notch on every card saves cleanly",
			old:  settings(conf.EqualizerSettings{}, cards(0)),
			cur:  settings(notch(audibleHz), cards(0)),
		},
		{
			name:        "global ultrasonic notch names the card that runs at the default rate",
			old:         settings(conf.EqualizerSettings{}, cards(0)),
			cur:         settings(notch(notchHz), cards(0)),
			wantWarning: true, wantCount: 1, wantSources: "Bird",
		},
		{
			name: "raising a card rate in the same save uses the new rate",
			old:  settings(conf.EqualizerSettings{}, cards(48000)),
			cur: func() *conf.Settings {
				c := cards(batRate)
				c[0].Equalizer = notchPtr(notchHz)
				return settings(conf.EqualizerSettings{}, c)
			}(),
			registry: camRegistry(48000),
		},
		{
			name: "a removed source is not reported",
			old:  settings(conf.EqualizerSettings{}, cards(0)),
			cur:  settings(notch(notchHz), cards(0)[1:]),
		},
		{
			name: "per-source override replaces a global ultrasonic notch",
			old:  settings(conf.EqualizerSettings{}, cards(0)),
			cur: func() *conf.Settings {
				c := cards(0)
				c[0].Equalizer = notchPtr(audibleHz)
				return settings(notch(notchHz), c)
			}(),
		},
		{
			name: "per-source override on a default-rate card is judged at that rate",
			old:  settings(conf.EqualizerSettings{}, cards(0)),
			cur: func() *conf.Settings {
				c := cards(0)
				c[0].Equalizer = notchPtr(notchHz)
				return settings(conf.EqualizerSettings{}, c)
			}(),
			wantWarning: true, wantCount: 1, wantSources: "Bird",
		},
		{
			name: "several skipping cards are named in config order",
			old:  settings(conf.EqualizerSettings{}, cards(0)),
			cur: settings(notch(notchHz), []conf.AudioSourceConfig{
				{Name: "Bird", Device: "hw:0,0"},
				{Name: "Bat", Device: "hw:1,0", SampleRate: batRate},
				{Name: "Yard", Device: "hw:2,0", SampleRate: 96000},
			}),
			wantWarning: true, wantCount: 2, wantSources: "Bird, Yard",
		},
		{
			name: "card without a device is not reported",
			old:  settings(conf.EqualizerSettings{}, cards(0)),
			cur: func() *conf.Settings {
				c := cards(0)
				c[0].Device = " "
				return settings(notch(notchHz), c[:1])
			}(),
		},
		{
			name: "stream with a new URL is left out even when the registry knows it",
			old:  settings(conf.EqualizerSettings{}, nil, conf.StreamConfig{Name: "Cam", URL: "rtsp://old.example/stream", Enabled: true}),
			cur: settings(conf.EqualizerSettings{}, nil, conf.StreamConfig{
				Name: "Cam", URL: camURL, Enabled: true, Equalizer: notchPtr(notchHz),
			}),
			registry: camRegistry(48000),
		},
		{
			name: "disabled stream is not reported",
			old:  settings(conf.EqualizerSettings{}, nil, conf.StreamConfig{Name: "Cam", URL: camURL, Enabled: true}),
			cur: settings(conf.EqualizerSettings{}, nil, conf.StreamConfig{
				Name: "Cam", URL: camURL, Enabled: false, Equalizer: notchPtr(notchHz),
			}),
			registry: camRegistry(48000),
		},
		{
			name: "stream without a URL is not reported",
			old:  settings(conf.EqualizerSettings{}, nil, conf.StreamConfig{Name: "Cam", Enabled: true}),
			cur: settings(conf.EqualizerSettings{}, nil, conf.StreamConfig{
				Name: "Cam", Enabled: true, Equalizer: notchPtr(notchHz),
			}),
			registry: &fakeRegistry{byConn: map[string]*audiocore.AudioSource{"": {SampleRate: 48000}}},
		},
		{
			name: "without an engine streams are left out",
			old:  settings(conf.EqualizerSettings{}, nil, conf.StreamConfig{Name: "Cam", URL: camURL, Enabled: true}),
			cur: settings(conf.EqualizerSettings{}, nil, conf.StreamConfig{
				Name: "Cam", URL: camURL, Enabled: true, Equalizer: notchPtr(notchHz),
			}),
			nilRegistry: true,
		},
		{
			name: "stream with an unchanged URL and models uses its registered rate",
			old:  settings(conf.EqualizerSettings{}, nil, conf.StreamConfig{Name: "Cam", URL: camURL, Enabled: true, Models: []string{"birdnet"}}),
			cur: settings(conf.EqualizerSettings{}, nil, conf.StreamConfig{
				Name: "Cam", URL: camURL, Enabled: true, Models: []string{"birdnet"}, Equalizer: notchPtr(notchHz),
			}),
			registry:    camRegistry(48000),
			wantWarning: true, wantCount: 1, wantSources: "Cam",
		},
		{
			name: "stream whose models change in the same save is left out",
			old:  settings(conf.EqualizerSettings{}, nil, conf.StreamConfig{Name: "Cam", URL: camURL, Enabled: true, Models: []string{"birdnet"}}),
			cur: settings(conf.EqualizerSettings{}, nil, conf.StreamConfig{
				Name: "Cam", URL: camURL, Enabled: true, Models: []string{"bat"}, Equalizer: notchPtr(notchHz),
			}),
			registry: camRegistry(48000),
		},
		{
			name: "disabled equalizer holding an ultrasonic filter saves cleanly",
			old:  settings(conf.EqualizerSettings{}, cards(0)),
			cur: func() *conf.Settings {
				eq := notch(notchHz)
				eq.Enabled = false
				return settings(eq, cards(0))
			}(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var registry streamRateLookup
			switch {
			case tt.nilRegistry:
			case tt.registry != nil:
				registry = tt.registry
			default:
				registry = &fakeRegistry{}
			}
			_, toastType, _, key, params := equalizerSaveToast(equalizerSkipReport(tt.old, tt.cur, registry))
			if !tt.wantWarning {
				assert.Equal(t, ToastTypeSuccess, toastType)
				assert.Equal(t, notification.MsgSettingsEqualizerUpdated, key)
				assert.Nil(t, params)
				return
			}
			assert.Equal(t, ToastTypeWarning, toastType)
			assert.Equal(t, notification.MsgSettingsEqualizerFiltersSkipped, key)
			assert.Equal(t, map[string]any{"count": tt.wantCount, "sources": tt.wantSources}, params)
		})
	}
}

// TestNewlySkippedSources pins the warning for a save that changes no
// equalizer setting: only sources that start skipping filters because of the
// save are reported, and ones that already skipped are not repeated.
func TestNewlySkippedSources(t *testing.T) {
	t.Parallel()

	const (
		batRate = 256000
		notchHz = 60000
	)
	withRate := func(rate int) *conf.Settings {
		s := &conf.Settings{}
		s.Realtime.Audio.Equalizer = conf.EqualizerSettings{
			Enabled: true,
			Filters: []conf.EqualizerFilter{{Type: "BandReject", Frequency: notchHz, Width: 500, Passes: 1}},
		}
		s.Realtime.Audio.Sources = []conf.AudioSourceConfig{{Name: "Bat", Device: "hw:1,0", SampleRate: rate}}
		return s
	}
	report := func(s *conf.Settings) []eqSkippedSource {
		return equalizerSkipReport(s, s, nil)
	}

	tests := []struct {
		name     string
		old, cur *conf.Settings
		want     string
	}{
		{"lowering the rate below the notch reports the card", withRate(batRate), withRate(0), "Bat"},
		{"keeping the rate reports nothing", withRate(batRate), withRate(batRate), ""},
		{"raising the rate reports nothing", withRate(0), withRate(batRate), ""},
		{"a card that already skipped is not repeated", withRate(0), withRate(48000), ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			added := newlySkippedSources(report(tt.old), report(tt.cur))
			if tt.want == "" {
				assert.Empty(t, added)
				return
			}
			require.Len(t, added, 1)
			assert.Equal(t, tt.want, added[0].name)
		})
	}
}

// TestHandleAudioSettingsChanges_EqualizerToastKey pins which equalizer toast a
// save sends: the warning when a rate-only save makes a saved filter skip, the
// success toast when an equalizer setting changed and every filter applies, and
// none when nothing about the equalizer or its sources changed.
func TestHandleAudioSettingsChanges_EqualizerToastKey(t *testing.T) {
	t.Parallel()

	const (
		batRate = 256000
		notchHz = 60000
	)
	card := func(rate int, notch bool) *conf.Settings {
		s := &conf.Settings{}
		s.Realtime.Audio.Sources = []conf.AudioSourceConfig{{Name: "Bat", Device: "hw:1,0", SampleRate: rate}}
		if notch {
			s.Realtime.Audio.Equalizer = conf.EqualizerSettings{
				Enabled: true,
				Filters: []conf.EqualizerFilter{{Type: "BandReject", Frequency: notchHz, Width: 500, Passes: 1}},
			}
		}
		return s
	}

	tests := []struct {
		name     string
		old, cur *conf.Settings
		wantKey  string
	}{
		{"rate-only save that newly skips a filter warns", card(batRate, true), card(0, true), notification.MsgSettingsEqualizerFiltersSkipped},
		{"adding a filter that applies reports success", card(batRate, false), card(batRate, true), notification.MsgSettingsEqualizerUpdated},
		{"rate-only save that skips nothing sends no equalizer toast", card(batRate, true), card(batRate, true), ""},
		{"a rename in the same save sends no warning", card(0, true), func() *conf.Settings {
			s := card(0, true)
			s.Realtime.Audio.Sources[0].Name = "Renamed"
			s.Realtime.Audio.Sources[0].Models = []string{"perch_v2"}
			return s
		}(), ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c, service := newToastTestController(t)
			ch, _ := service.Subscribe()
			defer service.Unsubscribe(ch)

			_, err := c.handleAudioSettingsChanges(tt.old, tt.cur)
			require.NoError(t, err)

			var keys []string
			for drained := false; !drained; {
				select {
				case n := <-ch:
					keys = append(keys, n.MessageKey)
				default:
					drained = true
				}
			}
			eqKeys := slices.DeleteFunc(keys, func(k string) bool {
				return k != notification.MsgSettingsEqualizerFiltersSkipped && k != notification.MsgSettingsEqualizerUpdated
			})
			if tt.wantKey == "" {
				assert.Empty(t, eqKeys)
				return
			}
			assert.Equal(t, []string{tt.wantKey}, eqKeys)
		})
	}
}
