package security

import (
	"context"
	"crypto/subtle"
	"fmt"
	"net"
	"net/http"

	"github.com/tphakala/birdnet-go/internal/errors"

	"github.com/gorilla/sessions"
	"github.com/labstack/echo/v4"
	"github.com/markbates/goth/gothic"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// IsInLocalSubnet checks if the given IP is in the same subnet as any local network interface
func IsInLocalSubnet(clientIP net.IP) bool {
	secLog := GetLogger().With(logger.String("ip", clientIP.String()))
	if clientIP == nil {
		secLog.Debug("IsInLocalSubnet check failed: client IP is nil")
		return false
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

	// Get the client's /24 subnet
	clientSubnet := getIPv4Subnet(clientIP)
	if clientSubnet == nil {
		secLog.Debug("Failed to get IPv4 /24 subnet for client IP")
		return false
	}
	secLog = secLog.With(logger.String("client_subnet", clientSubnet.String()))

	// Check each network interface
	for _, addr := range addrs {
		ipnet, ok := addr.(*net.IPNet)
		if !ok || ipnet.IP.IsLoopback() {
			continue
		}

		serverSubnet := getIPv4Subnet(ipnet.IP)
		if serverSubnet != nil {
			secLog.Debug("Checking against server interface", logger.String("server_ip", ipnet.IP.String()), logger.String("server_subnet", serverSubnet.String()))
			if clientSubnet.Equal(serverSubnet) {
				secLog.Debug("Client IP is in local subnet")
				return true
			}
		}
	}
	secLog.Debug("Client IP is not in any local subnet")
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
	return ipv4.Mask(net.CIDRMask(IPv4SubnetMaskBits, IPv4TotalAddressBits))
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

// configureLocalNetworkCookieStore configures the cookie store for local network access
func (s *OAuth2Server) configureLocalNetworkCookieStore() {
	GetLogger().Info("Configuring cookie store for local network access (allowing non-HTTPS cookies)")
	// Configure session options based on store type
	switch store := gothic.Store.(type) {
	case *sessions.CookieStore:
		// For CookieStore, use default session duration (session cookie if 0)
		store.Options = buildSessionOptions(false, 0)
	case *sessions.FilesystemStore:
		// Calculate MaxAge in seconds from the configured session duration
		// If not configured, default to 7 days
		maxAge := DefaultSessionMaxAgeSeconds
		if s.Settings.Security.SessionDuration > 0 {
			maxAge = int(s.Settings.Security.SessionDuration.Seconds())
		}
		store.Options = buildSessionOptions(false, maxAge)
	default:
		// Log a warning for unknown store types - operators should configure a supported store
		GetLogger().Warn("Unknown session store type, session options not configured", logger.String("store_type", fmt.Sprintf("%T", store)))
	}
}

// HandleBasicAuthorize handles the basic authorization flow
func (s *OAuth2Server) HandleBasicAuthorize(c echo.Context) error {
	clientID := c.QueryParam("client_id")
	redirectURI := c.QueryParam("redirect_uri")
	secLog := GetLogger().With(logger.String("client_id", clientID), logger.String("redirect_uri", redirectURI))
	secLog.Info("Handling basic authorization request")

	if clientID != s.Settings.Security.BasicAuth.ClientID {
		secLog.Warn("Invalid client_id provided", logger.String("expected", s.Settings.Security.BasicAuth.ClientID))
		return c.String(http.StatusBadRequest, "Invalid client_id")
	}

	// Validate redirect URI using the shared function and pre-parsed expected URI
	if err := ValidateRedirectURI(redirectURI, s.ExpectedBasicRedirectURI); err != nil {
		secLog.Warn("Redirect URI validation failed", logger.Error(err))
		// Return the specific error message for better client-side debugging
		return c.String(http.StatusBadRequest, err.Error())
	}

	// Generate an auth code
	secLog.Debug("Generating authorization code")
	authCode, err := s.GenerateAuthCode()
	if err != nil {
		secLog.Error("Failed to generate authorization code", logger.Error(err))
		return c.String(http.StatusInternalServerError, "Error generating auth code")
	}

	// DO NOT log the authCode itself
	secLog.Info("Authorization code generated successfully, redirecting user")
	return c.Redirect(http.StatusFound, redirectURI+"?code="+authCode)
}

// HandleBasicAuthToken handles the basic authorization token flow
func (s *OAuth2Server) HandleBasicAuthToken(c echo.Context) error {
	// Verify client credentials from Authorization header
	// Log the attempt, but DO NOT log the clientSecret
	clientID, clientSecret, ok := c.Request().BasicAuth()
	secLog := GetLogger().With(logger.String("client_id", clientID))
	secLog.Info("Handling basic authorization token request")

	if !ok {
		secLog.Warn("Basic auth header missing or malformed")
		return c.JSON(http.StatusUnauthorized, map[string]string{"error": "Missing or malformed Authorization header"})
	}

	// Use constant-time comparison to prevent timing attacks on credentials.
	// Both comparisons are performed and combined with bitwise AND to prevent
	// short-circuit evaluation from leaking timing information about valid IDs.
	clientIDMatch := subtle.ConstantTimeCompare([]byte(clientID), []byte(s.Settings.Security.BasicAuth.ClientID))
	clientSecretMatch := subtle.ConstantTimeCompare([]byte(clientSecret), []byte(s.Settings.Security.BasicAuth.ClientSecret))
	if (clientIDMatch & clientSecretMatch) != 1 {
		secLog.Warn("Invalid client credentials provided")
		return c.JSON(http.StatusUnauthorized, map[string]string{"error": "Invalid client id or secret"})
	}

	// Check if client is in local subnet and configure cookie store accordingly
	if clientIP := net.ParseIP(c.RealIP()); IsInLocalSubnet(clientIP) {
		// For clients in the local subnet, allow non-HTTPS cookies
		secLog.Info("Client is in local subnet, configuring cookie store for non-HTTPS")
		s.configureLocalNetworkCookieStore()
	}

	grantType := c.FormValue("grant_type")
	code := c.FormValue("code") // Do not log the code
	redirectURI := c.FormValue("redirect_uri")

	secLog.Info("Received token request parameters", logger.String("grant_type", grantType), logger.String("redirect_uri", redirectURI))

	// Check for required fields
	if grantType == "" || code == "" || redirectURI == "" {
		secLog.Warn("Missing required fields in token request")
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "Missing required fields"})
	}

	// Verify grant type
	if grantType != "authorization_code" {
		secLog.Warn("Unsupported grant type provided", logger.String("grant_type", grantType))
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "Unsupported grant type"})
	}

	// Validate redirect URI using the shared function and pre-parsed expected URI
	if err := ValidateRedirectURI(redirectURI, s.ExpectedBasicRedirectURI); err != nil {
		secLog.Warn("Redirect URI validation failed", logger.String("provided_uri", redirectURI), logger.Error(err))
		// Return a generic error to the client, log the specific one internally
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid redirect_uri"})
	}

	// Exchange the authorization code for an access token with timeout
	// Do not log the code being exchanged
	secLog.Info("Attempting to exchange authorization code for access token")
	// Pass the request context to ExchangeAuthCode
	tokenCtx, tokenCancel := context.WithTimeout(c.Request().Context(), TokenExchangeTimeout)
	defer tokenCancel()
	accessToken, err := s.ExchangeAuthCode(tokenCtx, code)
	if err != nil {
		// Check for context deadline exceeded specifically
		if errors.Is(err, context.DeadlineExceeded) {
			secLog.Warn("Timeout exchanging authorization code", logger.Error(err))
			return c.JSON(http.StatusGatewayTimeout, map[string]string{"error": "Timeout during token exchange"})
		}
		secLog.Warn("Failed to exchange authorization code", logger.Error(err))
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid authorization code"})
	}
	// DO NOT log the accessToken
	secLog.Info("Successfully exchanged authorization code for access token")

	// Store the access token in Gothic session
	// Do not log the token here either
	if err := gothic.StoreInSession("access_token", accessToken, c.Request(), c.Response()); err != nil {
		secLog.Warn("Failed to store access token in session", logger.Error(err))
		// Continue anyway since we'll return the token to the client
	}

	// Ensure content type is set explicitly
	c.Response().Header().Set("Content-Type", "application/json")

	// Return the access token in the response body
	expiresInSeconds := int(s.Settings.Security.BasicAuth.AccessTokenExp.Seconds())
	resp := map[string]any{ // Use interface{} for mixed types
		"access_token": accessToken, // This is sent to the client, unavoidable
		"token_type":   "Bearer",
		"expires_in":   expiresInSeconds,
	}

	secLog.Info("Returning access token response to client", logger.Int("expires_in_seconds", expiresInSeconds))
	return c.JSON(http.StatusOK, resp)
}

