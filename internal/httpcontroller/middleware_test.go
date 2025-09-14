package httpcontroller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// TestCacheControlMiddleware_V2AudioHeaders verifies that v2 audio routes
// receive the required headers for iOS Safari compatibility
func TestCacheControlMiddleware_V2AudioHeaders(t *testing.T) {
	// Create a test server
	s := &Server{
		Echo:     echo.New(),
		Settings: &conf.Settings{},
	}

	// Create the middleware
	middleware := s.CacheControlMiddleware()

	// Test cases for audio routes
	tests := []struct {
		name        string
		path        string
		wantHeaders map[string]string
	}{
		{
			name: "v1 audio route",
			path: "/api/v1/media/audio/test.wav",
			wantHeaders: map[string]string{
				"Cache-Control":          "no-store",
				"X-Content-Type-Options": "nosniff",
				"Accept-Ranges":          "bytes",
			},
		},
		{
			name: "v2 audio route",
			path: "/api/v2/audio/12345",
			wantHeaders: map[string]string{
				"Cache-Control":          "no-store",
				"X-Content-Type-Options": "nosniff",
				"Accept-Ranges":          "bytes",
			},
		},
		{
			name: "v1 spectrogram route",
			path: "/api/v1/media/spectrogram/test.png",
			wantHeaders: map[string]string{
				// Note: .png suffix matches the image rule first, so it gets 7-day cache
				"Cache-Control": "public, max-age=604800, immutable",
			},
		},
		{
			name: "v2 spectrogram route",
			path: "/api/v2/spectrogram/12345",
			wantHeaders: map[string]string{
				"Cache-Control": "public, max-age=2592000, immutable",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create request and response recorder
			req := httptest.NewRequest(http.MethodGet, tt.path, http.NoBody)
			rec := httptest.NewRecorder()
			c := s.Echo.NewContext(req, rec)

			// Create a simple handler that just returns OK
			handler := func(c echo.Context) error {
				return c.String(http.StatusOK, "OK")
			}

			// Apply the middleware
			err := middleware(handler)(c)
			require.NoError(t, err)

			// Check that expected headers are set
			for header, expectedValue := range tt.wantHeaders {
				actualValue := rec.Header().Get(header)
				assert.Equal(t, expectedValue, actualValue,
					"Header %s should be %s for path %s, got %s",
					header, expectedValue, tt.path, actualValue)
			}
		})
	}
}

// TestCacheControlMiddleware_IOSSafariCompatibility specifically tests
// that the Accept-Ranges header is present for audio routes, which is
// critical for iOS Safari audio playback through Cloudflare
func TestCacheControlMiddleware_IOSSafariCompatibility(t *testing.T) {
	s := &Server{
		Echo:     echo.New(),
		Settings: &conf.Settings{},
	}

	middleware := s.CacheControlMiddleware()

	// Test that both v1 and v2 audio routes have Accept-Ranges header
	audioPaths := []string{
		"/api/v1/media/audio/clip.wav",
		"/api/v2/audio/detection-id-123",
	}

	for _, path := range audioPaths {
		req := httptest.NewRequest(http.MethodGet, path, http.NoBody)
		rec := httptest.NewRecorder()
		c := s.Echo.NewContext(req, rec)

		handler := func(c echo.Context) error {
			return c.String(http.StatusOK, "audio data")
		}

		err := middleware(handler)(c)
		require.NoError(t, err)

		// The critical header for iOS Safari
		acceptRanges := rec.Header().Get("Accept-Ranges")
		assert.Equal(t, "bytes", acceptRanges,
			"Accept-Ranges: bytes header must be present for iOS Safari compatibility on path %s", path)
	}
}

// TestCacheControlMiddleware_ContentTypePreservation tests that when Content-Type
// is already set by a handler (e.g., for audio files), the middleware doesn't override it
func TestCacheControlMiddleware_ContentTypePreservation(t *testing.T) {
	s := &Server{
		Echo:     echo.New(),
		Settings: &conf.Settings{},
	}

	middleware := s.CacheControlMiddleware()

	// Test that pre-set Content-Type headers are preserved
	req := httptest.NewRequest(http.MethodGet, "/api/v2/audio/12345", http.NoBody)
	rec := httptest.NewRecorder()
	c := s.Echo.NewContext(req, rec)

	// Simulate handler setting Content-Type before middleware processes it
	presetContentType := "audio/flac"

	handler := func(c echo.Context) error {
		// Handler sets Content-Type first (simulates what media.go does)
		c.Response().Header().Set("Content-Type", presetContentType)
		return c.String(http.StatusOK, "audio data")
	}

	err := middleware(handler)(c)
	require.NoError(t, err)

	// Verify the preset Content-Type is preserved
	actualContentType := rec.Header().Get("Content-Type")
	assert.Equal(t, presetContentType, actualContentType,
		"Preset Content-Type should be preserved by middleware")

	// Verify other audio-specific headers are still set
	acceptRanges := rec.Header().Get("Accept-Ranges")
	assert.Equal(t, "bytes", acceptRanges, "Accept-Ranges should still be set")

	cacheControl := rec.Header().Get("Cache-Control")
	assert.Equal(t, "no-store", cacheControl, "Cache-Control should still be set")
}
