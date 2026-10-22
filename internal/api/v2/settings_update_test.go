package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/api/v2/apitest"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// TestDashboardLayoutWidthPersistence verifies that changing element width from
// "half" to "full" (omitted in JSON) correctly clears the old width value.
// Regression test for: json.Unmarshal reuses existing slice elements, so
// omitted fields (like width via omitempty) retained their old values.
func TestDashboardLayoutWidthPersistence(t *testing.T) {
	initialSettings := getTestSettings(t)
	initialSettings.Realtime.Dashboard.Layout = conf.DashboardLayout{
		Elements: []conf.DashboardElement{
			{ID: "daily-summary-0", Type: "daily-summary", Enabled: true},
			{ID: "currently-hearing-0", Type: "currently-hearing", Enabled: true, Width: "half"},
			{ID: "detections-grid-0", Type: "detections-grid", Enabled: true, Width: "half"},
		},
	}

	e := echo.New()
	controller := &Controller{Core: &apicore.Core{Echo: e}, controlChan: make(chan string, 10), DisableSaveSettings: true}
	controller.Settings.Store(initialSettings)

	// Simulate the frontend save: elements without width field (user set to full)
	update := map[string]any{
		"layout": map[string]any{
			"elements": []map[string]any{
				{"id": "daily-summary-0", "type": "daily-summary", "enabled": true},
				{"id": "currently-hearing-0", "type": "currently-hearing", "enabled": true},
				{"id": "detections-grid-0", "type": "detections-grid", "enabled": true},
			},
		},
	}

	body, err := json.Marshal(update)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPatch, "/api/v2/settings/dashboard", bytes.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)
	ctx.SetParamNames("section")
	ctx.SetParamValues("dashboard")

	err = controller.UpdateSectionSettings(ctx)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)

	// The width fields must be cleared (empty string = full width default)
	elements := controller.Settings.Load().Realtime.Dashboard.Layout.Elements
	require.Len(t, elements, 3)
	assert.Empty(t, elements[0].Width, "daily-summary width should be empty")
	assert.Empty(t, elements[1].Width, "currently-hearing width should be empty (was 'half')")
	assert.Empty(t, elements[2].Width, "detections-grid width should be empty (was 'half')")

	// Also verify: sending explicit "full" works the same way
	update2 := map[string]any{
		"layout": map[string]any{
			"elements": []map[string]any{
				{"id": "daily-summary-0", "type": "daily-summary", "enabled": true},
				{"id": "currently-hearing-0", "type": "currently-hearing", "enabled": true, "width": "full"},
				{"id": "detections-grid-0", "type": "detections-grid", "enabled": true, "width": "half"},
			},
		},
	}

	body2, err := json.Marshal(update2)
	require.NoError(t, err)

	req2 := httptest.NewRequest(http.MethodPatch, "/api/v2/settings/dashboard", bytes.NewReader(body2))
	req2.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec2 := httptest.NewRecorder()
	ctx2 := e.NewContext(req2, rec2)
	ctx2.SetParamNames("section")
	ctx2.SetParamValues("dashboard")

	err = controller.UpdateSectionSettings(ctx2)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec2.Code)

	elements2 := controller.Settings.Load().Realtime.Dashboard.Layout.Elements
	require.Len(t, elements2, 3)
	assert.Equal(t, "full", elements2[1].Width, "currently-hearing should have explicit 'full'")
	assert.Equal(t, "half", elements2[2].Width, "detections-grid should have 'half'")
}

