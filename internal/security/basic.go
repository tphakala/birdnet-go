package security

import (
	"context"
	"errors"
	"fmt"
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
	logger := securityLogger.With("ip", clientIP.String())
	if clientIP == nil {
		logger.Debug("IsInLocalSubnet check failed: client IP is nil")
		return false
	}

	// If running in container, check if client IP is in the same subnet as the host
	if conf.RunningInContainer() {
		isInHostSubnet := conf.IsInHostSubnet(clientIP)
		logger.Debug("Running in container, checking host subnet", "is_in_host_subnet", isInHostSubnet)
		return isInHostSubnet
	}

	addrs, err := net.InterfaceAddrs()
	if err != nil {
		logger.Warn("Failed to get network interface addresses", "error", err)
		return false
	}

	// Get the client's /24 subnet
	clientSubnet := getIPv4Subnet(clientIP)
	if clientSubnet == nil {
		logger.Debug("Failed to get IPv4 /24 subnet for client IP")
		return false
	}
	logger = logger.With("client_subnet", clientSubnet.String())

	// Check each network interface
	for _, addr := range addrs {
		ipnet, ok := addr.(*net.IPNet)
		if !ok || ipnet.IP.IsLoopback() {
			continue
		}

		serverSubnet := getIPv4Subnet(ipnet.IP)
		if serverSubnet != nil {
			logger.Debug("Checking against server interface", "server_ip", ipnet.IP.String(), "server_subnet", serverSubnet.String())
			if clientSubnet.Equal(serverSubnet) {
				logger.Debug("Client IP is in local subnet")
				return true
			}
		}
	}
	logger.Debug("Client IP is not in any local subnet")
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
	securityLogger.Info("Configuring cookie store for local network access (allowing non-HTTPS cookies)")
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
		// Log the warning using structured logger
		securityLogger.Warn("Unknown session store type, using default cookie store options", "store_type", fmt.Sprintf("%T", store))
		// log.Printf("Warning: Unknown session store type %T, using default cookie store options", store)
		// Create a default cookie store as fallback - only for reference, not actually used
		// Use the configured session secret instead of a hardcoded value
		sessionSecret := s.Settings.Security.SessionSecret
		if sessionSecret == "" {
			// If no session secret is configured, use a pseudo-random value
			// This is still not ideal but better than a hardcoded string
			sessionSecret = fmt.Sprintf("birdnet-go-%d", time.Now().UnixNano())
			// Log the warning using structured logger
			securityLogger.Warn("No session secret configured, using temporary value")
			// log.Printf("Warning: No session secret configured, using temporary value")
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
	logger := securityLogger.With("client_id", clientID, "redirect_uri", redirectURI)
	logger.Info("Handling basic authorization request")

	if clientID != s.Settings.Security.BasicAuth.ClientID {
		logger.Warn("Invalid client_id provided", "expected", s.Settings.Security.BasicAuth.ClientID)
		return c.String(http.StatusBadRequest, "Invalid client_id")
	}

	if redirectURI != s.Settings.Security.BasicAuth.RedirectURI {
		logger.Warn("Invalid redirect_uri provided", "expected", s.Settings.Security.BasicAuth.RedirectURI)
		return c.String(http.StatusBadRequest, "Invalid redirect_uri")
	}

	// Generate an auth code
	logger.Debug("Generating authorization code")
	authCode, err := s.GenerateAuthCode()
	if err != nil {
		logger.Error("Failed to generate authorization code", "error", err)
		return c.String(http.StatusInternalServerError, "Error generating auth code")
	}

	// DO NOT log the authCode itself
	logger.Info("Authorization code generated successfully, redirecting user")
	return c.Redirect(http.StatusFound, redirectURI+"?code="+authCode)
}

// HandleBasicAuthToken handles the basic authorization token flow
func (s *OAuth2Server) HandleBasicAuthToken(c echo.Context) error {
	// Verify client credentials from Authorization header
	// Log the attempt, but DO NOT log the clientSecret
	clientID, clientSecret, ok := c.Request().BasicAuth()
	logger := securityLogger.With("client_id", clientID)
	logger.Info("Handling basic authorization token request")

	if !ok {
		logger.Warn("Basic auth header missing or malformed")
		return c.JSON(http.StatusUnauthorized, map[string]string{"error": "Missing or malformed Authorization header"})
	}

	if clientID != s.Settings.Security.BasicAuth.ClientID || clientSecret != s.Settings.Security.BasicAuth.ClientSecret {
		logger.Warn("Invalid client credentials provided")
		return c.JSON(http.StatusUnauthorized, map[string]string{"error": "Invalid client id or secret"})
	}

	// Check if client is in local subnet and configure cookie store accordingly
	if clientIP := net.ParseIP(c.RealIP()); IsInLocalSubnet(clientIP) {
		// For clients in the local subnet, allow non-HTTPS cookies
		logger.Info("Client is in local subnet, configuring cookie store for non-HTTPS")
		s.configureLocalNetworkCookieStore()
	}

	grantType := c.FormValue("grant_type")
	code := c.FormValue("code") // Do not log the code
	redirectURI := c.FormValue("redirect_uri")

	logger.Info("Received token request parameters", "grant_type", grantType, "redirect_uri", redirectURI)

	// Check for required fields
	if grantType == "" || code == "" || redirectURI == "" {
		logger.Warn("Missing required fields in token request")
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "Missing required fields"})
	}

	// Verify grant type
	if grantType != "authorization_code" {
		logger.Warn("Unsupported grant type provided", "grant_type", grantType)
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "Unsupported grant type"})
	}

	// Verify redirect URI
	if !strings.Contains(redirectURI, s.Settings.Security.Host) {
		logger.Warn("Invalid redirect URI host", "redirect_uri", redirectURI, "expected_host", s.Settings.Security.Host)
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid host for redirect URI"})
	}

	// Exchange the authorization code for an access token
	// Do not log the code being exchanged
	logger.Info("Attempting to exchange authorization code for access token")
	// Pass the request context to ExchangeAuthCode
	accessToken, err := s.ExchangeAuthCode(c.Request().Context(), code)
	if err != nil {
		logger.Warn("Failed to exchange authorization code", "error", err)
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid authorization code"})
	}
	// DO NOT log the accessToken
	logger.Info("Successfully exchanged authorization code for access token")

	// Store the access token in Gothic session
	// Do not log the token here either
	if err := gothic.StoreInSession("access_token", accessToken, c.Request(), c.Response()); err != nil {
		logger.Warn("Failed to store access token in session", "error", err)
		// Continue anyway since we'll return the token to the client
	}

	// Ensure content type is set explicitly
	c.Response().Header().Set("Content-Type", "application/json")

	// Return the access token in the response body
	expiresInSeconds := int(s.Settings.Security.BasicAuth.AccessTokenExp.Seconds())
	resp := map[string]interface{}{ // Use interface{} for mixed types
		"access_token": accessToken, // This is sent to the client, unavoidable
		"token_type":   "Bearer",
		"expires_in":   expiresInSeconds,
	}

	logger.Info("Returning access token response to client", "expires_in_seconds", expiresInSeconds)
	return c.JSON(http.StatusOK, resp)
}

