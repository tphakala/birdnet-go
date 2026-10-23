// internal/api/v2/audio_facade_test.go
// Tests for the facade-side audio wiring: the PrivateMode exempt allow-list
// (isPrivateModeExempt) which composes app-config, auth, and HLS route constants
// and is injected into apicore.NewCore. These stay in package api because they
// exercise the facade function; the app-config and HLS path fragments come from
// the extracted app and audio domain packages.
package api

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/tphakala/birdnet-go/internal/api/v2/app"
	audioapi "github.com/tphakala/birdnet-go/internal/api/v2/audio"
	authapi "github.com/tphakala/birdnet-go/internal/api/v2/auth"
)

// TestIsPrivateModeExempt verifies the (method, route) allow-list that
// privateModeAuth uses when Security.PrivateMode is enabled. Bootstrap/auth
// paths must stay reachable for unauthenticated clients so the frontend can
// fetch the /app/config endpoint and complete a login; HLS live audio routes
// must stay exempt so the per-route publicLiveAudioAuth middleware can honour
// PublicAccess.LiveAudio. Exemptions are method-specific so unrelated
// handlers added on the same paths later fail closed by default.
func TestIsPrivateModeExempt(t *testing.T) {
	t.Parallel()

	// Exempt paths are composed from the same route constants the controllers
	// register with, so this allow-list cannot drift from production routing.
	exempt := []struct {
		method string
		path   string
	}{
		{http.MethodGet, apiV2Prefix + app.AppConfigEndpoint},
		{http.MethodPost, apiV2Prefix + authapi.AuthGroupPath + authapi.AuthLoginPath},
		{http.MethodGet, apiV2Prefix + authapi.AuthGroupPath + authapi.AuthCallbackPath},
		{http.MethodPost, apiV2Prefix + audioapi.HLSGroupPath + audioapi.HLSStartPath},
		{http.MethodPost, apiV2Prefix + audioapi.HLSGroupPath + audioapi.HLSHeartbeatPath},
		{http.MethodGet, apiV2Prefix + audioapi.HLSGroupPath + audioapi.HLSStatusPath},
		{http.MethodGet, apiV2Prefix + audioapi.HLSGroupPath + audioapi.HLSTokenGroupPath + audioapi.HLSPlaylistPath},
		{http.MethodGet, apiV2Prefix + audioapi.HLSGroupPath + audioapi.HLSTokenGroupPath + audioapi.HLSContentPath},
	}
	for _, tt := range exempt {
		t.Run("exempt/"+tt.method+"_"+tt.path, func(t *testing.T) {
			t.Parallel()
			assert.True(t, isPrivateModeExempt(tt.method, tt.path),
				"expected %s %q to be exempt", tt.method, tt.path)
		})
	}

	notExempt := []struct {
		method string
		path   string
	}{
		{http.MethodGet, ""},
		{http.MethodGet, "/api/v2/detections"},
		{http.MethodGet, "/api/v2/notifications"},
		{http.MethodGet, "/api/v2/settings/dashboard"},
		{http.MethodPost, apiV2Prefix + authapi.AuthGroupPath + authapi.AuthLogoutPath},
		{http.MethodGet, apiV2Prefix + authapi.AuthGroupPath + authapi.AuthStatusPath},
		{http.MethodPost, apiV2Prefix + audioapi.HLSGroupPath + audioapi.HLSStopPath}, // mutation, always auth-gated
		{http.MethodPost, apiV2Prefix + app.WizardDismissEndpoint},                    // gated under PrivateMode by design
		{http.MethodGet, "/api/v2/streams/audio-level"},
		{http.MethodGet, "/api/v2/streams/sources"},
		{http.MethodGet, "/health"},
		// Method mismatches must NOT match the allow-list.
		{http.MethodGet, apiV2Prefix + authapi.AuthGroupPath + authapi.AuthLoginPath}, // login is POST-only
		{http.MethodPost, apiV2Prefix + app.AppConfigEndpoint},                        // config is GET-only
		{http.MethodDelete, apiV2Prefix + app.AppConfigEndpoint},
	}
	for _, tt := range notExempt {
		t.Run("not_exempt/"+tt.method+"_"+tt.path, func(t *testing.T) {
			t.Parallel()
			assert.False(t, isPrivateModeExempt(tt.method, tt.path),
				"expected %s %q to NOT be exempt", tt.method, tt.path)
		})
	}
}
