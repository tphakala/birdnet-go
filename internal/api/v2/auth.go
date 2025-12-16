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
	auth "github.com/tphakala/birdnet-go/internal/api/v2/auth"
	"github.com/tphakala/birdnet-go/internal/security"
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
				Rate:      5,                // 5 requests
				Burst:     5,                // Allow burst up to the rate
				ExpiresIn: 15 * time.Minute, // Per 15 minutes
			},
		),
		IdentifierExtractor: func(ctx echo.Context) (string, error) {
			// Use IP address as identifier for rate limiting
			return ctx.RealIP(), nil
		},
		ErrorHandler: func(ctx echo.Context, err error) error {
			// Return a user-friendly error message when rate limit is exceeded
			if c.apiLogger != nil {
				c.apiLogger.Warn("Login rate limit exceeded",
					"ip", ctx.RealIP(),
					"path", ctx.Request().URL.Path,
					"user_agent", ctx.Request().Header.Get("User-Agent"),
				)
			}
			return ctx.JSON(http.StatusTooManyRequests, map[string]string{
				"error": "Too many login attempts. Please try again in 15 minutes.",
			})
		},
		DenyHandler: func(ctx echo.Context, identifier string, err error) error {
			// This is called when the rate limit is exceeded
			if c.apiLogger != nil {
				c.apiLogger.Warn("Login attempt denied due to rate limit",
					"identifier", identifier,
					"ip", ctx.RealIP(),
					"path", ctx.Request().URL.Path,
				)
			}
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
	protectedGroup := authGroup.Group("", c.AuthMiddleware)
	protectedGroup.POST("/logout", c.Logout)
	protectedGroup.GET("/status", c.GetAuthStatus)
}

// Login handles POST /api/v2/auth/login
func (c *Controller) Login(ctx echo.Context) error {
	// Parse login request
	var req AuthRequest
	if err := ctx.Bind(&req); err != nil {
		if c.apiLogger != nil {
			c.apiLogger.Error("Invalid login request",
				"error", err.Error(),
				"ip", ctx.RealIP(),
				"path", ctx.Request().URL.Path,
			)
		}
		return c.HandleError(ctx, err, "Invalid login request", http.StatusBadRequest)
	}

	// Use the stored auth service instance
	authService := c.AuthService
	if authService == nil {
		// Handle case where auth might not be configured but login endpoint is hit
		if c.apiLogger != nil {
			c.apiLogger.Error("Login attempt but AuthService is nil (auth not configured?)",
				"ip", ctx.RealIP(),
				"path", ctx.Request().URL.Path,
			)
		}
		// Return a generic error, perhaps indicating auth isn't enabled
		return c.HandleError(ctx, errors.New("authentication not configured"),
			"Authentication service unavailable", http.StatusInternalServerError)
	}

	// If authentication is not required, act as if the login was successful
	if !authService.IsAuthRequired(ctx) {
		if c.apiLogger != nil {
			c.apiLogger.Info("Authentication not required",
				"username", req.Username,
				"ip", ctx.RealIP(),
				"path", ctx.Request().URL.Path,
			)
		}
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
		randomDelay(ctx.Request().Context(), 50, 150)

		if c.apiLogger != nil {
			c.apiLogger.Warn("Login attempt with missing credentials",
				"username_present", req.Username != "",
				"password_present", req.Password != "",
				"ip", ctx.RealIP(),
				"path", ctx.Request().URL.Path,
			)
		}

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
		randomDelay(ctx.Request().Context(), 50, 150)

		if c.apiLogger != nil {
			c.apiLogger.Warn("Failed login attempt",
				"username", req.Username,
				"ip", ctx.RealIP(),
				"path", ctx.Request().URL.Path,
				"error", authErr.Error(), // Use the error from the service
			)
		}

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
	if c.apiLogger != nil {
		c.apiLogger.Info("Successful login with auth code",
			"username", req.Username,
			"ip", ctx.RealIP(),
			"path", ctx.Request().URL.Path,
			"auth_code_length", len(authCode),
		)
	}

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
				if c.apiLogger != nil {
					c.apiLogger.Debug("Adjusted redirect URL to stay within base path",
						"requested", req.RedirectURL,
						"basePath", basePath,
						"final", finalRedirect,
					)
				}
			}
		} else if c.apiLogger != nil {
			// Invalid redirect - log and use default
			c.apiLogger.Warn("Invalid redirect URL provided, using base path",
				"requested", req.RedirectURL,
				"basePath", basePath,
				"default", finalRedirect,
			)
		}
	}
	
	// Construct the V2 OAuth callback URL with the validated redirect
	redirectURL := fmt.Sprintf("/api/v2/auth/callback?code=%s&redirect=%s", authCode, finalRedirect)

	if c.apiLogger != nil {
		c.apiLogger.Info("Returning successful login response with redirect",
			"username", req.Username,
			"redirect_url", redirectURL,
			"final_redirect", finalRedirect,
			"auth_code_length", len(authCode),
		)
	}

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
	authService := c.AuthService
	if authService == nil {
		if c.apiLogger != nil {
			c.apiLogger.Warn("Logout requested but AuthService is nil (auth not configured?)",
				"ip", ctx.RealIP(),
				"path", ctx.Request().URL.Path,
			)
		}
		// Return success even if service isn't available, as logout intent is met.
		return ctx.JSON(http.StatusOK, AuthResponse{
			Success:   true,
			Message:   "Logged out (auth service unavailable)",
			Timestamp: time.Now(),
		})
	}

	// Try to perform logout via auth service
	if err := authService.Logout(ctx); err != nil {
		if c.apiLogger != nil {
			c.apiLogger.Error("Logout failed",
				"error", err.Error(),
				"ip", ctx.RealIP(),
				"path", ctx.Request().URL.Path,
			)
		}
		return c.HandleError(ctx, err, "Logout failed", http.StatusInternalServerError)
	}

	if c.apiLogger != nil {
		c.apiLogger.Info("User logged out",
			"ip", ctx.RealIP(),
			"path", ctx.Request().URL.Path,
		)
	}

	return ctx.JSON(http.StatusOK, AuthResponse{
		Success:   true,
		Message:   "Logged out successfully",
		Timestamp: time.Now(),
	})
}

