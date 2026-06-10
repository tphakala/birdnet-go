package api

import (
	"context"
	stderrors "errors"
	"fmt"
	"maps"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/audiocore/ffmpeg"
	"github.com/tphakala/birdnet-go/internal/conf"
)

type testStreamRequest struct {
	URL string `json:"url"`
}

type testStreamResponse struct {
	SampleRate    int      `json:"sampleRate"`
	Channels      int      `json:"channels"`
	Codec         string   `json:"codec"`
	BatCompatible bool     `json:"batCompatible"`
	Warnings      []string `json:"warnings"`
}

type analyzeChannelsRequest struct {
	URL string `json:"url"`
}

type analyzeChannelsResponse struct {
	Channels    int                    `json:"channels"`
	Energy      []ffmpeg.ChannelEnergy `json:"energy"`
	Recommended string                 `json:"recommended"`
}

var allowedSchemes = map[string]bool{
	"rtsp":  true,
	"rtsps": true,
	"http":  true,
	"https": true,
	"rtmp":  true,
	"rtmps": true,
	"udp":   true,
	"rtp":   true,
}

var blockedHosts = map[string]bool{
	"169.254.169.254":          true,
	"metadata.google.internal": true,
	"metadata.internal":        true,
	"localhost":                true,
}

// probeStreamInfoFunc probes a live stream for its audio characteristics.
// Controller.probeStreamInfo defaults to ffmpeg.ProbeStreamInfo (used when the
// field is nil) and is overridden in tests to stub probing without ffprobe.
type probeStreamInfoFunc func(ctx context.Context, url string) (*ffmpeg.StreamInfo, error)

// initStreamTestRoutes registers stream testing endpoints.
func (c *Controller) initStreamTestRoutes() {
	c.Group.POST("/streams/test", c.TestStream, c.authMiddleware)
	c.Group.POST("/streams/analyze-channels", c.AnalyzeChannels, c.authMiddleware)
}

// TestStream tests a stream URL to discover its audio properties.
// Used by the frontend to verify connectivity and check model compatibility
// before saving a stream configuration.
func (c *Controller) TestStream(ctx echo.Context) error {
	var req testStreamRequest
	if err := ctx.Bind(&req); err != nil {
		return c.HandleErrorWithKey(ctx, err, "invalid request body",
			http.StatusBadRequest, "errors.streams.test.invalidBody", nil)
	}

	if vErr := validateStreamTestURL(req.URL); vErr != nil {
		return c.HandleErrorWithKey(ctx, nil, vErr.message,
			vErr.status, vErr.errorKey, vErr.params)
	}

	probe := c.probeStreamInfo
	if probe == nil {
		probe = ffmpeg.ProbeStreamInfo
	}

	info, err := probe(ctx.Request().Context(), req.URL)
	if err != nil {
		message := "stream connection failed"
		errorKey := "errors.streams.test.connectionFailed"
		status := http.StatusBadGateway

		if stderrors.Is(err, ffmpeg.ErrNoAudioStreamsFound) {
			message = "stream has no audio track"
			errorKey = "errors.streams.test.noAudioTrack"
			status = http.StatusUnprocessableEntity
		}

		return c.HandleErrorWithKey(ctx, err, message, status, errorKey, nil)
	}

	lossy := ffmpeg.IsLossyCodec(info.Codec)

	resp := testStreamResponse{
		SampleRate:    info.SampleRate,
		Channels:      info.Channels,
		Codec:         info.Codec,
		BatCompatible: info.SampleRate >= ffmpeg.MinBatSampleRate && !lossy,
		Warnings:      []string{},
	}

	if lossy {
		resp.Warnings = append(resp.Warnings,
			"Lossy codec ("+info.Codec+") destroys ultrasonic content above ~20 kHz")
	}
	if info.SampleRate < ffmpeg.MinBatSampleRate {
		resp.Warnings = append(resp.Warnings,
			"Sample rate below bat model minimum (96 kHz)")
	}
	if info.Channels > 1 {
		resp.Warnings = append(resp.Warnings,
			"Stream sends stereo audio. AI inference works on mono only. "+
				"Select a specific channel (left/right) for best detection accuracy, "+
				"or use the channel detector to find the active microphone channel.")
	}

	return ctx.JSON(http.StatusOK, resp)
}

// AnalyzeChannels captures a short stereo sample and returns per-channel
// energy levels with a recommendation for which channel to use.
func (c *Controller) AnalyzeChannels(ctx echo.Context) error {
	var req analyzeChannelsRequest
	if err := ctx.Bind(&req); err != nil {
		return c.HandleErrorWithKey(ctx, err, "invalid request body",
			http.StatusBadRequest, "errors.streams.test.invalidBody", nil)
	}

	if vErr := validateStreamTestURL(req.URL); vErr != nil {
		return c.HandleErrorWithKey(ctx, nil, vErr.message,
			vErr.status, vErr.errorKey, vErr.params)
	}

	analysis, err := ffmpeg.AnalyzeChannelEnergy(ctx.Request().Context(), req.URL, conf.Setting().Realtime.Audio.FfmpegPath)
	if err != nil {
		return c.HandleErrorWithKey(ctx, err, "channel analysis failed",
			http.StatusBadGateway, "errors.streams.analyzeChannels.failed", nil)
	}

	return ctx.JSON(http.StatusOK, analyzeChannelsResponse{
		Channels:    analysis.Channels,
		Energy:      analysis.Energy,
		Recommended: analysis.Recommended,
	})
}

type streamTestValidationError struct {
	message  string
	errorKey string
	params   map[string]any
	status   int
}

// validateStreamTestURL checks that the URL uses an allowed scheme and does not
// target cloud metadata or loopback endpoints. Private RFC1918 IPs are
// allowed since BirdNET-Go runs on home networks.
func validateStreamTestURL(rawURL string) *streamTestValidationError {
	if rawURL == "" {
		return &streamTestValidationError{
			message:  "URL is required",
			errorKey: "errors.streams.test.urlRequired",
			status:   http.StatusBadRequest,
		}
	}

	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme == "" {
		return &streamTestValidationError{
			message:  "invalid URL format",
			errorKey: "errors.streams.test.invalidUrl",
			status:   http.StatusBadRequest,
		}
	}

	scheme := strings.ToLower(parsed.Scheme)
	if !allowedSchemes[scheme] {
		schemes := slices.Collect(maps.Keys(allowedSchemes))
		slices.Sort(schemes)
		supportedList := strings.Join(schemes, ", ")
		return &streamTestValidationError{
			message:  fmt.Sprintf("unsupported URL scheme %q, supported: %s", scheme, supportedList),
			errorKey: "errors.streams.test.unsupportedScheme",
			params:   map[string]any{"scheme": scheme},
			status:   http.StatusBadRequest,
		}
	}

	host := parsed.Hostname()
	if blockedHosts[strings.ToLower(host)] {
		return &streamTestValidationError{
			message:  "blocked destination",
			errorKey: "errors.streams.test.blockedDestination",
			status:   http.StatusForbidden,
		}
	}

	if ip := net.ParseIP(host); ip != nil {
		if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() {
			return &streamTestValidationError{
				message:  "blocked destination",
				errorKey: "errors.streams.test.blockedDestination",
				status:   http.StatusForbidden,
			}
		}
	}

	return nil
}
