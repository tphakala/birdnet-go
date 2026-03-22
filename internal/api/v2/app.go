// internal/api/v2/app.go
package api

import (
	"context"
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/api/middleware"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/repository"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/telemetry"
)

// App config endpoint constants
const (
	// AppConfigEndpoint is the path for the app config endpoint
	AppConfigEndpoint = "/app/config"
	// WizardDismissEndpoint is the path for the wizard dismiss endpoint
	WizardDismissEndpoint = "/app/wizard/dismiss"
)

// appMetadataKeyLastSeenVersion is the app_metadata key that tracks the last application
// version acknowledged by the user through the wizard dismiss action.
const appMetadataKeyLastSeenVersion = "last_seen_version"

// AppConfigResponse represents the application configuration returned to the frontend.
// This replaces the server-side injected window.BIRDNET_CONFIG.
type AppConfigResponse struct {
	CSRFToken       string                `json:"csrfToken"`
	Security        SecurityConfigDTO     `json:"security"`
	Version         string                `json:"version"`
	BasePath        string                `json:"basePath"`                  // reverse proxy prefix for frontend URL construction
	ColorScheme     string                `json:"colorScheme,omitempty"`     // admin-configured color scheme for all visitors
	CustomColors    *conf.CustomColors    `json:"customColors,omitempty"`    // custom scheme hex colors (when colorScheme is "custom")
	LogoStyle       string                `json:"logoStyle,omitempty"`       // admin-configured logo style: "gradient" or "solid"
	LiveSpectrogram bool                  `json:"liveSpectrogram"`           // auto-start live spectrogram on dashboard
	Layout          *conf.DashboardLayout `json:"layout,omitempty"`          // dashboard element layout for guest/pre-auth rendering
	FreshInstall    bool                  `json:"freshInstall"`              // true when this is a brand-new installation
	NewVersion      bool                  `json:"newVersion"`                // true when the app was upgraded since last dismiss
	PreviousVersion string                `json:"previousVersion,omitempty"` // last version the user acknowledged
	Sentry          *SentryFrontendConfig `json:"sentry,omitempty"`          // frontend telemetry config (only when enabled)
}

// SentryFrontendConfig exposes telemetry configuration to the frontend.
// Only included in AppConfigResponse when telemetry is enabled.
type SentryFrontendConfig struct {
	Enabled  bool   `json:"enabled"`
	DSN      string `json:"dsn"`
	SystemID string `json:"systemId"`
}

// SecurityConfigDTO represents the security configuration for the frontend.
type SecurityConfigDTO struct {
	Enabled       bool            `json:"enabled"`
	AccessAllowed bool            `json:"accessAllowed"`
	AuthConfig    AuthConfigDTO   `json:"authConfig"`
	PublicAccess  PublicAccessDTO `json:"publicAccess"`
}

// PublicAccessDTO exposes which features are accessible without authentication.
type PublicAccessDTO struct {
	LiveAudio bool `json:"liveAudio"`
}

// AuthConfigDTO represents the authentication provider configuration.
type AuthConfigDTO struct {
	BasicEnabled     bool     `json:"basicEnabled"`
	EnabledProviders []string `json:"enabledProviders"`
}

// initAppRoutes registers application-level API endpoints
func (c *Controller) initAppRoutes() {
	// Initialize app metadata repository from V2Manager if available
	if c.V2Manager != nil {
		var useV2Prefix bool
		if tp, ok := c.V2Manager.(interface{ TablePrefix() string }); ok {
			useV2Prefix = tp.TablePrefix() != ""
		}
		c.appMetadataRepo = repository.NewAppMetadataRepository(
			c.V2Manager.DB(),
			useV2Prefix,
			c.V2Manager.IsMySQL(),
		)
	}

	// App config endpoint - publicly accessible (no auth required)
	// This endpoint provides the frontend with configuration data
	// that was previously injected server-side into the HTML template.
	c.Group.GET(AppConfigEndpoint, c.GetAppConfig)

	// Wizard dismiss endpoint - conditionally protected.
	// When security is enabled, require authentication; otherwise allow public access.
	if c.authMiddleware != nil {
		c.Group.POST(WizardDismissEndpoint, c.DismissWizard, c.authMiddleware)
	} else {
		c.Group.POST(WizardDismissEndpoint, c.DismissWizard)
	}
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

	// Determine wizard state (freshInstall, newVersion, previousVersion)
	freshInstall, newVersion, previousVersion := c.determineWizardState(ctx.Request().Context())

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
			PublicAccess: PublicAccessDTO{
				LiveAudio: c.Settings.Security.PublicAccess.LiveAudio,
			},
		},
		Version:         c.Settings.Version,
		BasePath:        basePath,
		ColorScheme:     c.Settings.Realtime.Dashboard.ColorScheme,
		CustomColors:    c.Settings.Realtime.Dashboard.CustomColors,
		LogoStyle:       c.Settings.Realtime.Dashboard.LogoStyle,
		LiveSpectrogram: c.Settings.Realtime.Dashboard.LiveSpectrogram,
		FreshInstall:    freshInstall,
		NewVersion:      newVersion,
		PreviousVersion: previousVersion,
	}

	// Include dashboard layout for guest/pre-auth rendering if configured
	if len(c.Settings.Realtime.Dashboard.Layout.Elements) > 0 {
		response.Layout = &c.Settings.Realtime.Dashboard.Layout
	}

	// Include Sentry frontend config when telemetry is enabled
	if c.Settings.Sentry.Enabled {
		response.Sentry = &SentryFrontendConfig{
			Enabled:  true,
			DSN:      telemetry.GetFrontendDSN(),
			SystemID: c.Settings.SystemID,
		}
	}

	c.logDebugIfEnabled("Serving app config",
		logger.Bool("security_enabled", securityEnabled),
		logger.Bool("access_allowed", accessAllowed),
		logger.String("ip", ctx.RealIP()),
	)

	return ctx.JSON(http.StatusOK, response)
}

