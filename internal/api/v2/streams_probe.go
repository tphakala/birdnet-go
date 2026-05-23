package api

import (
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/audiocore/ffmpeg"
)

type probeRequest struct {
	URL string `json:"url"`
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
	"localhost":                true,
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
		return c.HandleError(ctx, err, "invalid request body", http.StatusBadRequest)
	}

	if err := validateProbeURL(req.URL); err != nil {
		return err
	}

	info, err := ffmpeg.ProbeStreamInfo(ctx.Request().Context(), req.URL)
	if err != nil {
		return c.HandleError(ctx, err, "failed to probe stream", http.StatusBadGateway)
	}

	lossy := ffmpeg.IsLossyCodec(info.Codec)

	resp := probeResponse{
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

// validateProbeURL checks that the URL uses an allowed scheme and does not
// target cloud metadata or loopback endpoints. Private RFC1918 IPs are
// allowed since BirdNET-Go runs on home networks.
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
	if blockedHosts[strings.ToLower(host)] {
		return echo.NewHTTPError(http.StatusForbidden, "blocked destination")
	}

	if ip := net.ParseIP(host); ip != nil {
		if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() {
			return echo.NewHTTPError(http.StatusForbidden, "blocked destination")
		}
	}

	return nil
}