// TestPatchSectionPreservesOmittedFields verifies that a section PATCH changes
// only the scalar it names, leaving the section's other scalars alone, and that
// an empty body changes nothing. Each preserved field holds a non-zero value,
// from the row's seed or from getTestSettings, so a field reset to its zero
// value shows as a difference.
//
// The table pins scalar preservation only. A nested object that is replaced
// instead of merged keeps its scalars (json.Unmarshal leaves absent ones alone)
// and loses its slices, so TestPatchNestedObjectKeepsSiblingSlices is the pin
// for the nested merge.
func TestPatchSectionPreservesOmittedFields(t *testing.T) {
	// marshaled returns v as JSON, for a row that compares a whole section.
	marshaled := func(t *testing.T, v any) string {
		t.Helper()
		out, err := json.Marshal(v)
		require.NoError(t, err)
		return string(out)
	}

	tests := []struct {
		name    string
		section string
		seed    func(s *conf.Settings)
		body    map[string]any
		// changed asserts the field the body names. Nil for the empty body.
		changed func(t *testing.T, s *conf.Settings)
		// preserved returns the values that must be identical before and after.
		preserved func(t *testing.T, s *conf.Settings) any
	}{
		{
			name:    "dashboard thumbnails summary",
			section: "dashboard",
			seed: func(s *conf.Settings) {
				s.Realtime.Dashboard.Thumbnails.ImageProvider = "testprovider"
				s.Realtime.Dashboard.SummaryLimit = 200
			},
			body: map[string]any{"thumbnails": map[string]any{"summary": false}},
			changed: func(t *testing.T, s *conf.Settings) {
				t.Helper()
				assert.False(t, s.Realtime.Dashboard.Thumbnails.Summary)
			},
			preserved: func(t *testing.T, s *conf.Settings) any {
				t.Helper()
				d := s.Realtime.Dashboard
				return []any{d.Thumbnails.Recent, d.Thumbnails.ImageProvider, d.SummaryLimit}
			},
		},
		{
			name:    "weather provider",
			section: "weather",
			seed:    func(s *conf.Settings) { s.Realtime.Weather.Debug = true },
			body:    map[string]any{"provider": "openweather"},
			changed: func(t *testing.T, s *conf.Settings) {
				t.Helper()
				assert.Equal(t, "openweather", s.Realtime.Weather.Provider)
			},
			preserved: func(t *testing.T, s *conf.Settings) any {
				t.Helper()
				w := s.Realtime.Weather
				return []any{w.PollInterval, w.Debug}
			},
		},
		{
			name:    "mqtt broker",
			section: "mqtt",
			seed: func(s *conf.Settings) {
				s.Realtime.MQTT.Enabled = true
				s.Realtime.MQTT.Retain = true
				s.Realtime.MQTT.Username = "testuser"
			},
			body: map[string]any{"broker": "tcp://newbroker:1883"},
			changed: func(t *testing.T, s *conf.Settings) {
				t.Helper()
				assert.Equal(t, "tcp://newbroker:1883", s.Realtime.MQTT.Broker)
			},
			preserved: func(t *testing.T, s *conf.Settings) any {
				t.Helper()
				m := s.Realtime.MQTT
				return []any{m.Enabled, m.Topic, m.Retain, m.Username}
			},
		},
		{
			name:    "audio export type",
			section: "audio",
			body:    map[string]any{"export": map[string]any{"type": "mp3"}},
			changed: func(t *testing.T, s *conf.Settings) {
				t.Helper()
				assert.Equal(t, "mp3", s.Realtime.Audio.Export.Type)
			},
			preserved: func(t *testing.T, s *conf.Settings) any {
				t.Helper()
				x := s.Realtime.Audio.Export
				return []any{x.Enabled, x.Path, x.Bitrate}
			},
		},
		{
			name:    "empty body changes nothing",
			section: "dashboard",
			seed:    func(s *conf.Settings) { s.Realtime.Dashboard.Thumbnails.ImageProvider = "wikimedia" },
			body:    map[string]any{},
			preserved: func(t *testing.T, s *conf.Settings) any {
				t.Helper()
				return marshaled(t, s.Realtime.Dashboard)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := echo.New()
			controller := newMergeController(t, e, tt.seed)
			want := tt.preserved(t, controller.Settings.Load())

			rec := patchSection(t, e, controller, tt.section, tt.body)

			var response struct {
				Message string `json:"message"`
			}
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
			assert.Equal(t, tt.section+" settings updated successfully", response.Message)

			saved := controller.Settings.Load()
			if tt.changed != nil {
				tt.changed(t, saved)
			}
			assert.Equal(t, want, tt.preserved(t, saved), "fields the body omits must keep their values")
		})
	}
}

