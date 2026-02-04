// internal/api/v2/app.go
package api

import (
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/api/middleware"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// App config endpoint constants
const (
	// AppConfigEndpoint is the path for the app config endpoint
	AppConfigEndpoint = "/app/config"
)

// AppConfigResponse represents the application configuration returned to the frontend.
// This replaces the server-side injected window.BIRDNET_CONFIG.
type AppConfigResponse struct {
	CSRFToken string            `json:"csrfToken"`
	Security  SecurityConfigDTO `json:"security"`
	Version   string            `json:"version"`
	BasePath  string            `json:"basePath"` // reverse proxy prefix for frontend URL construction
}

// SecurityConfigDTO represents the security configuration for the frontend.
type SecurityConfigDTO struct {
	Enabled       bool          `json:"enabled"`
	AccessAllowed bool          `json:"accessAllowed"`
	AuthConfig    AuthConfigDTO `json:"authConfig"`
}

// AuthConfigDTO represents the authentication provider configuration.
type AuthConfigDTO struct {
	BasicEnabled     bool     `json:"basicEnabled"`
	EnabledProviders []string `json:"enabledProviders"`
}

// initAppRoutes registers application-level API endpoints
func (c *Controller) initAppRoutes() {
	// App config endpoint - publicly accessible (no auth required)
	// This endpoint provides the frontend with configuration data
	// that was previously injected server-side into the HTML template.
	c.Group.GET(AppConfigEndpoint, c.GetAppConfig)
}

// GetAppConfig handles GET /api/v2/app/config
// Returns the application configuration needed by the frontend SPA.
// This endpoint is public because:
// 1. It provides data needed before authentication can occur
// 2. The security.accessAllowed field tells the frontend if auth is needed
// 3. CSRF token is needed for any subsequent authenticated requests
func (c *Controller) GetAppConfig(ctx echo.Context) error {
	// Prevent caching of this response (contains user-specific CSRF token)
	ctx.Response().Header().Set("Cache-Control", "no-store, no-cache, must-revalidate, private")
	ctx.Response().Header().Set("Pragma", "no-cache")
	ctx.Response().Header().Set("Expires", "0")

	// Ensure CSRF token is available, generating one if the middleware didn't.
	// Echo v4.15.0's Sec-Fetch-Site optimization may skip token generation for
	// same-origin requests, but this endpoint must always provide a token.
	csrfToken, err := middleware.EnsureCSRFToken(ctx)
	if err != nil {
		c.logWarnIfEnabled("Failed to generate CSRF token", logger.Error(err))
		return c.HandleError(ctx, err, "Failed to generate CSRF token", http.StatusInternalServerError)
	}

	// Get enabled OAuth providers from the new array-based config
	// This returns provider IDs for all enabled providers with valid credentials
	enabledProviders := c.Settings.GetEnabledOAuthProviders()
	// Ensure we always return an array (not null) for JSON serialization
	if enabledProviders == nil {
		enabledProviders = []string{}
	}

	// Determine if any security method is enabled
	securityEnabled := c.Settings.Security.BasicAuth.Enabled || len(enabledProviders) > 0

	// Determine if access is currently allowed
	accessAllowed := c.determineAccessAllowed(ctx, securityEnabled)

	// Determine the effective base path for reverse proxy support.
	// Priority: X-Ingress-Path > X-Forwarded-Prefix > config BasePath > empty.
	basePath := requestBasePath(ctx, c.Settings)

	// Build response
	response := AppConfigResponse{
		CSRFToken: csrfToken,
		Security: SecurityConfigDTO{
			Enabled:       securityEnabled,
			AccessAllowed: accessAllowed,
			AuthConfig: AuthConfigDTO{
				BasicEnabled:     c.Settings.Security.BasicAuth.Enabled,
				EnabledProviders: enabledProviders,
			},
		},
		Version:  c.Settings.Version,
		BasePath: basePath,
	}

	c.logDebugIfEnabled("Serving app config",
		logger.Bool("security_enabled", securityEnabled),
		logger.Bool("access_allowed", accessAllowed),
		logger.String("ip", ctx.RealIP()),
	)

	return ctx.JSON(http.StatusOK, response)
}

// determineAccessAllowed checks if the current request has access.
// Returns true if:
// - Security is disabled (no auth required)
// - Auth service confirms the request is authenticated
func (c *Controller) determineAccessAllowed(ctx echo.Context, securityEnabled bool) bool {
	// If security is not enabled, allow access
	if !securityEnabled {
		return true
	}

	// If auth service is not configured, deny access (fail closed)
	if c.authService == nil {
		return false
	}

	// Use auth service to check authentication status
	// This checks: subnet bypass, token auth, and session auth
	return c.authService.IsAuthenticated(ctx)
}

// requestBasePath returns the effective base path prefix for the current request.
// Priority: X-Ingress-Path header > X-Forwarded-Prefix header > config BasePath > empty.
func requestBasePath(c echo.Context, settings *conf.Settings) string {
	if p := c.Request().Header.Get("X-Ingress-Path"); isSafePathPrefix(p) {
		return strings.TrimRight(p, "/")
	}
	if p := c.Request().Header.Get("X-Forwarded-Prefix"); isSafePathPrefix(p) {
		return strings.TrimRight(p, "/")
	}
	if settings != nil && settings.WebServer.BasePath != "" {
		return strings.TrimRight(settings.WebServer.BasePath, "/")
	}
	return ""
}

// isSafePathPrefix validates that a path prefix is safe for use in redirects.
// Rejects empty strings, protocol-relative URLs (//...), and absolute URLs (://...).
func isSafePathPrefix(p string) bool {
	return p != "" && strings.HasPrefix(p, "/") && !strings.HasPrefix(p, "//") && !strings.Contains(p, "://")
}
