package audio

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/api/v2/apitest"
	"github.com/tphakala/birdnet-go/internal/audiocore"
)

// newAudioHealthTestHandler builds a minimal audio Handler around an isolated
// apitest core for the health endpoint tests (GetAudioHealth reads only the
// AudioWatchdog atomic pointer on the shared core).
func newAudioHealthTestHandler(t *testing.T, e *echo.Echo) *Handler {
	t.Helper()
	return &Handler{Core: apitest.NewCore(t, apitest.WithEcho(e))}
}

// TestGetAudioHealth checks that the endpoint answers an empty sources array
// both with no watchdog installed and with a watchdog that tracks no routes.
func TestGetAudioHealth(t *testing.T) {
	tests := []struct {
		name         string
		withWatchdog bool
	}{
		{name: "no watchdog"},
		{name: "watchdog tracking no sources", withWatchdog: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := echo.New()
			c := newAudioHealthTestHandler(t, e)

			if tt.withWatchdog {
				router := audiocore.NewAudioRouter(audiocore.GetLogger(), nil)
				t.Cleanup(router.Close)
				c.SetAudioWatchdog(audiocore.NewLivenessWatchdog(
					audiocore.DefaultLivenessConfig(), router, audiocore.LivenessCallbacks{}))
			}

			req := httptest.NewRequest(http.MethodGet, "/api/v2/health/audio", http.NoBody)
			rec := httptest.NewRecorder()

			require.NoError(t, c.GetAudioHealth(e.NewContext(req, rec)))
			assert.Equal(t, http.StatusOK, rec.Code)
			// An empty array, not null: assert.Empty cannot tell the two apart.
			assert.JSONEq(t, `{"sources":[]}`, rec.Body.String())
		})
	}
}