// TestAudioSourcesSectionPatch pins the server side of the onboarding wizard's
// audio payloads: sources are replaced as a whole array, and an emptied sources
// array only stays empty when the legacy source is cleared in the same request.
func TestAudioSourcesSectionPatch(t *testing.T) {
	const staleLegacySource = "hw:9,0"

	tests := []struct {
		name        string
		body        string
		wantDevices []string
	}{
		{
			name:        "sources replaced and legacy source cleared",
			body:        `{"sources":[{"name":"Sound Card 1","device":"usb-path:test","gain":0,"quietHours":{"enabled":false,"mode":"fixed","startTime":"22:00","endTime":"06:00","startEvent":"sunset","startOffset":0,"endEvent":"sunrise","endOffset":0}}],"source":""}`,
			wantDevices: []string{"usb-path:test"},
		},
		{
			name:        "emptied sources stay empty when legacy source is cleared",
			body:        `{"sources":[],"source":""}`,
			wantDevices: []string{},
		},
		{
			// The frontend must send source: "" with an emptied sources array,
			// otherwise the migration recreates a sound card from the stale value.
			name:        "emptied sources without clearing legacy source resurrect it",
			body:        `{"sources":[]}`,
			wantDevices: []string{staleLegacySource},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			initialSettings := getTestSettings(t)
			initialSettings.Realtime.Audio.Source = staleLegacySource

			e := echo.New()
			controller := &Controller{Core: &apicore.Core{Echo: e}, controlChan: make(chan string, 10), DisableSaveSettings: true}
			controller.Settings.Store(initialSettings)

			req := httptest.NewRequest(http.MethodPatch, "/api/v2/settings/audio", strings.NewReader(tt.body))
			req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
			rec := httptest.NewRecorder()
			ctx := e.NewContext(req, rec)
			ctx.SetParamNames("section")
			ctx.SetParamValues("audio")

			require.NoError(t, controller.UpdateSectionSettings(ctx))
			assert.Equal(t, http.StatusOK, rec.Code)

			audio := controller.Settings.Load().Realtime.Audio
			devices := make([]string, 0, len(audio.Sources))
			for _, src := range audio.Sources {
				devices = append(devices, src.Device)
			}
			assert.Equal(t, tt.wantDevices, devices)
			// The migration moves a legacy source into sources, so Source is empty in every case
			assert.Empty(t, audio.Source)
			// Export settings are untouched by a sources-only patch
			assert.True(t, audio.Export.Enabled)
			assert.Equal(t, "clips", audio.Export.Path)
			assert.Equal(t, "192k", audio.Export.Bitrate)
		})
	}
}

// TestValidationErrors verifies validation rules are enforced
//
// Not parallel: see the note in settings_blocked_fields_test.go.
func TestValidationErrors(t *testing.T) {
	tests := []struct {
		name          string
		section       string
		update        any
		expectedError string
		expectedCode  int
	}{
		{
			name:          "Validate MQTT broker required when enabled",
			section:       "mqtt",
			update:        map[string]any{"enabled": true, "broker": ""},
			expectedError: "Failed to update mqtt settings",
			expectedCode:  http.StatusBadRequest,
		},
		{
			name:          "Handle unknown section",
			section:       "nonexistent",
			update:        map[string]any{"foo": "bar"},
			expectedError: "Failed to update nonexistent settings",
			expectedCode:  http.StatusBadRequest,
		},
		{
			name:          "Reject invalid JSON",
			section:       "dashboard",
			update:        "{invalid json",
			expectedError: "Failed to parse request body",
			expectedCode:  http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := echo.New()
			controller := getTestController(t, e)

			var body []byte
			var err error
			if str, ok := tt.update.(string); ok {
				body = []byte(str)
			} else {
				body, err = json.Marshal(tt.update)
				require.NoError(t, err)
			}

			req := httptest.NewRequest(http.MethodPatch, "/api/v2/settings/"+tt.section, bytes.NewReader(body))
			req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
			rec := httptest.NewRecorder()
			ctx := e.NewContext(req, rec)
			ctx.SetParamNames("section")
			ctx.SetParamValues(tt.section)

			err = controller.UpdateSectionSettings(ctx)

			// Use helper function to assert error response
			apitest.AssertControllerError(t, err, rec, tt.expectedCode, tt.expectedError)
		})
	}
}

// TestPatchNestedObjectKeepsSiblingSlices pins the nested-object merge of the
// PATCH path. The body names one scalar inside the equalizer object, and the
// filter slice next to it must come through untouched. A slice is the case a
// struct-only assertion cannot see: the merge resets every JSON-visible slice
// before decoding, so a nested object that is replaced instead of merged loses
// its slices, while json.Unmarshal would keep an absent scalar either way.
func TestPatchNestedObjectKeepsSiblingSlices(t *testing.T) {
	e := echo.New()
	controller := getTestController(t, e)
	controller.Settings.Load().Realtime.Audio.Equalizer = conf.EqualizerSettings{
		Enabled: true,
		Filters: []conf.EqualizerFilter{
			{Type: "HighPass", Frequency: 200, Q: 0.707, Passes: 1},
			{Type: "LowPass", Frequency: 14000, Q: 0.707, Passes: 2},
		},
	}

	patchSection(t, e, controller, "audio", map[string]any{
		"equalizer": map[string]any{"enabled": false},
	})

	eq := controller.Settings.Load().Realtime.Audio.Equalizer
	assert.False(t, eq.Enabled, "the field in the body must change")
	require.Len(t, eq.Filters, 2, "the sibling filter slice must survive")
	assert.Equal(t, "HighPass", eq.Filters[0].Type)
	assert.InDelta(t, 200.0, eq.Filters[0].Frequency, 0.001)
	assert.Equal(t, 1, eq.Filters[0].Passes)
	assert.Equal(t, "LowPass", eq.Filters[1].Type)
	assert.InDelta(t, 14000.0, eq.Filters[1].Frequency, 0.001)
	assert.Equal(t, 2, eq.Filters[1].Passes)
}

