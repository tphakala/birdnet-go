package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/tphakala/birdnet-go/internal/conf"
)

const (
	// wizardBirdWeatherID is a 24 character alphanumeric BirdWeather station ID.
	wizardBirdWeatherID = "abcdefghij0123456789ABCD"
	// wizardStreamURL is the RTSP URL the wizard body carries.
	wizardStreamURL = "rtsp://camera.example/stream"
	// wizardStreamName is the name the wizard gives the stream it creates.
	wizardStreamName = "Stream 1"
	// wizardLatitude and wizardLongitude are the coordinates the wizard sets.
	wizardLatitude  = 60.17
	wizardLongitude = 24.94
	// wizardThreshold is the detection threshold of the high accuracy preset.
	wizardThreshold = 0.9
	// wizardEQFilterFrequency, wizardEQFilterQ and wizardEQFilterPasses describe
	// the global equalizer filter that must survive every wizard save unchanged.
	wizardEQFilterFrequency = 200
	wizardEQFilterQ         = 0.707
	wizardEQFilterPasses    = 1
)

// wizardPatch is one section PATCH the onboarding wizard sends, with the change
// it must make to the settings and nothing else.
type wizardPatch struct {
	name    string
	section string
	body    map[string]any
	expect  func(*conf.Settings)
}

// wizardPatches lists the exact bodies the wizard steps send, in wizard order.
func wizardPatches() []wizardPatch {
	return []wizardPatch{
		{
			name:    "location and species language",
			section: "birdnet",
			body: map[string]any{
				"latitude": wizardLatitude, "longitude": wizardLongitude,
				"locale": "fi", "locationConfigured": true,
			},
			expect: func(s *conf.Settings) {
				s.BirdNET.Latitude = wizardLatitude
				s.BirdNET.Longitude = wizardLongitude
				s.BirdNET.Locale = "fi"
				s.BirdNET.LocationConfigured = true
			},
		},
		{
			name:    "ui language",
			section: "dashboard",
			body:    map[string]any{"locale": "fi"},
			expect:  func(s *conf.Settings) { s.Realtime.Dashboard.Locale = "fi" },
		},
		{
			name:    "audio source sound card",
			section: "audio",
			body:    map[string]any{"source": "hw:1,0"},
			expect:  func(s *conf.Settings) { s.Realtime.Audio.Source = "hw:1,0" },
		},
		{
			name:    "audio source stream",
			section: "rtsp",
			body: map[string]any{"streams": []map[string]any{{
				"name": wizardStreamName, "url": wizardStreamURL,
				"enabled": true, "type": "rtsp", "transport": "tcp",
			}}},
			expect: func(s *conf.Settings) {
				s.Realtime.RTSP.Streams = []conf.StreamConfig{{
					Name: wizardStreamName, URL: wizardStreamURL,
					Enabled: true, Type: "rtsp", Transport: "tcp",
				}}
			},
		},
		{
			name:    "detection preset",
			section: "birdnet",
			body:    map[string]any{"threshold": wizardThreshold},
			expect: func(s *conf.Settings) {
				s.BirdNET.Threshold = wizardThreshold
				// The server marks the location configured on any birdnet save
				// while a coordinate is non-zero, and the fixture has one.
				s.BirdNET.LocationConfigured = true
			},
		},
		{
			name:    "birdweather",
			section: "birdweather",
			body:    map[string]any{"enabled": true, "id": wizardBirdWeatherID},
			expect: func(s *conf.Settings) {
				s.Realtime.Birdweather.Enabled = true
				s.Realtime.Birdweather.ID = wizardBirdWeatherID
			},
		},
		{
			name:    "privacy filter",
			section: "privacyfilter",
			body:    map[string]any{"enabled": true},
			expect:  func(s *conf.Settings) { s.Realtime.PrivacyFilter.Enabled = true },
		},
		{
			name:    "error reporting",
			section: "sentry",
			body:    map[string]any{"enabled": true},
			expect:  func(s *conf.Settings) { s.Sentry.Enabled = true },
		},
	}
}

