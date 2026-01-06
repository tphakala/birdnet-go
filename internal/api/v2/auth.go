// internal/api/v2/auth.go
package api

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/tphakala/birdnet-go/internal/api/auth"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/security"
)

// Auth constants (file-local)
const (
	authExtendedTimeout = 15  // Extended timeout for slower operations
	maxBasePathLength   = 128 // Maximum length for base path validation
	authRateLimitRate   = 5   // Login attempts per rate limit window
	authRateLimitBurst  = 5   // Burst allowance for rate limiting
	authRateLimitWindow = 15  // Rate limit window in minutes
	authDelayMinMs      = 50  // Minimum random delay in ms for timing attack mitigation
	authDelayMaxMs      = 150 // Maximum random delay in ms for timing attack mitigation
)

// Compiled regex for path validation (moved outside function for performance)
var validBasePathRegex = regexp.MustCompile(`^/[a-zA-Z0-9/_-]*/$`)

// AuthRequest represents the login request structure
type AuthRequest struct {
	Username    string `json:"username"`
	Password    string `json:"password"`
	RedirectURL string `json:"redirectUrl,omitempty"` // Optional redirect URL after successful login
	BasePath    string `json:"basePath,omitempty"`    // Optional base path where UI is hosted (e.g., "/ui", "/app", or "/")
}

// AuthResponse represents the login response structure
type AuthResponse struct {
	Success     bool      `json:"success"`
	Message     string    `json:"message"`
	Username    string    `json:"username,omitempty"`
	Timestamp   time.Time `json:"timestamp"`
	RedirectURL string    `json:"redirectUrl,omitempty"` // For OAuth callback redirect
	// In a real token-based auth system, we would return tokens here
	// Token     string    `json:"token,omitempty"`
	// ExpiresAt time.Time `json:"expires_at,omitempty"`
}

// AuthStatus represents the current authentication status
type AuthStatus struct {
	Authenticated bool   `json:"authenticated"`
	Username      string `json:"username,omitempty"`
	Method        string `json:"auth_method,omitempty"`
}

// initAuthRoutes registers all authentication-related API endpoints
func (c *Controller) initAuthRoutes() {
	// Create auth API group
	authGroup := c.Group.Group("/auth")

	// Create rate limiter for login endpoint to prevent brute force attacks
	// Allow 5 login attempts per 15 minutes per IP address
	loginRateLimiter := middleware.RateLimiterWithConfig(middleware.RateLimiterConfig{
		Skipper: middleware.DefaultSkipper,
		Store: middleware.NewRateLimiterMemoryStoreWithConfig(
			middleware.RateLimiterMemoryStoreConfig{
				Rate:      authRateLimitRate,                 // 5 requests
				Burst:     authRateLimitBurst,                // Allow burst up to the rate
				ExpiresIn: authRateLimitWindow * time.Minute, // Per 15 minutes
			},
		),
		IdentifierExtractor: func(ctx echo.Context) (string, error) {
			// Use IP address as identifier for rate limiting
			return ctx.RealIP(), nil
		},
		ErrorHandler: func(ctx echo.Context, err error) error {
			// Return a user-friendly error message when rate limit is exceeded
			c.logWarnIfEnabled("Login rate limit exceeded",
				logger.String("ip", ctx.RealIP()),
				logger.String("path", ctx.Request().URL.Path),
				logger.String("user_agent", ctx.Request().Header.Get("User-Agent")),
			)
			return ctx.JSON(http.StatusTooManyRequests, map[string]string{
				"error": "Too many login attempts. Please try again in 15 minutes.",
			})
		},
		DenyHandler: func(ctx echo.Context, identifier string, err error) error {
			// This is called when the rate limit is exceeded
			c.logWarnIfEnabled("Login attempt denied due to rate limit",
				logger.String("identifier", identifier),
				logger.String("ip", ctx.RealIP()),
				logger.String("path", ctx.Request().URL.Path),
			)
			return ctx.JSON(http.StatusTooManyRequests, map[string]string{
				"error": "Too many login attempts. Please try again in 15 minutes.",
			})
		},
	})

	// Routes that don't require authentication (but are rate limited)
	authGroup.POST("/login", c.Login, loginRateLimiter)

	// OAuth callback endpoint - public, completes the OAuth flow
	// This is the V2 replacement for /api/v1/oauth2/callback
	authGroup.GET("/callback", c.OAuthCallback)

	// Routes that require authentication
	protectedGroup := authGroup.Group("", c.authMiddleware)
	protectedGroup.POST("/logout", c.Logout)
	protectedGroup.GET("/status", c.GetAuthStatus)
}

