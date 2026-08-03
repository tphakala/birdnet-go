// media_cache_control_test.go: coverage for the Private Mode media cache policy
// introduced for GHSA-c7jx-552f-94hh.
//
// When Private Mode is enabled the stored detection media served by this domain
// is access-controlled, so responses must be marked "private" to keep shared or
// proxy caches from retaining a spectrogram/audio clip and re-serving it to an
// unauthenticated client. When Private Mode is off the media is public, so the
// long-lived "public ... immutable" caching that mitigates connection exhaustion
// on detection-heavy pages is preserved.

package media

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/api/v2/apitest"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// newCacheControlTestHandler builds a media Handler whose settings snapshot has
// Security.PrivateMode set to privateMode, and returns the handler, its echo
// instance, and the SecureFS root directory tests write fixtures into. Mirrors
// setupMediaTestEnvironment but toggles Private Mode (the setting that drives the
// Cache-Control policy). Not parallel-safe: NewCore publishes to the process-global
// settings snapshot that mediaCacheVisibility() reads.
func newCacheControlTestHandler(t *testing.T, privateMode bool) (*Handler, *echo.Echo, string) {
	t.Helper()
	e := echo.New()
	core := apitest.NewCore(t, apitest.WithEcho(e), apitest.WithSettingsFunc(func(s *conf.Settings) {
		s.Security.PrivateMode = privateMode
	}))
	h := New(core)
	return h, e, core.SFS.BaseDir()
}

// TestMediaCacheVisibility pins both branches of the per-request cache-visibility
// decision that the spectrogram serve paths use to build their Cache-Control
// header. The value is read via CurrentSettings() so a hot-reload toggle takes
// effect without a restart; each subtest publishes its own settings snapshot.
func TestMediaCacheVisibility(t *testing.T) {
	t.Run("public when Private Mode is off", func(t *testing.T) {
		h := New(apitest.NewCore(t))
		assert.Equal(t, "public", h.mediaCacheVisibility(),
			"media must be publicly cacheable when Private Mode is disabled")
	})

	t.Run("private when Private Mode is on", func(t *testing.T) {
		h := New(apitest.NewCore(t, apitest.WithSettingsFunc(func(s *conf.Settings) {
			s.Security.PrivateMode = true
		})))
		assert.Equal(t, "private", h.mediaCacheVisibility(),
			"media must be marked private so shared caches do not retain it when Private Mode is enabled")
	})
}

// TestServeSpectrogramCacheControlHonorsPrivateMode drives the real spectrogram
// serve path (ServeSpectrogram) and asserts the emitted Cache-Control header, not
// just the helper's return. This is the end-to-end guard that the visibility token
// is actually wired into the served response: re-hardcoding "public" at any
// spectrogram serve site would turn this red in Private Mode, which the pure-helper
// test above cannot catch.
func TestServeSpectrogramCacheControlHonorsPrivateMode(t *testing.T) {
	testCases := []struct {
		name           string
		privateMode    bool
		wantVisibility string
	}{
		{"public mode serves public cache", false, "public"},
		{"private mode serves private cache", true, "private"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			h, e, root := newCacheControlTestHandler(t, tc.privateMode)

			// Simulate an already-generated spectrogram so the serve path runs
			// without invoking external tools. Default raw=true => audio_800px.png.
			require.NoError(t, createTestAudioFile(t, filepath.Join(root, "audio.wav")))
			require.NoError(t, os.WriteFile(filepath.Join(root, "audio_800px.png"),
				[]byte("simulated spectrogram content"), 0o600))

			req := httptest.NewRequest(http.MethodGet, "/api/v2/media/spectrogram/audio.wav?width=800", http.NoBody)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)
			c.SetParamNames("filename")
			c.SetParamValues("audio.wav")

			require.NoError(t, h.ServeSpectrogram(c))
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

			cacheControl := rec.Header().Get("Cache-Control")
			assert.Truef(t, strings.HasPrefix(cacheControl, tc.wantVisibility+", "),
				"spectrogram Cache-Control must start with %q visibility in %s, got %q",
				tc.wantVisibility, tc.name, cacheControl)
			assert.Contains(t, cacheControl, "immutable",
				"spectrogram cache directive should retain immutable")
		})
	}
}

// TestServeAudioClipCacheControlPrivateInPrivateMode drives the real audio serve
// path (ServeAudioClip) and asserts setPrivateAudioCacheControl is wired in:
// "private" in Private Mode, and no Cache-Control at all in public mode (the prior
// behavior). Dropping the setPrivateAudioCacheControl call would turn the Private
// Mode case red.
func TestServeAudioClipCacheControlPrivateInPrivateMode(t *testing.T) {
	testCases := []struct {
		name             string
		privateMode      bool
		wantCacheControl string // exact expected header ("" means header absent)
	}{
		{"public mode sets no cache-control", false, ""},
		{"private mode marks response private", true, "private"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			h, e, root := newCacheControlTestHandler(t, tc.privateMode)

			audioFilename := "clip.wav"
			require.NoError(t, createTestAudioFile(t, filepath.Join(root, audioFilename)))

			req := httptest.NewRequest(http.MethodGet, "/api/v2/media/audio/"+audioFilename, http.NoBody)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)
			c.SetParamNames("filename")
			c.SetParamValues(audioFilename)

			require.NoError(t, h.ServeAudioClip(c))
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

			assert.Equal(t, tc.wantCacheControl, rec.Header().Get("Cache-Control"),
				"audio Cache-Control mismatch in %s", tc.name)
		})
	}
}
