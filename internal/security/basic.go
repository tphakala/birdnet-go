package security

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/sessions"
	"github.com/labstack/echo/v4"
	"github.com/markbates/goth/gothic"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// IsInLocalSubnet checks if the given IP is in the same subnet as any local network interface
func IsInLocalSubnet(clientIP net.IP) bool {
	if clientIP == nil {
		return false
	}

	// If running in container, check if client IP is in the same subnet as the host
	if conf.RunningInContainer() {
		return conf.IsInHostSubnet(clientIP)
	}

	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return false
	}

	// Get the client's /24 subnet
	clientSubnet := getIPv4Subnet(clientIP)
	if clientSubnet == nil {
		return false
	}

	// Check each network interface
	for _, addr := range addrs {
		ipnet, ok := addr.(*net.IPNet)
		if !ok || ipnet.IP.IsLoopback() {
			continue
		}

		serverSubnet := getIPv4Subnet(ipnet.IP)
		if serverSubnet != nil && clientSubnet.Equal(serverSubnet) {
			return true
		}
	}
	return false
}

// getIPv4Subnet converts an IP address to its /24 subnet address
func getIPv4Subnet(ip net.IP) net.IP {
	if ip == nil {
		return nil
	}

	// Convert to IPv4 if possible
	ipv4 := ip.To4()
	if ipv4 == nil {
		return nil
	}

	// Get the /24 subnet
	return ipv4.Mask(net.CIDRMask(24, 32))
}

// configureLocalNetworkCookieStore configures the cookie store for local network access
func (s *OAuth2Server) configureLocalNetworkCookieStore() {
	// Configure session options based on store type
	switch store := gothic.Store.(type) {
	case *sessions.CookieStore:
		store.Options = &sessions.Options{
			Path: "/",
			// Allow cookies to be sent over HTTP, this is for development purposes only
			// and is allowed only for local LAN access
			Secure:   false,
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
		}
	case *sessions.FilesystemStore:
		store.Options = &sessions.Options{
			Path:     "/",
			MaxAge:   86400 * 7, // 7 days
			Secure:   false,     // Allow cookies to be sent over HTTP for local LAN access
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
		}
	default:
		log.Printf("Warning: Unknown session store type %T, using default cookie store options", store)
		// Create a default cookie store as fallback - only for reference, not actually used
		// Use the configured session secret instead of a hardcoded value
		sessionSecret := s.Settings.Security.SessionSecret
		if sessionSecret == "" {
			// If no session secret is configured, use a pseudo-random value
			// This is still not ideal but better than a hardcoded string
			sessionSecret = fmt.Sprintf("birdnet-go-%d", time.Now().UnixNano())
			log.Printf("Warning: No session secret configured, using temporary value")
		}

		// Note: This store is not actually used, it's only created as a reference
		// for what options would be applied to a proper store. The warning above
		// alerts operators about the unknown store type.
		_ = sessions.NewCookieStore([]byte(sessionSecret))
	}
}

// HandleBasicAuthorize handles the basic authorization flow
func (s *OAuth2Server) HandleBasicAuthorize(c echo.Context) error {
	clientID := c.QueryParam("client_id")
	redirectURI := c.QueryParam("redirect_uri")

	if clientID != s.Settings.Security.BasicAuth.ClientID {
		return c.String(http.StatusBadRequest, "Invalid client_id")
	}

	if redirectURI != s.Settings.Security.BasicAuth.RedirectURI {
		return c.String(http.StatusBadRequest, "Invalid redirect_uri")
	}

	// Generate an auth code
	authCode, err := s.GenerateAuthCode()
	if err != nil {
		return c.String(http.StatusInternalServerError, "Error generating auth code")
	}

	return c.Redirect(http.StatusFound, redirectURI+"?code="+authCode)
}

// HandleBasicAuthToken handles the basic authorization token flow
func (s *OAuth2Server) HandleBasicAuthToken(c echo.Context) error {
	// Verify client credentials from Authorization header
	clientID, clientSecret, ok := c.Request().BasicAuth()
	if !ok || clientID != s.Settings.Security.BasicAuth.ClientID || clientSecret != s.Settings.Security.BasicAuth.ClientSecret {
		s.Debug("Invalid client credentials: %s", clientID)
		return c.JSON(http.StatusUnauthorized, map[string]string{"error": "Invalid client id or secret"})
	}

	// Check if client is in local subnet and configure cookie store accordingly
	if clientIP := net.ParseIP(c.RealIP()); IsInLocalSubnet(clientIP) {
		// For clients in the local subnet, allow non-HTTPS cookies
		s.Debug("Client in local subnet, configuring cookie store accordingly")
		s.configureLocalNetworkCookieStore()
	}

	grantType := c.FormValue("grant_type")
	code := c.FormValue("code")
	redirectURI := c.FormValue("redirect_uri")

	s.Debug("Token request: grant_type=%s, code=%s, redirect_uri=%s", grantType, code, redirectURI)

	// Check for required fields
	if grantType == "" || code == "" || redirectURI == "" {
		s.Debug("Missing required fields in token request")
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "Missing required fields"})
	}

	// Verify grant type
	if grantType != "authorization_code" {
		s.Debug("Unsupported grant type: %s", grantType)
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "Unsupported grant type"})
	}

	// Verify redirect URI
	if !strings.Contains(redirectURI, s.Settings.Security.Host) {
		s.Debug("Invalid redirect URI host: %s, expected %s", redirectURI, s.Settings.Security.Host)
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid host for redirect URI"})
	}

	// Exchange the authorization code for an access token
	accessToken, err := s.ExchangeAuthCode(code)
	if err != nil {
		s.Debug("Failed to exchange auth code: %v", err)
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid authorization code"})
	}

	// Store the access token in Gothic session
	if err := gothic.StoreInSession("access_token", accessToken, c.Request(), c.Response()); err != nil {
		s.Debug("Failed to store access token in session: %v", err)
		// Continue anyway since we'll return the token to the client
	}

	// Ensure content type is set explicitly
	c.Response().Header().Set("Content-Type", "application/json")

	// Return the access token in the response body
	resp := map[string]string{
		"access_token": accessToken,
		"token_type":   "Bearer",
		"expires_in":   s.Settings.Security.BasicAuth.AccessTokenExp.String(),
	}

	s.Debug("Successfully exchanged token, returning response")
	return c.JSON(http.StatusOK, resp)
}

// HandleBasicAuthCallback handles the basic authorization callback flow
func (s *OAuth2Server) HandleBasicAuthCallback(c echo.Context) error {
	code := c.QueryParam("code")
	redirect := c.QueryParam("redirect")
	if code == "" {
		return c.String(http.StatusBadRequest, "Missing authorization code")
	}

	// Instead of exchanging the code here, we'll pass it to the client
	return c.Render(http.StatusOK, "callback", map[string]interface{}{
		"Code":        code,
		"RedirectURL": redirect,
		"ClientID":    s.Settings.Security.BasicAuth.ClientID,
		"Secret":      s.Settings.Security.BasicAuth.ClientSecret,
	})
}