// Login handles POST /api/v2/auth/login
func (c *Controller) Login(ctx echo.Context) error {
	// Parse login request
	var req AuthRequest
	if err := ctx.Bind(&req); err != nil {
		c.logErrorIfEnabled("Invalid login request",
			logger.Error(err),
			logger.String("ip", ctx.RealIP()),
			logger.String("path", ctx.Request().URL.Path),
		)
		return c.HandleError(ctx, err, "Invalid login request", http.StatusBadRequest)
	}

	// Use the stored auth service instance
	authService := c.authService
	if authService == nil {
		// Handle case where auth might not be configured but login endpoint is hit
		c.logErrorIfEnabled("Login attempt but AuthService is nil (auth not configured?)",
			logger.String("ip", ctx.RealIP()),
			logger.String("path", ctx.Request().URL.Path),
		)
		// Return a generic error, perhaps indicating auth isn't enabled
		return c.HandleError(ctx, errors.New("authentication not configured"),
			"Authentication service unavailable", http.StatusInternalServerError)
	}

	// If authentication is not required, act as if the login was successful
	if !authService.IsAuthRequired(ctx) {
		c.logInfoIfEnabled("Authentication not required",
			logger.Username(req.Username),
			logger.String("ip", ctx.RealIP()),
			logger.String("path", ctx.Request().URL.Path),
		)
		return ctx.JSON(http.StatusOK, AuthResponse{
			Success:   true,
			Message:   "Authentication is not required on this server",
			Username:  req.Username,
			Timestamp: time.Now(),
		})
	}

	// Check for empty credentials before calling the auth service
	if req.Username == "" || req.Password == "" {
		// Add a short, randomized delay to mitigate timing attacks on username enumeration
		randomDelay(ctx.Request().Context(), authDelayMinMs, authDelayMaxMs)

		c.logWarnIfEnabled("Login attempt with missing credentials",
			logger.Bool("username_present", req.Username != ""),
			logger.Bool("password_present", req.Password != ""),
			logger.String("ip", ctx.RealIP()),
			logger.String("path", ctx.Request().URL.Path),
		)

		return ctx.JSON(http.StatusBadRequest, AuthResponse{
			Success:   false,
			Message:   "Username and password are required",
			Timestamp: time.Now(),
		})
	}

	// Authenticate using basic auth - now returns auth code directly
	authCode, authErr := authService.AuthenticateBasic(ctx, req.Username, req.Password)

	if authErr != nil {
		// Add a short, randomized delay to mitigate brute force/timing attacks
		randomDelay(ctx.Request().Context(), authDelayMinMs, authDelayMaxMs)

		c.logWarnIfEnabled("Failed login attempt",
			logger.Username(req.Username),
			logger.String("ip", ctx.RealIP()),
			logger.String("path", ctx.Request().URL.Path),
			logger.Error(authErr),
		)

		// Use the error message from the sentinel error if appropriate
		message := "Invalid credentials"
		if errors.Is(authErr, auth.ErrInvalidCredentials) {
			message = auth.ErrInvalidCredentials.Error()
		}

		return ctx.JSON(http.StatusUnauthorized, AuthResponse{
			Success:   false,
			Message:   message,
			Timestamp: time.Now(),
		})
	}

	// Successful login - auth code has been generated directly (V1 pattern)
	c.logInfoIfEnabled("Successful login with auth code",
		logger.Username(req.Username),
		logger.String("ip", ctx.RealIP()),
		logger.String("path", ctx.Request().URL.Path),
		logger.Bool("auth_code_generated", authCode != ""),
	)

	// Extract the base path dynamically
	basePath := c.extractBasePath(ctx, req)

	// Validate and sanitize the redirect URL from the request
	finalRedirect := basePath // Default to detected base path
	if req.RedirectURL != "" {
		// Use the security package's validation
		if security.IsValidRedirect(req.RedirectURL) {
			// Ensure the redirect stays within the detected base path
			finalRedirect = ensurePathWithinBase(req.RedirectURL, basePath)

			// Log if redirect was adjusted
			if finalRedirect != req.RedirectURL {
				c.logDebugIfEnabled("Adjusted redirect URL to stay within base path",
					logger.String("requested", req.RedirectURL),
					logger.String("basePath", basePath),
					logger.String("final", finalRedirect),
				)
			}
		} else {
			// Invalid redirect - log and use default
			c.logWarnIfEnabled("Invalid redirect URL provided, using base path",
				logger.String("requested", req.RedirectURL),
				logger.String("basePath", basePath),
				logger.String("default", finalRedirect),
			)
		}
	}

	// Construct the V2 OAuth callback URL with the validated redirect
	// URL-encode both code and redirect to prevent parameter injection and handle special characters
	redirectURL := fmt.Sprintf("/api/v2/auth/callback?code=%s&redirect=%s", url.QueryEscape(authCode), url.QueryEscape(finalRedirect))

	c.logInfoIfEnabled("Returning successful login response with redirect",
		logger.Username(req.Username),
		logger.String("redirect_url", redirectURL),
		logger.String("final_redirect", finalRedirect),
		logger.Bool("auth_code_generated", authCode != ""),
	)

	return ctx.JSON(http.StatusOK, AuthResponse{
		Success:     true,
		Message:     "Login successful - complete OAuth flow",
		Username:    req.Username,
		Timestamp:   time.Now(),
		RedirectURL: redirectURL,
	})
}

