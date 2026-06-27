// internal/api/v2/audio_facade_test.go
// Tests for the facade-side audio wiring: the PrivateMode exempt allow-list
// (isPrivateModeExempt) which composes app-config, auth, and HLS route constants
// and is injected into apicore.NewCore. These stay in package api because they
// exercise the facade function and reference facade-owned constants; the HLS path
// fragments come from the extracted audio domain package.
package api

import (
	"net/http"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"

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
		{http.MethodGet, apiV2Prefix + AppConfigEndpoint},
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
		{http.MethodPost, apiV2Prefix + WizardDismissEndpoint},                        // gated under PrivateMode by design
		{http.MethodGet, "/api/v2/streams/audio-level"},
		{http.MethodGet, "/api/v2/streams/sources"},
		{http.MethodGet, "/health"},
		// Method mismatches must NOT match the allow-list.
		{http.MethodGet, apiV2Prefix + authapi.AuthGroupPath + authapi.AuthLoginPath}, // login is POST-only
		{http.MethodPost, apiV2Prefix + AppConfigEndpoint},                            // config is GET-only
		{http.MethodDelete, apiV2Prefix + AppConfigEndpoint},
	}
	for _, tt := range notExempt {
		t.Run("not_exempt/"+tt.method+"_"+tt.path, func(t *testing.T) {
			t.Parallel()
			assert.False(t, isPrivateModeExempt(tt.method, tt.path),
				"expected %s %q to NOT be exempt", tt.method, tt.path)
		})
	}
}

// TestPrivateModeExemptPathsAreRegisteredRoutes mounts the routes that the
// PrivateMode exempt allow-list cares about (using the same path constants the
// controllers register them with) and asserts that isPrivateModeExempt's
// verdict matches the intended policy for every registered route. If a route
// constant is renamed the registered path and the exempt composition move
// together, and if isPrivateModeExempt's composition is edited incorrectly the
// verdict no longer matches a real route and this test fails.
func TestPrivateModeExemptPathsAreRegisteredRoutes(t *testing.T) {
	t.Parallel()

	noop := func(echo.Context) error { return nil }

	e := echo.New()
	g := e.Group(apiV2Prefix)

	// App config (public bootstrap endpoint, registered in initAppRoutes).
	g.GET(AppConfigEndpoint, noop)
	g.POST(WizardDismissEndpoint, noop) // registered but NOT exempt under PrivateMode

	// Auth flow (mirrors the auth domain's RegisterRoutes).
	authGroup := g.Group(authapi.AuthGroupPath)
	authGroup.POST(authapi.AuthLoginPath, noop)
	authGroup.GET(authapi.AuthCallbackPath, noop)
	authGroup.POST(authapi.AuthLogoutPath, noop)
	authGroup.GET(authapi.AuthStatusPath, noop)

	// HLS streaming (mirrors the audio domain's RegisterHLSRoutes).
	hlsGroup := g.Group(audioapi.HLSGroupPath)
	hlsGroup.POST(audioapi.HLSStartPath, noop)
	hlsGroup.POST(audioapi.HLSStopPath, noop)
	hlsGroup.POST(audioapi.HLSHeartbeatPath, noop)
	hlsGroup.GET(audioapi.HLSStatusPath, noop)
	hlsTokenGroup := hlsGroup.Group(audioapi.HLSTokenGroupPath)
	hlsTokenGroup.GET(audioapi.HLSPlaylistPath, noop)
	hlsTokenGroup.GET(audioapi.HLSContentPath, noop)

	key := func(method, path string) string { return method + " " + path }

	// Expected exempt set, with paths composed from the same constants the
	// production code and isPrivateModeExempt use.
	wantExempt := map[string]bool{
		key(http.MethodGet, apiV2Prefix+AppConfigEndpoint):                                                         true,
		key(http.MethodPost, apiV2Prefix+authapi.AuthGroupPath+authapi.AuthLoginPath):                              true,
		key(http.MethodGet, apiV2Prefix+authapi.AuthGroupPath+authapi.AuthCallbackPath):                            true,
		key(http.MethodPost, apiV2Prefix+audioapi.HLSGroupPath+audioapi.HLSStartPath):                              true,
		key(http.MethodPost, apiV2Prefix+audioapi.HLSGroupPath+audioapi.HLSHeartbeatPath):                          true,
		key(http.MethodGet, apiV2Prefix+audioapi.HLSGroupPath+audioapi.HLSStatusPath):                              true,
		key(http.MethodGet, apiV2Prefix+audioapi.HLSGroupPath+audioapi.HLSTokenGroupPath+audioapi.HLSPlaylistPath): true,
		key(http.MethodGet, apiV2Prefix+audioapi.HLSGroupPath+audioapi.HLSTokenGroupPath+audioapi.HLSContentPath):  true,
	}

	registered := make(map[string]bool)
	for _, r := range e.Routes() {
		// Echo can register auxiliary entries; only assert on the verbs we use.
		if r.Method != http.MethodGet && r.Method != http.MethodPost {
			continue
		}
		k := key(r.Method, r.Path)
		registered[k] = true
		assert.Equalf(t, wantExempt[k], isPrivateModeExempt(r.Method, r.Path),
			"isPrivateModeExempt verdict mismatch for registered route %s", k)
	}

	// Every expected-exempt entry must correspond to an actually registered
	// route (catches an exempt path that no real route serves).
	for k := range wantExempt {
		assert.Truef(t, registered[k], "exempt route %q is not a registered route", k)
	}
}