// HandleBasicAuthCallback handles the basic authorization callback flow
func (s *OAuth2Server) HandleBasicAuthCallback(c echo.Context) error {
	code := c.QueryParam("code")
	redirect := c.QueryParam("redirect")
	secLog := GetLogger().With(logger.String("redirect", redirect))
	secLog.Info("Handling basic authorization callback")

	if code == "" {
		secLog.Warn("Missing authorization code in callback")
		return c.String(http.StatusBadRequest, "Missing authorization code")
	}

	// Exchange the authorization code for an access token
	accessToken, err := s.exchangeCodeWithTimeout(c.Request().Context(), code)
	if err != nil {
		return s.handleTokenExchangeError(c, err, secLog)
	}
	secLog.Info("Successfully exchanged authorization code for access token")

	// Regenerate session and store token
	if err := s.regenerateAndStoreToken(c, accessToken, secLog); err != nil {
		return err
	}

	// Validate and sanitize the redirect path
	safeRedirect := ValidateAuthCallbackRedirect(redirect)
	if safeRedirect != redirect && redirect != "" {
		secLog.Debug("Redirect path sanitized", logger.String("original", redirect), logger.String("sanitized", safeRedirect))
	}

	// Redirect the user to the final destination
	secLog.Info("Redirecting user to final destination", logger.String("destination", safeRedirect))
	return c.Redirect(http.StatusFound, safeRedirect)
}