// Logout handles POST /api/v2/auth/logout
func (c *Controller) Logout(ctx echo.Context) error {
	// Use the stored auth service instance
	authService := c.authService
	if authService == nil {
		c.logWarnIfEnabled("Logout requested but AuthService is nil (auth not configured?)",
			logger.String("ip", ctx.RealIP()),
			logger.String("path", ctx.Request().URL.Path),
		)
		// Return success even if service isn't available, as logout intent is met.
		return ctx.JSON(http.StatusOK, AuthResponse{
			Success:   true,
			Message:   "Logged out (auth service unavailable)",
			Timestamp: time.Now(),
		})
	}

	// Try to perform logout via auth service
	if err := authService.Logout(ctx); err != nil {
		c.logErrorIfEnabled("Logout failed",
			logger.Error(err),
			logger.String("ip", ctx.RealIP()),
			logger.String("path", ctx.Request().URL.Path),
		)
		return c.HandleError(ctx, err, "Logout failed", http.StatusInternalServerError)
	}

	c.logInfoIfEnabled("User logged out",
		logger.String("ip", ctx.RealIP()),
		logger.String("path", ctx.Request().URL.Path),
	)

	return ctx.JSON(http.StatusOK, AuthResponse{
		Success:   true,
		Message:   "Logged out successfully",
		Timestamp: time.Now(),
	})
}

