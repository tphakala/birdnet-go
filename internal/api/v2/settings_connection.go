package api

import (
	"net"
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/security/proxytrust"
)

// connectionCheckCacheControl keeps the answer, which is specific to one
// request, out of every cache.
const connectionCheckCacheControl = "no-store"

// ConnectionCheckResponse is the body of GET /api/v2/settings/connection: how
// the requester's own connection relates to the saved security.trustedProxies
// list. It describes only the current request and the current settings.
type ConnectionCheckResponse struct {
	// Status is the proxytrust.ConnectionStatus string: direct, trusted_proxy,
	// trusted_proxy_unverified, untrusted_public_proxy, untrusted_proxy or
	// untrusted_proxy_unverifiable.
	Status proxytrust.ConnectionStatus `json:"status"`
	// PeerAddress is the connection peer, or "" when it cannot be parsed.
	PeerAddress string `json:"peerAddress"`
	// HasForwardedHeaders reports whether the request carries any client-IP
	// header (X-Forwarded-For, X-Real-IP, CF-Connecting-IP and so on).
	HasForwardedHeaders bool `json:"hasForwardedHeaders"`
	// PeerIsTrustedProxy reports whether the peer is inside a saved trusted
	// proxy range.
	PeerIsTrustedProxy bool `json:"peerIsTrustedProxy"`
	// VerifiedClientAddress is the client address the authentication decision
	// uses for this request, or "" when none could be verified.
	VerifiedClientAddress string `json:"verifiedClientAddress"`
	// PeerIsCloudflareEdge reports whether the peer is inside Cloudflare's
	// published edge ranges, whether or not the preset is saved.
	PeerIsCloudflareEdge bool `json:"peerIsCloudflareEdge"`
	// CloudflarePresetWouldVerify reports that listing the peer together with
	// the cloudflare preset would verify the forwarded headers, which they do
	// not today.
	CloudflarePresetWouldVerify bool `json:"cloudflarePresetWouldVerify"`
}

// GetConnectionCheck handles GET /api/v2/settings/connection. It classifies the
// requester's own connection against the saved security.trustedProxies list
// with proxytrust.Checker.Classify, the classifier the subnet bypass log
// notices use, so the answer matches the authentication decision. The list is
// read from the live settings snapshot on every request, so a save takes effect
// on the next check without a restart. The request is classified from its
// connection peer and headers, never from the attribution address (RealIP).
func (c *Controller) GetConnectionCheck(ctx echo.Context) error {
	conn := c.proxyTrust.ResolveSettings(c.ControllerSettings()).Classify(ctx.Request())
	c.LogAPIRequest(ctx, logger.LogLevelDebug, "Classified connection", logger.String("status", string(conn.Status)))

	ctx.Response().Header().Set(echo.HeaderCacheControl, connectionCheckCacheControl)
	return ctx.JSON(http.StatusOK, ConnectionCheckResponse{
		Status:                      conn.Status,
		PeerAddress:                 ipString(conn.Peer),
		HasForwardedHeaders:         conn.HasClientIPHeaders,
		PeerIsTrustedProxy:          conn.PeerIsConfiguredProxy,
		VerifiedClientAddress:       ipString(conn.VerifiedClient),
		PeerIsCloudflareEdge:        conn.PeerIsCloudflareEdge,
		CloudflarePresetWouldVerify: conn.CloudflarePresetWouldVerify,
	})
}

// ipString returns ip as text, or "" for a nil address.
func ipString(ip net.IP) string {
	if ip == nil {
		return ""
	}
	return ip.String()
}
