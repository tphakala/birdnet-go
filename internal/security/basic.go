package security

import (
	"net"
	"net/http"

	"github.com/gorilla/sessions"
	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// IsInLocalSubnet checks if the given IP is in the same subnet as any local network interface.
// Supports both IPv4 and IPv6 addresses.
func IsInLocalSubnet(clientIP net.IP) bool {
	if clientIP == nil {
		GetLogger().Debug("IsInLocalSubnet check failed: client IP is nil")
		return false
	}
	secLog := GetLogger().With(logger.String("ip", clientIP.String()))

	// IPv6 link-local addresses (fe80::/10) are by definition on the local
	// network segment; routers do not forward them (RFC 4291).
	if clientIP.IsLinkLocalUnicast() {
		secLog.Debug("Client IP is link-local unicast, treating as local subnet")
		return true
	}

	// If running in container, check if client IP is in the same subnet as the host
	if conf.RunningInContainer() {
		isInHostSubnet := conf.IsInHostSubnet(clientIP)
		secLog.Debug("Running in container, checking host subnet", logger.Bool("is_in_host_subnet", isInHostSubnet))
		return isInHostSubnet
	}

	addrs, err := net.InterfaceAddrs()
	if err != nil {
		secLog.Warn("Failed to get network interface addresses", logger.Error(err))
		return false
	}

	// Check each network interface using the actual subnet mask,
	// supporting both IPv4 and IPv6 subnets.
	for _, addr := range addrs {
		ipnet, ok := addr.(*net.IPNet)
		if !ok || ipnet.IP.IsLoopback() {
			continue
		}

		if ipnet.Contains(clientIP) {
			secLog.Debug("Client IP is in local subnet",
				logger.String("server_ip", ipnet.IP.String()),
				logger.String("network", ipnet.String()))
			return true
		}
	}
	secLog.Debug("Client IP is not in any local subnet")
	return false
}

// buildSessionOptions creates session options with standard security settings.
// The secure parameter controls whether cookies require HTTPS.
// The maxAge parameter sets the session duration in seconds.
func buildSessionOptions(secure bool, maxAge int) *sessions.Options {
	return &sessions.Options{
		Path:     "/",
		MaxAge:   maxAge,
		Secure:   secure,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	}
}

// HandleBasicAuthorize handles the basic authorization flow
func (s *OAuth2Server) HandleBasicAuthorize(c echo.Context) error {
	clientID := c.QueryParam("client_id")
	redirectURI := c.QueryParam("redirect_uri")
	secLog := GetLogger().With(logger.String("client_id", clientID), logger.String("redirect_uri", redirectURI))
	secLog.Info("Handling basic authorization request")

	// Read the live snapshot once so UI changes to the client id and redirect URI
	// apply without a restart (issue #3370).
	settings := s.currentSettings()
	if settings == nil {
		secLog.Error("Basic authorization failed: settings unavailable")
		return c.String(http.StatusInternalServerError, "Internal server error")
	}
	expectedClientID := settings.Security.BasicAuth.ClientID
	if clientID != expectedClientID {
		secLog.Warn("Invalid client_id provided", logger.String("expected", expectedClientID))
		return c.String(http.StatusBadRequest, "Invalid client_id")
	}

	// Validate redirect URI using the shared function and the live expected URI
	if err := ValidateRedirectURI(redirectURI, parseBasicAuthRedirectURI(settings)); err != nil {
		secLog.Warn("Redirect URI validation failed", logger.Error(err))
		// Return the specific error message for better client-side debugging
		return c.String(http.StatusBadRequest, err.Error())
	}

	// Generate an auth code
	secLog.Debug("Generating authorization code")
	authCode, err := s.GenerateAuthCode(settings, false)
	if err != nil {
		secLog.Warn("Basic authorization failed: credentials changed", logger.Error(err))
		return c.String(http.StatusUnauthorized, "Unauthorized")
	}

	// DO NOT log the authCode itself
	secLog.Info("Authorization code generated successfully, redirecting user")
	return c.Redirect(http.StatusFound, redirectURI+"?code="+authCode)
}