// determineWizardState computes the freshInstall, newVersion, and previousVersion fields
// by comparing the current app version with the last_seen_version stored in app_metadata.
//
// Rules:
//   - Dev builds (empty version or "Development Build"): both flags forced to false.
//   - If last_seen_version is missing and the database has zero detections: freshInstall = true.
//   - If last_seen_version differs from the current version (and not fresh install): newVersion = true.
func (c *Controller) determineWizardState(ctx context.Context) (freshInstall, newVersion bool, previousVersion string) {
	// Skip wizard triggers for dev builds
	if isDevBuild(c.Settings.Version) {
		return false, false, ""
	}

	// If metadata repository is not available, skip wizard state
	if c.appMetadataRepo == nil {
		return false, false, ""
	}

	lastSeenVersion, err := c.appMetadataRepo.Get(ctx, appMetadataKeyLastSeenVersion)
	if err != nil {
		c.logWarnIfEnabled("Failed to read last_seen_version from app_metadata", logger.Error(err))
		return false, false, ""
	}

	// If last_seen_version has never been set, check for fresh install
	if lastSeenVersion == "" {
		if c.hasZeroDetections(ctx) {
			return true, false, ""
		}
		// Existing install that predates wizard tracking — treat as upgrade from unknown version
		return false, true, ""
	}

	// If versions differ, this is an upgrade
	if lastSeenVersion != c.Settings.Version {
		return false, true, lastSeenVersion
	}

	// Version matches — no wizard needed
	return false, false, lastSeenVersion
}

// hasZeroDetections returns true if the V2 database contains no detections.
// Used to distinguish a fresh install (no data) from an existing install
// that predates wizard version tracking.
func (c *Controller) hasZeroDetections(ctx context.Context) bool {
	if c.V2Manager == nil {
		return true
	}

	tableName := "detections"
	if tp, ok := c.V2Manager.(interface{ TablePrefix() string }); ok && tp.TablePrefix() != "" {
		tableName = tp.TablePrefix() + tableName
	}

	var exists int
	err := c.V2Manager.DB().WithContext(ctx).Table(tableName).
		Select("1").Limit(1).Scan(&exists).Error
	if err != nil {
		c.logWarnIfEnabled("Failed to check detections for wizard state", logger.Error(err))
		// On error, assume not a fresh install to avoid showing wizard incorrectly
		return false
	}
	return exists == 0
}

// isDevBuild returns true for development/unversioned builds where wizard should be suppressed.
func isDevBuild(version string) bool {
	return version == "" || version == "Development Build"
}

// DismissWizard handles POST /api/v2/app/wizard/dismiss
// Updates the last_seen_version in app_metadata to the current application version,
// preventing the wizard from showing again until the next upgrade.
func (c *Controller) DismissWizard(ctx echo.Context) error {
	if c.appMetadataRepo == nil {
		return c.HandleError(ctx, nil, "App metadata not available", http.StatusServiceUnavailable)
	}

	if err := c.appMetadataRepo.Set(ctx.Request().Context(), appMetadataKeyLastSeenVersion, c.Settings.Version); err != nil {
		return c.HandleError(ctx, err, "Failed to dismiss wizard", http.StatusInternalServerError)
	}

	return ctx.NoContent(http.StatusNoContent)
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