// GetAuthStatus handles GET /api/v2/auth/status
func (c *Controller) GetAuthStatus(ctx echo.Context) error {
	// Read authentication status details set by the AuthMiddleware in the context.
	isAuthenticated := boolFromCtx(ctx, "isAuthenticated", false)
	username := stringFromCtx(ctx, "username", "")
	// Read the method as a string from context for now.
	// Downstream consumers comparing this value might need updates if they
	// relied on specific string literals. The middleware now sets the context
	// value using the string representation of the new AuthMethod constants.
	authMethod := stringFromCtx(ctx, "authMethod", auth.AuthMethodUnknown.String())

	// Construct the response based on context values
	status := AuthStatus{
		Authenticated: isAuthenticated,
		Username:      username,
		Method:        authMethod,
	}

	if c.apiLogger != nil {
		c.apiLogger.Info("Auth status check",
			"authenticated", status.Authenticated,
			"username", status.Username,
			"method", status.Method,
			"ip", ctx.RealIP(),
			"path", ctx.Request().URL.Path,
			"user_agent", ctx.Request().Header.Get("User-Agent"),
		)
	}

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
		if c.apiLogger != nil {
			c.apiLogger.Debug("Using explicit base path from request",
				"basePath", req.BasePath,
				"ip", ctx.RealIP(),
			)
		}
		return req.BasePath
	}

	// 2. Try to extract from Referer header
	referer := ctx.Request().Header.Get("Referer")
	if referer != "" {
		if basePath := extractBasePathFromReferer(referer); basePath != "" {
			if c.apiLogger != nil {
				c.apiLogger.Debug("Extracted base path from Referer",
					"basePath", basePath,
					"referer", referer,
					"ip", ctx.RealIP(),
				)
			}
			return basePath
		}
	}

	// 3. Default fallback - try to detect common patterns
	// Check if request is coming from /ui/* path based on API versioning
	defaultBasePath := "/ui/"
	if c.apiLogger != nil {
		c.apiLogger.Debug("Using default base path",
			"basePath", defaultBasePath,
			"ip", ctx.RealIP(),
		)
	}
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
		"..",           // Directory traversal
		"//",           // Protocol-relative URL
		"\\",           // Backslash
		"<",            // HTML injection
		">",            // HTML injection
		"javascript:",  // XSS
		"data:",        // Data URLs
		"\n",           // Newline injection
		"\r",           // Carriage return injection
		"\x00",         // Null byte
	}

	lowerPath := strings.ToLower(basePath)
	for _, pattern := range dangerousPatterns {
		if strings.Contains(lowerPath, pattern) {
			return false
		}
	}

	// Length check
	if len(basePath) > 128 {
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

	if c.apiLogger != nil {
		c.apiLogger.Info("Handling V2 OAuth callback",
			"redirect", redirect,
			"ip", ctx.RealIP(),
			"path", ctx.Request().URL.Path,
		)
	}

	// 1. Validate code parameter
	if code == "" {
		if c.apiLogger != nil {
			c.apiLogger.Warn("Missing authorization code in callback",
				"ip", ctx.RealIP(),
				"path", ctx.Request().URL.Path,
			)
		}
		return ctx.String(http.StatusBadRequest, "Missing authorization code")
	}

	// 2. Exchange auth code for access token (with 15s timeout)
	exchangeCtx, cancel := context.WithTimeout(ctx.Request().Context(), 15*time.Second)
	defer cancel()

	accessToken, err := c.AuthService.ExchangeAuthCode(exchangeCtx, code)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			if c.apiLogger != nil {
				c.apiLogger.Warn("Timeout exchanging authorization code",
					"error", err.Error(),
					"ip", ctx.RealIP(),
				)
			}
			return ctx.String(http.StatusGatewayTimeout, "Login timed out. Please try again.")
		}
		if c.apiLogger != nil {
			c.apiLogger.Warn("Failed to exchange authorization code",
				"error", err.Error(),
				"ip", ctx.RealIP(),
			)
		}
		return ctx.String(http.StatusUnauthorized, "Unable to complete login at this time. Please try again.")
	}

	if c.apiLogger != nil {
		c.apiLogger.Info("Successfully exchanged authorization code for access token",
			"ip", ctx.RealIP(),
		)
	}

	// 3. Establish session (handles session fixation mitigation)
	if err := c.AuthService.EstablishSession(ctx, accessToken); err != nil {
		if c.apiLogger != nil {
			c.apiLogger.Error("Failed to establish session",
				"error", err.Error(),
				"ip", ctx.RealIP(),
			)
		}
		return ctx.String(http.StatusInternalServerError, "Session error during login. Please try again.")
	}

	// 4. Validate redirect path (prevent open redirects)
	safeRedirect := validateAndSanitizeRedirect(redirect)

	if c.apiLogger != nil {
		c.apiLogger.Info("Redirecting user to final destination",
			"destination", safeRedirect,
			"ip", ctx.RealIP(),
		)
	}

	// 5. Redirect to final destination
	return ctx.Redirect(http.StatusFound, safeRedirect)
}