// TestStreamsSettingsChanged_DetectsModelEdits verifies that per-stream model
// list changes trigger reconfigure_rtsp_sources. Without this, a user who
// adds a classifier to a stream (e.g., enables Perch v2 alongside BirdNET)
// would see the save persist to disk but the running pipeline keep using
// the old model set, silently breaking the hot-reload contract.
func TestStreamsSettingsChanged_DetectsModelEdits(t *testing.T) {
	t.Parallel()

	makeSettings := func(models []string) *conf.Settings {
		s := &conf.Settings{}
		s.Realtime.RTSP.Streams = []conf.StreamConfig{
			{
				Name:      "Front Yard",
				URL:       "rtsp://192.168.1.10/stream",
				Type:      conf.StreamTypeRTSP,
				Transport: "tcp",
				Enabled:   true,
				Models:    models,
			},
		}
		return s
	}

	tests := []struct {
		name string
		old  []string
		new  []string
		want bool
	}{
		{"identical model list", []string{"birdnet"}, []string{"birdnet"}, false},
		{"model added", []string{"birdnet"}, []string{"birdnet", "perch_v2"}, true},
		{"model removed", []string{"birdnet", "perch_v2"}, []string{"birdnet"}, true},
		{"model reordered", []string{"birdnet", "perch_v2"}, []string{"perch_v2", "birdnet"}, true},
		{"replaced entirely", []string{"birdnet"}, []string{"perch_v2"}, true},
		{"both empty", nil, nil, false},
		{"nil vs empty slice", nil, []string{}, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			old := makeSettings(tc.old)
			cur := makeSettings(tc.new)
			assert.Equal(t, tc.want, streamsSettingsChanged(old, cur))
		})
	}
}

// TestAudioDeviceSettingChanged_DetectsModelsEdits verifies the analogous fix
// for sound-card audio sources: editing AudioSourceConfig.Models must trigger
// reconfigure_audio_sources. Pre-fix, only the deprecated singular Model
// string was compared, so adding a classifier via the new list field would
// silently no-op until restart.
func TestAudioDeviceSettingChanged_DetectsModelsEdits(t *testing.T) {
	t.Parallel()

	makeSettings := func(models []string) *conf.Settings {
		s := &conf.Settings{}
		s.Realtime.Audio.Sources = []conf.AudioSourceConfig{{
			Name:   "Garden Mic",
			Device: "hw:1,0",
			Gain:   0,
			Models: models,
		}}
		return s
	}

	tests := []struct {
		name string
		old  []string
		new  []string
		want bool
	}{
		{"identical model list", []string{"birdnet"}, []string{"birdnet"}, false},
		{"model added", []string{"birdnet"}, []string{"birdnet", "perch_v2"}, true},
		{"model removed", []string{"birdnet", "perch_v2"}, []string{"birdnet"}, true},
		{"model reordered", []string{"birdnet", "perch_v2"}, []string{"perch_v2", "birdnet"}, true},
		{"replaced entirely", []string{"birdnet"}, []string{"perch_v2"}, true},
		{"both empty", nil, nil, false},
		{"nil vs empty slice", nil, []string{}, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			old := makeSettings(tc.old)
			cur := makeSettings(tc.new)
			assert.Equal(t, tc.want, audioDeviceSettingChanged(old, cur))
		})
	}
}

// TestStreamsSettingsChanged_BackwardCompatibility verifies that the existing
// fields (Name, URL, Enabled, Type, Transport) still trigger the change
// detector: the Models check is additive and must not regress prior coverage.
func TestStreamsSettingsChanged_BackwardCompatibility(t *testing.T) {
	t.Parallel()

	base := func() *conf.Settings {
		s := &conf.Settings{}
		s.Realtime.RTSP.Streams = []conf.StreamConfig{{
			Name:      "Front Yard",
			URL:       "rtsp://192.168.1.10/stream",
			Type:      conf.StreamTypeRTSP,
			Transport: "tcp",
			Enabled:   true,
			Models:    []string{"birdnet"},
		}}
		return s
	}

	tests := []struct {
		name   string
		mutate func(*conf.Settings)
		want   bool
	}{
		{"no change", func(*conf.Settings) {}, false},
		{"name changed", func(s *conf.Settings) { s.Realtime.RTSP.Streams[0].Name = "Back Yard" }, true},
		{"url changed", func(s *conf.Settings) { s.Realtime.RTSP.Streams[0].URL = "rtsp://192.168.1.11/stream" }, true},
		{"transport changed", func(s *conf.Settings) { s.Realtime.RTSP.Streams[0].Transport = "udp" }, true},
		{"disabled", func(s *conf.Settings) { s.Realtime.RTSP.Streams[0].Enabled = false }, true},
		{"stream added", func(s *conf.Settings) {
			s.Realtime.RTSP.Streams = append(s.Realtime.RTSP.Streams, conf.StreamConfig{
				Name: "Garden", URL: "rtsp://192.168.1.12/stream", Type: conf.StreamTypeRTSP, Transport: "tcp",
			})
		}, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			old := base()
			cur := base()
			tc.mutate(cur)
			assert.Equal(t, tc.want, streamsSettingsChanged(old, cur))
		})
	}
}