// HandleBasicAuthCallback handles the basic authorization callback flow
func (s *OAuth2Server) HandleBasicAuthCallback(c echo.Context) error {
	code := c.QueryParam("code")
	redirect := c.QueryParam("redirect")
	logger := securityLogger.With("redirect", redirect)
	logger.Info("Handling basic authorization callback")

	if code == "" {
		logger.Warn("Missing authorization code in callback")
		return c.String(http.StatusBadRequest, "Missing authorization code")
	}

	// Create a context with timeout for the token exchange
	ctx, cancel := context.WithTimeout(c.Request().Context(), 15*time.Second) // Increased timeout slightly
	defer cancel()

	// Exchange the authorization code for an access token directly on the server
	// Do not log the code being exchanged
	logger.Info("Attempting to exchange authorization code for access token server-side")
	accessToken, err := s.ExchangeAuthCode(ctx, code) // Pass context
	if err != nil {
		// Check if the error is context deadline exceeded
		if errors.Is(err, context.DeadlineExceeded) {
			logger.Warn("Timeout exchanging authorization code server-side", "error", err)
			return c.String(http.StatusGatewayTimeout, "Login timed out. Please try again.")
		}
		logger.Warn("Failed to exchange authorization code server-side", "error", err)
		// Provide a user-friendly error message
		return c.String(http.StatusInternalServerError, "Unable to complete login at this time. Please try again.")
	}
	// DO NOT log the accessToken
	logger.Info("Successfully exchanged authorization code for access token")

	// Regenerate session to prevent session fixation
	// This clears existing auth state and forces a new session ID on save
	// Pass the underlying http.ResponseWriter and *http.Request to gothic.Logout
	if err := gothic.Logout(c.Response().Writer, c.Request()); err != nil {
		// Log the error but proceed cautiously. Depending on the store,
		// Logout might fail if cookies are invalid, but StoreInSession might still create a new one.
		logger.Warn("Error during gothic.Logout (session regeneration step)", "error", err)
	} else {
		logger.Info("Successfully logged out old session before storing new token (session fixation mitigation)")
	}

	// Store the access token in the new Gothic session
	// Do not log the token here either
	if err := gothic.StoreInSession("access_token", accessToken, c.Request(), c.Response()); err != nil {
		// This error is more critical now, as it means we couldn't establish the new session
		logger.Error("Failed to store access token in new session after logout/regeneration", "error", err)
		return c.String(http.StatusInternalServerError, "Session error during login. Please try again.")
	} else {
		logger.Info("Successfully stored access token in new session")
	}

	// Validate the redirect path
	safeRedirect := "/" // Default redirect
	if redirect != "" {
		// Use IsSafePath from the conf package to validate the redirect
		if conf.IsSafePath(redirect) {
			safeRedirect = redirect
			logger.Debug("Validated redirect path", "safe_redirect", safeRedirect)
		} else {
			logger.Warn("Invalid or unsafe redirect path provided, using default", "provided_redirect", redirect, "default_redirect", safeRedirect)
		}
	} else {
		logger.Debug("No redirect path provided, using default", "default_redirect", safeRedirect)
	}

	// Redirect the user to the final destination
	logger.Info("Redirecting user to final destination", "destination", safeRedirect)
	return c.Redirect(http.StatusFound, safeRedirect)
}