// GetAuthStatus handles GET /api/v2/auth/status
func (c *Controller) GetAuthStatus(ctx echo.Context) error {
	// Read authentication status details set by the AuthMiddleware in the context.
	// Use the auth.CtxKey* constants to ensure consistency with the middleware.
	isAuthenticated := boolFromCtx(ctx, auth.CtxKeyIsAuthenticated, false)
	username := stringFromCtx(ctx, auth.CtxKeyUsername, "")
	// Read the method as a string from context for now.
	// Downstream consumers comparing this value might need updates if they
	// relied on specific string literals. The middleware now sets the context
	// value using the string representation of the new AuthMethod constants.
	authMethod := stringFromCtx(ctx, auth.CtxKeyAuthMethod, auth.AuthMethodUnknown.String())

	// Construct the response based on context values
	status := AuthStatus{
		Authenticated: isAuthenticated,
		Username:      username,
		Method:        authMethod,
	}

	c.logInfoIfEnabled("Auth status check",
		logger.Bool("authenticated", status.Authenticated),
		logger.Username(status.Username),
		logger.String("method", status.Method),
		logger.String("ip", ctx.RealIP()),
		logger.String("path", ctx.Request().URL.Path),
		logger.String("user_agent", ctx.Request().Header.Get("User-Agent")),
	)

	return ctx.JSON(http.StatusOK, status)
}

// --- Context Helper Functions ---

// boolFromCtx safely retrieves a boolean value from the Echo context.
// Returns the defaultValue if the key is not found or the type assertion fails.
func boolFromCtx(ctx echo.Context, key string, defaultValue bool) bool {
	val := ctx.Get(key)
	if val == nil {
		return defaultValue
	}
	if boolVal, ok := val.(bool); ok {
		return boolVal
	}
	return defaultValue
}

// stringFromCtx safely retrieves a string value from the Echo context.
// Returns the defaultValue if the key is not found or the type assertion fails.
// It specifically handles values of type auth.AuthMethod by converting them to string.
func stringFromCtx(ctx echo.Context, key, defaultValue string) string {
	val := ctx.Get(key)
	if val == nil {
		return defaultValue
	}

	// Check if it's already a string
	if stringVal, ok := val.(string); ok {
		return stringVal
	}

	// Check if it's an auth.AuthMethod type
	if authMethodVal, ok := val.(auth.AuthMethod); ok {
		return authMethodVal.String()
	}

	// If neither string nor auth.AuthMethod, return default
	return defaultValue
}

// --- End Context Helper Functions ---

// extractBasePath attempts to determine the base path where the UI is hosted.
// It tries multiple sources in order of preference:
// 1. Explicit basePath from request
// 2. Referer header analysis
// 3. Default fallback
func (c *Controller) extractBasePath(ctx echo.Context, req AuthRequest) string {
	// 1. If explicitly provided and valid, use it
	if req.BasePath != "" && isValidBasePath(req.BasePath) {
		c.logDebugIfEnabled("Using explicit base path from request",
			logger.String("basePath", req.BasePath),
			logger.String("ip", ctx.RealIP()),
		)
		return req.BasePath
	}

	// 2. Try to extract from Referer header
	referer := ctx.Request().Header.Get("Referer")
	if referer != "" {
		if basePath := extractBasePathFromReferer(referer); basePath != "" {
			c.logDebugIfEnabled("Extracted base path from Referer",
				logger.String("basePath", basePath),
				logger.String("referer", referer),
				logger.String("ip", ctx.RealIP()),
			)
			return basePath
		}
	}

	// 3. Default fallback - try to detect common patterns
	// Check if request is coming from /ui/* path based on API versioning
	defaultBasePath := "/ui/"
	c.logDebugIfEnabled("Using default base path",
		logger.String("basePath", defaultBasePath),
		logger.String("ip", ctx.RealIP()),
	)
	return defaultBasePath
}

// isValidBasePath validates that a base path is safe to use
func isValidBasePath(basePath string) bool {
	// Must start with /
	if !strings.HasPrefix(basePath, "/") {
		return false
	}

	// Must not contain dangerous patterns
	dangerousPatterns := []string{
		"..",          // Directory traversal
		"//",          // Protocol-relative URL
		"\\",          // Backslash
		"<",           // HTML injection
		">",           // HTML injection
		"javascript:", // XSS
		"data:",       // Data URLs
		"\n",          // Newline injection
		"\r",          // Carriage return injection
		"\x00",        // Null byte
	}

	lowerPath := strings.ToLower(basePath)
	for _, pattern := range dangerousPatterns {
		if strings.Contains(lowerPath, pattern) {
			return false
		}
	}

	// Length check
	if len(basePath) > maxBasePathLength {
		return false
	}

	// Should end with / for consistency
	if !strings.HasSuffix(basePath, "/") {
		return false
	}

	// Basic path validation - alphanumeric, hyphens, underscores, and slashes
	// This regex ensures the path contains only safe characters
	return validBasePathRegex.MatchString(basePath)
}

