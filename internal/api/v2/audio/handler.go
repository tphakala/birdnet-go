// Package audio is the api/v2 audio/streaming domain handler. It owns the live
// audio-level SSE stream, the HLS streaming endpoints (including the bespoke
// stream-token playlist/segment serving and the publicLiveAudio dynamic auth
// gate), the stream health and stream-test endpoints, the quiet-hours status
// endpoint, the audio liveness health endpoint, and the /system/audio device and
// source listing endpoints.
//
// The Handler embeds *apicore.Core by pointer so the shared dependencies and
// helpers (Settings accessors, AuthMiddleware, the HandleError/logging helpers,
// the Context/Cancel/Wait lifecycle, the audio Engine and AudioWatchdog atomic
// pointers, and the promoted SSE helpers) are available without re-plumbing.
//
// Two members are injected from the facade because they live on facade-owned
// state that is not part of the shared core:
//   - authService: the auth service (nil-guarded), read per request so the public
//     audio-level stream and quiet-hours status can anonymize source display
//     names / stream URLs for unauthenticated callers. The facade keeps its own
//     copy because the detections domain also depends on the same check.
//   - audioLevelChan: the live audio-level channel, injected after construction
//     via SetAudioLevelChan (the parent server owns and feeds it).
//
// probeStreamInfo is an injectable stream-probe seam used by the stream-test
// endpoint; it defaults to ffmpeg.ProbeStreamInfo lazily and is overridden in
// tests.
package audio

import (
	"github.com/labstack/echo/v4"

	"github.com/tphakala/birdnet-go/internal/api/auth"
	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/audiocore"
)

// Local copies of constants that live in the facade package (constants.go, and
// apiV2Prefix in api.go). They are duplicated here (the system-domain precedent)
// rather than rippled into apicore, because they are read only by this domain
// after the split.
const (
	// apiV2Prefix is the v2 API path prefix used when building the client-facing
	// HLS playlist URL. It mirrors the facade's apiV2Prefix and the auth domain's
	// local copy.
	apiV2Prefix = "/api/v2"

	// osWindows/osDarwin/osLinux are runtime.GOOS identifiers used by the audio
	// device diagnostics and the HLS Windows-audio-feed path.
	osWindows = "windows"
	osDarwin  = "darwin"
	osLinux   = "linux"

	// secondsPerMinute converts the stream-health SSE rate limit (requests per
	// minute) into echo's per-second rate.Limit.
	secondsPerMinute = 60

	// queryValueTrue is the canonical "true" query-parameter value parsed by the
	// HLS handlers.
	queryValueTrue = "true"

	// logLevelWarning is the ffmpeg log-level used when verbose HLS logging is off.
	logLevelWarning = "warning"

	// filePermReadWrite (0o644) and filePermExecutable (0o755) are the file/dir
	// permissions used for the HLS FFmpeg output directory and log files.
	filePermReadWrite  = 0o644
	filePermExecutable = 0o755

	// defaultReadBufferSize is the default channel/buffer capacity for the HLS
	// audio feed.
	defaultReadBufferSize = 1024
)

// Handler serves the api/v2 audio/streaming domain endpoints. It embeds the
// shared *apicore.Core (by pointer) and additionally holds the facade-injected
// auth service, the injectable stream-probe seam, and the live audio-level channel
// (set after construction via SetAudioLevelChan).
type Handler struct {
	*apicore.Core

	// authService is the facade-injected authentication service (nil-guarded). It
	// is read per request so the public audio-level stream and quiet-hours status
	// can strip source metadata / stream URLs for unauthenticated callers.
	authService auth.Service

	// probeStreamInfo probes a live stream's audio characteristics for the
	// stream-test endpoint. nil by default (TestStream falls back to
	// ffmpeg.ProbeStreamInfo); overridden in tests to stub probing.
	probeStreamInfo probeStreamInfoFunc

	// audioLevelChan is the live audio-level channel injected by the parent server
	// via SetAudioLevelChan after construction.
	audioLevelChan chan audiocore.AudioLevelData
}

// New constructs the audio/streaming domain handler around the shared core and
// the facade-injected auth service. The audio-level channel is wired later via
// SetAudioLevelChan.
func New(core *apicore.Core, authService auth.Service) *Handler {
	return &Handler{
		Core:        core,
		authService: authService,
	}
}

// isClientAuthenticated reports whether the current request is authenticated by
// delegating to the auth service's centralized IsAuthenticated method. It returns
// false when no auth service is configured (matching the facade's behavior for the
// audio-level stream).
func (c *Handler) isClientAuthenticated(ctx echo.Context) bool {
	if c.authService == nil {
		return false
	}
	return c.authService.IsAuthenticated(ctx)
}