// TestStreamsSettingsChanged_ChannelMode verifies that a real channel-mode edit
// triggers reconfiguration while a no-op transition between the empty default and
// its canonical form ("" <-> "downmix") does NOT. An unset mode and an explicit
// "downmix" produce identical FFmpeg arguments, so treating them as a change would
// restart the stream (a brief audio drop) for no functional reason. The frontend
// now sends an explicit "downmix" where it previously sent "", which is exactly
// when this no-op transition occurs on the first save.
func TestStreamsSettingsChanged_ChannelMode(t *testing.T) {
	t.Parallel()

	makeSettings := func(mode conf.ChannelMode) *conf.Settings {
		s := &conf.Settings{}
		s.Realtime.RTSP.Streams = []conf.StreamConfig{{
			Name:        "Front Yard",
			URL:         "rtsp://192.168.1.10/stream",
			Type:        conf.StreamTypeRTSP,
			Transport:   "tcp",
			Enabled:     true,
			ChannelMode: mode,
			Models:      []string{"birdnet"},
		}}
		return s
	}

	tests := []struct {
		name string
		old  conf.ChannelMode
		new  conf.ChannelMode
		want bool
	}{
		{"unset to explicit downmix is a no-op", "", conf.ChannelModeDownmix, false},
		{"explicit downmix to unset is a no-op", conf.ChannelModeDownmix, "", false},
		{"identical left", conf.ChannelModeLeft, conf.ChannelModeLeft, false},
		{"unset to left changes", "", conf.ChannelModeLeft, true},
		{"downmix to left changes", conf.ChannelModeDownmix, conf.ChannelModeLeft, true},
		{"left to right changes", conf.ChannelModeLeft, conf.ChannelModeRight, true},
		{"left to downmix changes", conf.ChannelModeLeft, conf.ChannelModeDownmix, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			old := makeSettings(tc.old)
			cur := makeSettings(tc.new)
			assert.Equal(t, tc.want, streamsSettingsChanged(old, cur))
		})
	}
}

// TestStreamsSettingsChanged_MediaMode verifies that a real media-mode edit
// triggers reconfiguration while the no-op transition between the empty default
// and its canonical form ("" <-> "full-stream") does NOT. Without canonical
// comparison the first save after the frontend starts sending an explicit
// "full-stream" would needlessly restart the stream.
func TestStreamsSettingsChanged_MediaMode(t *testing.T) {
	t.Parallel()

	makeSettings := func(mode conf.MediaMode) *conf.Settings {
		s := &conf.Settings{}
		s.Realtime.RTSP.Streams = []conf.StreamConfig{{
			Name:      "Front Yard",
			URL:       "rtsp://192.168.1.10/stream",
			Type:      conf.StreamTypeRTSP,
			Transport: "tcp",
			Enabled:   true,
			MediaMode: mode,
			Models:    []string{"birdnet"},
		}}
		return s
	}

	tests := []struct {
		name string
		old  conf.MediaMode
		new  conf.MediaMode
		want bool
	}{
		{"unset to explicit full-stream is a no-op", "", conf.MediaModeFullStream, false},
		{"explicit full-stream to unset is a no-op", conf.MediaModeFullStream, "", false},
		{"identical auto", conf.MediaModeAuto, conf.MediaModeAuto, false},
		{"unset to auto changes", "", conf.MediaModeAuto, true},
		{"full-stream to auto changes", conf.MediaModeFullStream, conf.MediaModeAuto, true},
		{"auto to audio-only changes", conf.MediaModeAuto, conf.MediaModeAudioOnly, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			old := makeSettings(tc.old)
			cur := makeSettings(tc.new)
			assert.Equal(t, tc.want, streamsSettingsChanged(old, cur))
		})
	}
}