// extractBasePathFromReferer attempts to extract the base path from a Referer URL
func extractBasePathFromReferer(referer string) string {
	parsedURL, err := url.Parse(referer)
	if err != nil {
		return ""
	}

	// Only process if it's from the same origin (no scheme/host means relative)
	// In production, you'd want to check against the actual server host
	path := parsedURL.Path

	// Try to identify common UI base paths
	// This list should be configurable in the future
	commonBasePaths := []string{
		"/ui/",
		"/app/",
		"/admin/",
		"/dashboard/",
		"/", // Root as last resort
	}

	for _, basePath := range commonBasePaths {
		if strings.HasPrefix(path, basePath) {
			// Validate before returning
			if isValidBasePath(basePath) {
				return basePath
			}
		}
	}

	return ""
}

// ensurePathWithinBase ensures a redirect path stays within the given base path
func ensurePathWithinBase(redirectPath, basePath string) string {
	// If redirect is already within base path, return as-is
	if strings.HasPrefix(redirectPath, basePath) {
		return redirectPath
	}

	// If redirect is root-relative, prepend base path
	if strings.HasPrefix(redirectPath, "/") && !strings.HasPrefix(redirectPath, "//") {
		// Remove leading slash to avoid double slashes
		trimmedRedirect := strings.TrimPrefix(redirectPath, "/")
		return basePath + trimmedRedirect
	}

	// Otherwise, just use the base path
	return basePath
}

// OAuthCallback handles GET /api/v2/auth/callback
// Completes the OAuth flow by exchanging auth code for access token and establishing session.
// This endpoint is the V2 equivalent of /api/v1/oauth2/callback.
func (c *Controller) OAuthCallback(ctx echo.Context) error {
	code := ctx.QueryParam("code")
	redirect := ctx.QueryParam("redirect")

	c.logInfoIfEnabled("Handling V2 OAuth callback",
		logger.String("redirect", redirect),
		logger.String("ip", ctx.RealIP()),
		logger.String("path", ctx.Request().URL.Path),
	)

	// 1. Validate code parameter
	if code == "" {
		c.logWarnIfEnabled("Missing authorization code in callback",
			logger.String("ip", ctx.RealIP()),
			logger.String("path", ctx.Request().URL.Path),
		)
		return ctx.String(http.StatusBadRequest, "Missing authorization code")
	}

	// 2. Defensive check: ensure AuthService is available
	if c.authService == nil {
		c.logErrorIfEnabled("AuthService is nil in OAuthCallback - server misconfiguration",
			logger.String("ip", ctx.RealIP()),
			logger.String("path", ctx.Request().URL.Path),
		)
		return ctx.String(http.StatusServiceUnavailable, "Authentication service unavailable. Please try again later.")
	}

	// 3. Exchange auth code for access token (with 15s timeout)
	exchangeCtx, cancel := context.WithTimeout(ctx.Request().Context(), authExtendedTimeout*time.Second)
	defer cancel()

	accessToken, err := c.authService.ExchangeAuthCode(exchangeCtx, code)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			c.logWarnIfEnabled("Timeout exchanging authorization code",
				logger.Error(err),
				logger.String("ip", ctx.RealIP()),
			)
			return ctx.String(http.StatusGatewayTimeout, "Login timed out. Please try again.")
		}
		c.logWarnIfEnabled("Failed to exchange authorization code",
			logger.Error(err),
			logger.String("ip", ctx.RealIP()),
		)
		return ctx.String(http.StatusUnauthorized, "Unable to complete login at this time. Please try again.")
	}

	c.logInfoIfEnabled("Successfully exchanged authorization code for access token",
		logger.String("ip", ctx.RealIP()),
	)

	// 4. Establish session (handles session fixation mitigation)
	if err := c.authService.EstablishSession(ctx, accessToken); err != nil {
		c.logErrorIfEnabled("Failed to establish session",
			logger.Error(err),
			logger.String("ip", ctx.RealIP()),
		)
		return ctx.String(http.StatusInternalServerError, "Session error during login. Please try again.")
	}

	// 5. Validate redirect path (prevent open redirects)
	safeRedirect := validateAndSanitizeRedirect(redirect)

	c.logInfoIfEnabled("Redirecting user to final destination",
		logger.String("destination", safeRedirect),
		logger.String("ip", ctx.RealIP()),
	)

	// 6. Redirect to final destination
	return ctx.Redirect(http.StatusFound, safeRedirect)
}