// exchangeCodeWithTimeout exchanges an auth code for an access token with a timeout
func (s *OAuth2Server) exchangeCodeWithTimeout(parentCtx context.Context, code string) (string, error) {
	ctx, cancel := context.WithTimeout(parentCtx, TokenExchangeTimeout)
	defer cancel()
	return s.ExchangeAuthCode(ctx, code)
}

// handleTokenExchangeError handles errors from token exchange and returns appropriate HTTP response
func (s *OAuth2Server) handleTokenExchangeError(c echo.Context, err error, log SecurityLogger) error {
	if errors.Is(err, context.DeadlineExceeded) {
		log.Warn("Timeout exchanging authorization code server-side", logger.Error(err))
		return c.String(http.StatusGatewayTimeout, "Login timed out. Please try again.")
	}
	log.Warn("Failed to exchange authorization code server-side", logger.Error(err))
	return c.String(http.StatusInternalServerError, "Unable to complete login at this time. Please try again.")
}

// regenerateAndStoreToken regenerates the session and stores the access token
func (s *OAuth2Server) regenerateAndStoreToken(c echo.Context, accessToken string, log SecurityLogger) error {
	// Regenerate session to prevent session fixation
	if err := gothic.Logout(c.Response().Writer, c.Request()); err != nil {
		log.Warn("Error during gothic.Logout (session regeneration step)", logger.Error(err))
	} else {
		log.Info("Successfully logged out old session before storing new token (session fixation mitigation)")
	}

	// Store the access token in the new Gothic session
	if err := gothic.StoreInSession("access_token", accessToken, c.Request(), c.Response()); err != nil {
		log.Error("Failed to store access token in new session after logout/regeneration", logger.Error(err))
		return c.String(http.StatusInternalServerError, "Session error during login. Please try again.")
	}
	log.Info("Successfully stored access token in new session")
	return nil
}
