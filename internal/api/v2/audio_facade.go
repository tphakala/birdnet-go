// internal/api/v2/audio_facade.go
package api

import (
	"net/http"

	"github.com/labstack/echo/v4"

	audioapi "github.com/tphakala/birdnet-go/internal/api/v2/audio"
	authapi "github.com/tphakala/birdnet-go/internal/api/v2/auth"
	"github.com/tphakala/birdnet-go/internal/audiocore"
)

// isClientAuthenticated checks if the current request is authenticated by
// delegating to the auth service's centralized IsAuthenticated method. It stays
// on the facade because the detections domain depends on it as an injected bound
// method value (the audio domain holds its own authService copy and reimplements
// the same check locally).
func (c *Controller) isClientAuthenticated(ctx echo.Context) bool {
	if c.authService == nil {
		return false
	}
	return c.authService.IsAuthenticated(ctx)
}

// isPrivateModeExempt returns true for v2 API (method, route pattern) pairs that
// must remain reachable without authentication even when PrivateMode is on. Two
// categories are exempt:
//
//  1. Bootstrap and auth flow paths so the frontend can fetch /app/config and
//     complete a login (including OAuth callback) from an unauthenticated state.
//  2. Live audio (HLS) paths that already have their own publicLiveAudioAuth
//     middleware. Letting them through privateModeAuth preserves the
//     PublicAccess.LiveAudio carve-out: when LiveAudio is enabled the route stays
//     public, when it is disabled the per-route middleware applies authMiddleware
//     as before.
//
// The allow-list is keyed on method + path so any future handler added at one of
// these paths under a different verb is fail-closed by default. It is injected
// into apicore.NewCore so the apicore privateModeAuth middleware reads it without
// importing a domain. The HLS path fragments come from the audio domain package
// (their registration site) so the allow-list cannot drift from the routes.
func isPrivateModeExempt(method, path string) bool {
	const (
		authBase     = apiV2Prefix + authapi.AuthGroupPath
		hlsBase      = apiV2Prefix + audioapi.HLSGroupPath
		hlsTokenBase = hlsBase + audioapi.HLSTokenGroupPath
	)
	switch {
	case method == http.MethodGet && path == apiV2Prefix+AppConfigEndpoint:
		return true
	case method == http.MethodPost && path == authBase+authapi.AuthLoginPath:
		return true
	case method == http.MethodGet && path == authBase+authapi.AuthCallbackPath:
		return true
	case method == http.MethodPost && path == hlsBase+audioapi.HLSStartPath:
		return true
	case method == http.MethodPost && path == hlsBase+audioapi.HLSHeartbeatPath:
		return true
	case method == http.MethodGet && path == hlsBase+audioapi.HLSStatusPath:
		return true
	case method == http.MethodGet && path == hlsTokenBase+audioapi.HLSPlaylistPath:
		return true
	case method == http.MethodGet && path == hlsTokenBase+audioapi.HLSContentPath:
		return true
	}
	return false
}

// RestartHLSStreams stops all active HLS streams so they restart with fresh
// settings. It delegates to the audio domain handler, which owns the HLS manager.
// The audio pipeline calls this on *Controller (internal/analysis), so the method
// stays on the facade as a one-line delegator.
func (c *Controller) RestartHLSStreams() {
	c.audio.RestartHLSStreams()
}

// SetAudioWatchdog injects the liveness watchdog read by the audio health
// endpoint. It delegates to the audio domain handler. internal/analysis calls
// this on *Controller during pipeline Start(), so it stays on the facade.
func (c *Controller) SetAudioWatchdog(w *audiocore.LivenessWatchdog) {
	c.audio.SetAudioWatchdog(w)
}

// SetAudioLevelChan wires the live audio-level channel into the audio domain
// handler and starts the broadcaster goroutine. The parent server (internal/api)
// owns the channel and calls this on *Controller, so it stays on the facade.
func (c *Controller) SetAudioLevelChan(ch chan audiocore.AudioLevelData) {
	c.audio.SetAudioLevelChan(ch)
}
