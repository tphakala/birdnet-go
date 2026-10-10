package audio

import (
	"context"
	"fmt"
	"maps"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/audiocore/ffmpeg"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/httpclient"
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
	"ip6-localhost":            true,
	"ip6-loopback":             true,
}

// localhostSuffix marks names that resolvers commonly map to loopback
// (RFC 6761), such as cam.localhost.
const localhostSuffix = ".localhost"

// streamHostLookupTimeout bounds DNS resolution of a stream test host.
const streamHostLookupTimeout = 3 * time.Second

// lookupHostFunc resolves a hostname to its IP addresses. Handler.lookupStreamHost
// defaults to the system resolver (used when the field is nil) and is overridden
// in tests so they never touch real DNS.
type lookupHostFunc func(ctx context.Context, host string) ([]netip.Addr, error)

func defaultLookupHost(ctx context.Context, host string) ([]netip.Addr, error) {
	return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
}

// probeStreamInfoFunc probes a live stream for its audio characteristics.
// Handler.probeStreamInfo defaults to ffmpeg.ProbeStreamInfo (used when the
// field is nil) and is overridden in tests to stub probing without ffprobe.
type probeStreamInfoFunc func(ctx context.Context, url string) (*ffmpeg.StreamInfo, error)

// RegisterStreamTestRoutes registers stream testing endpoints.
func (c *Handler) RegisterStreamTestRoutes(g *echo.Group) {
	g.POST("/streams/test", c.TestStream, c.AuthMiddleware)
	g.POST("/streams/analyze-channels", c.AnalyzeChannels, c.AuthMiddleware)
}

// TestStream tests a stream URL to discover its audio properties. The URL host
// is validated and resolved first (see validateStreamTestURL); blocked targets
// are refused before any probe runs.
// Used by the frontend to verify connectivity and check model compatibility
// before saving a stream configuration.
func (c *Handler) TestStream(ctx echo.Context) error {
	var req testStreamRequest
	if err := ctx.Bind(&req); err != nil {
		return c.HandleErrorWithKey(ctx, err, "invalid request body",
			http.StatusBadRequest, "errors.streams.test.invalidBody", nil)
	}

	if vErr := validateStreamTestURL(ctx.Request().Context(), req.URL, c.lookupStreamHost); vErr != nil {
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

		if errors.Is(err, ffmpeg.ErrNoAudioStreamsFound) {
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
// energy levels with a recommendation for which channel to use. The URL goes
// through the same host validation as TestStream before any capture starts.
func (c *Handler) AnalyzeChannels(ctx echo.Context) error {
	var req analyzeChannelsRequest
	if err := ctx.Bind(&req); err != nil {
		return c.HandleErrorWithKey(ctx, err, "invalid request body",
			http.StatusBadRequest, "errors.streams.test.invalidBody", nil)
	}

	if vErr := validateStreamTestURL(ctx.Request().Context(), req.URL, c.lookupStreamHost); vErr != nil {
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

// blockedDestinationError is the validation error for a refused target.
func blockedDestinationError() *streamTestValidationError {
	return &streamTestValidationError{
		message:  "blocked destination",
		errorKey: "errors.streams.test.blockedDestination",
		status:   http.StatusForbidden,
	}
}

// isNumericHostForm reports whether every dot-separated label of host is a
// decimal, octal or 0x-prefixed hex number. Such hosts are legacy inet_aton
// spellings of an IPv4 address (2130706433, 0x7f000001, 0177.0.0.1, 127.1)
// that ffmpeg's resolver accepts but netip.ParseAddr does not, so they are
// rejected rather than interpreted. Ordinary hostnames always contain a label
// that is not purely numeric.
func isNumericHostForm(host string) bool {
	if host == "" {
		return false
	}
	for label := range strings.SplitSeq(host, ".") {
		if label == "" {
			return false
		}
		digits := label
		if len(label) > 2 && label[0] == '0' && (label[1] == 'x' || label[1] == 'X') {
			digits = label[2:]
			for _, r := range digits {
				if (r < '0' || r > '9') && (r < 'a' || r > 'f') && (r < 'A' || r > 'F') {
					return false
				}
			}
			continue
		}
		for _, r := range digits {
			if r < '0' || r > '9' {
				return false
			}
		}
	}
	return true
}

// validateStreamTestURL checks that the URL uses an allowed scheme and that its
// host is not a cloud metadata, loopback, link-local or unspecified endpoint.
// IP literals are checked directly, legacy numeric spellings of an IPv4 address
// are refused, and any other hostname is resolved with lookup and refused when
// any returned address is blocked. The address policy is
// httpclient.IsBlockedStreamTarget. Private RFC1918 and ULA addresses are
// allowed since BirdNET-Go runs on home networks. A nil lookup uses the system
// resolver.
//
// Residual risk: this is a pre-flight check, not a connection guard. ffmpeg
// resolves the host again when it connects (so DNS rebinding between this check
// and the probe is possible) and follows HTTP redirects, so a permitted URL can
// still reach a blocked address. The check narrows the endpoint's use as a
// probe of local services but cannot eliminate it; the endpoint requires
// authentication, and an authenticated admin can already configure any stream
// URL.
func validateStreamTestURL(ctx context.Context, rawURL string, lookup lookupHostFunc) *streamTestValidationError {
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

	host := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
	if host == "" {
		return &streamTestValidationError{
			message:  "invalid URL format",
			errorKey: "errors.streams.test.invalidUrl",
			status:   http.StatusBadRequest,
		}
	}
	if blockedHosts[host] || strings.HasSuffix(host, localhostSuffix) {
		return blockedDestinationError()
	}

	if addr, perr := netip.ParseAddr(host); perr == nil {
		if httpclient.IsBlockedStreamTarget(addr) {
			return blockedDestinationError()
		}
		return nil
	}

	if isNumericHostForm(host) {
		return blockedDestinationError()
	}

	if lookup == nil {
		lookup = defaultLookupHost
	}
	lookupCtx, cancel := context.WithTimeout(ctx, streamHostLookupTimeout)
	defer cancel()
	addrs, err := lookup(lookupCtx, host)
	if err != nil || len(addrs) == 0 {
		return &streamTestValidationError{
			message:  "stream host could not be resolved",
			errorKey: "errors.streams.test.connectionFailed",
			status:   http.StatusBadGateway,
		}
	}
	if slices.ContainsFunc(addrs, httpclient.IsBlockedStreamTarget) {
		return blockedDestinationError()
	}

	return nil
}
