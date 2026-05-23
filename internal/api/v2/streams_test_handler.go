package api

import (
	"fmt"
	"maps"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/audiocore/ffmpeg"
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

var allowedSchemes = map[string]bool{
	"rtsp":  true,
	"rtsps": true,
	"http":  true,
	"https": true,
	"rtmp":  true,
	"udp":   true,
}

var blockedHosts = map[string]bool{
	"169.254.169.254":          true,
	"metadata.google.internal": true,
	"metadata.internal":        true,
	"localhost":                true,
}

// initStreamTestRoutes registers stream testing endpoints.
func (c *Controller) initStreamTestRoutes() {
	c.Group.POST("/streams/test", c.TestStream, c.authMiddleware)
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

	info, err := ffmpeg.ProbeStreamInfo(ctx.Request().Context(), req.URL)
	if err != nil {
		return c.HandleErrorWithKey(ctx, err, "stream connection failed",
			http.StatusBadGateway, "errors.streams.test.connectionFailed", nil)
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

	return ctx.JSON(http.StatusOK, resp)
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