// wizardFixture returns settings with the values the wizard must never alter as
// a side effect: a global equalizer filter with a zero Width, and a disabled
// webhook push provider whose endpoint has an empty Method.
func wizardFixture(t *testing.T) *conf.Settings {
	t.Helper()
	s := getTestSettings(t)
	s.Realtime.Audio.Equalizer.Enabled = true
	s.Realtime.Audio.Equalizer.Filters = []conf.EqualizerFilter{{
		Type: "HighPass", Frequency: wizardEQFilterFrequency, Q: wizardEQFilterQ,
		Width: 0, Passes: wizardEQFilterPasses,
	}}
	s.Notification.Push.Providers = []conf.PushProviderConfig{{
		Type: "webhook", Enabled: false, Name: "hook",
		Endpoints: []conf.WebhookEndpointConfig{{URL: "https://hook.example/notify", Method: ""}},
	}}
	// A loaded config.yaml has already been through validation, which fills in
	// defaults for unset fields. Do the same so the tests compare only what a
	// PATCH changes, not defaults that any validated save would write.
	require.NoError(t, conf.ValidateSettings(s))
	return s
}

// newWizardController builds a controller that holds the fixture and never
// writes config.yaml.
func newWizardController(t *testing.T) *Controller {
	t.Helper()
	c := getTestController(t, echo.New())
	c.Settings.Store(wizardFixture(t))
	return c
}

// patchWizardSection sends PATCH /api/v2/settings/:section through the handler.
func patchWizardSection(t *testing.T, c *Controller, section string, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	payload, err := json.Marshal(body)
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPatch, "/api/v2/settings/"+section, bytes.NewReader(payload))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	ctx := c.Echo.NewContext(req, rec)
	ctx.SetParamNames("section")
	ctx.SetParamValues(section)
	require.NoError(t, c.UpdateSectionSettings(ctx))
	return rec
}

// settingsYAML marshals settings the way SaveYAMLConfig writes config.yaml.
func settingsYAML(t *testing.T, s *conf.Settings) string {
	t.Helper()
	out, err := yaml.Marshal(s)
	require.NoError(t, err)
	return string(out)
}

// TestWizardSectionPatchesLeaveOtherSectionsByteIdentical verifies that each
// wizard PATCH changes only its own keys: the YAML of the stored settings equals
// the YAML of the fixture with just the expected change applied.
func TestWizardSectionPatchesLeaveOtherSectionsByteIdentical(t *testing.T) {
	t.Parallel()

	for _, tc := range wizardPatches() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := newWizardController(t)
			want := conf.CloneSettings(c.Settings.Load())
			tc.expect(want)

			rec := patchWizardSection(t, c, tc.section, tc.body)
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

			got := c.Settings.Load()
			assert.Equal(t, settingsYAML(t, want), settingsYAML(t, got))
			require.Len(t, got.Realtime.Audio.Equalizer.Filters, 1)
			assert.InDelta(t, 0.0, got.Realtime.Audio.Equalizer.Filters[0].Width, 0)
			require.Len(t, got.Notification.Push.Providers, 1)
			require.Len(t, got.Notification.Push.Providers[0].Endpoints, 1)
			assert.Empty(t, got.Notification.Push.Providers[0].Endpoints[0].Method)
			assert.False(t, got.Notification.Push.Providers[0].Enabled)
		})
	}
}

// TestWizardSectionPatchSequenceIsIdempotent verifies that running the whole
// wizard twice, as a rerun on a configured install does, gives the same config.
func TestWizardSectionPatchSequenceIsIdempotent(t *testing.T) {
	t.Parallel()

	c := newWizardController(t)
	runAll := func() {
		for _, tc := range wizardPatches() {
			rec := patchWizardSection(t, c, tc.section, tc.body)
			require.Equal(t, http.StatusOK, rec.Code, "%s: %s", tc.name, rec.Body.String())
		}
	}

	runAll()
	first := settingsYAML(t, c.Settings.Load())
	runAll()

	assert.Equal(t, first, settingsYAML(t, c.Settings.Load()))
}

// TestWizardInvalidBirdWeatherPatchChangesNothing verifies that a rejected
// BirdWeather token returns 400 and leaves the stored settings untouched.
func TestWizardInvalidBirdWeatherPatchChangesNothing(t *testing.T) {
	t.Parallel()

	c := newWizardController(t)
	before := settingsYAML(t, c.Settings.Load())

	rec := patchWizardSection(t, c, "birdweather", map[string]any{"enabled": true, "id": "bad"})

	assert.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	assert.Equal(t, before, settingsYAML(t, c.Settings.Load()))
}