// validateAndSanitizeRedirect validates and sanitizes a redirect path to prevent open redirects.
// Returns "/" if the path is invalid, otherwise returns the sanitized path.
func validateAndSanitizeRedirect(redirect string) string {
	safeRedirect := "/" // Default redirect

	if redirect == "" {
		return safeRedirect
	}

	// Replace ALL backslashes with forward slashes for robust normalization
	cleanedRedirect := strings.ReplaceAll(redirect, "\\", "/")
	parsedURL, err := url.Parse(cleanedRedirect)

	// Security validation for redirect paths to prevent open redirect vulnerabilities
	// We ONLY accept relative paths that:
	// 1. Parse without error
	// 2. Have no scheme (not http://, https://, etc.)
	// 3. Have no host (not //evil.com)
	// 4. Start with a single '/' (valid relative path)
	// 5. Do NOT start with '//' or '/\' (which browsers interpret as protocol-relative URLs)
	if err == nil && parsedURL.Scheme == "" && parsedURL.Host == "" &&
		strings.HasPrefix(parsedURL.Path, "/") &&
		(len(parsedURL.Path) <= 1 || (parsedURL.Path[1] != '/' && parsedURL.Path[1] != '\\')) {

		// Additional check for CR/LF injection characters in path and query
		pathContainsCRLF := strings.ContainsAny(parsedURL.Path, "\r\n") ||
			strings.Contains(strings.ToLower(parsedURL.Path), "%0d") ||
			strings.Contains(strings.ToLower(parsedURL.Path), "%0a")
		queryContainsCRLF := strings.ContainsAny(parsedURL.RawQuery, "\r\n") ||
			strings.Contains(strings.ToLower(parsedURL.RawQuery), "%0d") ||
			strings.Contains(strings.ToLower(parsedURL.RawQuery), "%0a")

		if !pathContainsCRLF && !queryContainsCRLF {
			// Passed all checks, construct safe redirect preserving path and query
			safeRedirect = parsedURL.Path
			if parsedURL.RawQuery != "" {
				safeRedirect += "?" + parsedURL.RawQuery
			}
		}
	}

	return safeRedirect
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