// containsCRLFCharacters checks if a string contains CR/LF injection characters.
func containsCRLFCharacters(s string) bool {
	lower := strings.ToLower(s)
	return strings.ContainsAny(s, "\r\n") ||
		strings.Contains(lower, "%0d") ||
		strings.Contains(lower, "%0a")
}

// isValidRelativePath checks if the parsed URL represents a safe relative path.
func isValidRelativePath(parsedURL *url.URL) bool {
	// Must have no scheme (not http://, https://, etc.)
	// Must have no host (not //evil.com)
	// Must start with a single '/' (valid relative path)
	// Must NOT start with '//' or '/\' (protocol-relative URLs)
	if parsedURL.Scheme != "" || parsedURL.Host != "" {
		return false
	}
	if !strings.HasPrefix(parsedURL.Path, "/") {
		return false
	}
	if len(parsedURL.Path) > 1 && (parsedURL.Path[1] == '/' || parsedURL.Path[1] == '\\') {
		return false
	}
	return true
}

// validateAndSanitizeRedirect validates and sanitizes a redirect path to prevent open redirects.
// Returns "/" if the path is invalid, otherwise returns the sanitized path.
func validateAndSanitizeRedirect(redirect string) string {
	if redirect == "" {
		return "/"
	}

	// Replace ALL backslashes with forward slashes for robust normalization
	cleanedRedirect := strings.ReplaceAll(redirect, "\\", "/")
	parsedURL, err := url.Parse(cleanedRedirect)
	if err != nil || !isValidRelativePath(parsedURL) {
		return "/"
	}

	// Check for CR/LF injection in path and query
	if containsCRLFCharacters(parsedURL.Path) || containsCRLFCharacters(parsedURL.RawQuery) {
		return "/"
	}

	// Passed all checks, construct safe redirect preserving path and query
	if parsedURL.RawQuery != "" {
		return parsedURL.Path + "?" + parsedURL.RawQuery
	}
	return parsedURL.Path
}

// randomDelay introduces a random sleep duration within the specified range [minMs, maxMs).
// It accepts a context to allow cancellation of the delay.
func randomDelay(ctx context.Context, minMs, maxMs int64) {
	if maxMs <= minMs {
		minMs = 50  // Default min
		maxMs = 150 // Default max
	}
	rangeSize := big.NewInt(maxMs - minMs)
	n, err := rand.Int(rand.Reader, rangeSize)
	var delayMs int64
	if err != nil {
		// Fallback to min delay if random generation fails
		delayMs = minMs
		// Optionally log the error
		// log.Printf("Error generating random delay: %v, using fallback %dms", err, delayMs)
	} else {
		delayMs = n.Int64() + minMs
	}

	delayDuration := time.Duration(delayMs) * time.Millisecond

	// Create a timer for the delay
	timer := time.NewTimer(delayDuration)
	defer timer.Stop() // Ensure timer resources are released

	// Wait for the timer or context cancellation
	select {
	case <-timer.C:
		// Delay completed normally
	case <-ctx.Done():
		// No need to call timer.Stop() here, defer handles it.
		// Context was cancelled, delay aborted
		// Optionally log context cancellation
		// log.Printf("Random delay cancelled by context: %v", ctx.Err())
	}
}
