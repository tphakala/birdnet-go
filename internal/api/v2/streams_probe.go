package api

import (
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/audiocore/ffmpeg"
	"github.com/tphakala/birdnet-go/internal/logger"
)

const minBatSampleRate = 96000

type probeRequest struct {
	URL       string `json:"url"`
	Transport string `json:"transport,omitempty"`
}

type probeResponse struct {
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
}

// initStreamProbeRoutes registers stream probing endpoints.
func (c *Controller) initStreamProbeRoutes() {
	c.Group.POST("/streams/probe", c.ProbeStream, c.authMiddleware)
}

// ProbeStream probes a stream URL to discover its audio properties.
// Used by the frontend to check bat model compatibility before configuration.
func (c *Controller) ProbeStream(ctx echo.Context) error {
	var req probeRequest
	if err := ctx.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}

	if err := validateProbeURL(req.URL); err != nil {
		return err
	}

	info, err := ffmpeg.ProbeStreamInfo(ctx.Request().Context(), req.URL)
	if err != nil {
		c.logAPIRequest(ctx, logger.LogLevelWarn, "stream probe failed",
			logger.Error(err),
			logger.String("operation", "probe_stream"))
		return echo.NewHTTPError(http.StatusBadGateway, "failed to probe stream")
	}

	resp := probeResponse{
		SampleRate: info.SampleRate,
		Channels:   info.Channels,
		Codec:      info.Codec,
	}

	resp.BatCompatible = info.SampleRate >= minBatSampleRate && !ffmpeg.IsLossyCodec(info.Codec)

	if ffmpeg.IsLossyCodec(info.Codec) {
		resp.Warnings = append(resp.Warnings,
			"Lossy codec ("+info.Codec+") destroys ultrasonic content above ~20 kHz")
	}
	if info.SampleRate < minBatSampleRate {
		resp.Warnings = append(resp.Warnings,
			"Sample rate below bat model minimum (96 kHz)")
	}

	return ctx.JSON(http.StatusOK, resp)
}

// validateProbeURL checks that the URL uses an allowed scheme and does not
// target cloud metadata endpoints. Private RFC1918 IPs are allowed since
// BirdNET-Go runs on home networks.
func validateProbeURL(rawURL string) error {
	if rawURL == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "url is required")
	}

	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid URL")
	}

	if !allowedSchemes[strings.ToLower(parsed.Scheme)] {
		return echo.NewHTTPError(http.StatusBadRequest, "unsupported URL scheme")
	}

	host := parsed.Hostname()
	if blockedHosts[host] {
		return echo.NewHTTPError(http.StatusForbidden, "blocked destination")
	}

	if ip := net.ParseIP(host); ip != nil && ip.IsLinkLocalUnicast() {
		return echo.NewHTTPError(http.StatusForbidden, "blocked destination")
	}

	return nil
}
