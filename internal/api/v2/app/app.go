// internal/api/v2/app/app.go
package app

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/api/middleware"
	"github.com/tphakala/birdnet-go/internal/branding"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/repository"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/speciesdict"
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

// appMetadataKeyOnboardingPending is the app_metadata key recorded at startup when
// the install has never been onboarded and its database holds no detections. It
// keeps the onboarding wizard pending until it is dismissed, independent of the
// config, which on a fresh install already contains an audio source (the default
// template) and possibly coordinates (written by install.sh).
const appMetadataKeyOnboardingPending = "onboarding_pending"

// onboardingPendingValue is the value of appMetadataKeyOnboardingPending while
// onboarding is pending. Any other value, including empty, means not pending.
const onboardingPendingValue = "true"

// onboardingSeedTimeout bounds the startup database work in recordOnboardingState.
const onboardingSeedTimeout = 5 * time.Second

// AppConfigResponse represents the application configuration returned to the frontend.
// This replaces the server-side injected window.BIRDNET_CONFIG.
type AppConfigResponse struct {
	CSRFToken          string                `json:"csrfToken"`
	Security           SecurityConfigDTO     `json:"security"`
	Version            string                `json:"version"`
	SpeciesDictVersion string                `json:"speciesDictVersion"`        // dataset version for content-addressed dictionary URL construction
	BasePath           string                `json:"basePath"`                  // reverse proxy prefix for frontend URL construction
	ColorScheme        string                `json:"colorScheme,omitempty"`     // admin-configured color scheme for all visitors
	CustomColors       *conf.CustomColors    `json:"customColors,omitempty"`    // custom scheme hex colors (when colorScheme is "custom")
	LogoStyle          string                `json:"logoStyle,omitempty"`       // admin-configured logo style: "gradient" or "solid"
	LiveSpectrogram    bool                  `json:"liveSpectrogram"`           // auto-start live spectrogram on dashboard
	AudioExportEnabled bool                  `json:"audioExportEnabled"`        // whether audio clip export is enabled; drives showing per-detection spectrogram/audio in the UI
	Layout             *conf.DashboardLayout `json:"layout,omitempty"`          // dashboard element layout for guest/pre-auth rendering
	FreshInstall       bool                  `json:"freshInstall"`              // true when this is a brand-new installation
	NewVersion         bool                  `json:"newVersion"`                // true when the app was upgraded since last dismiss
	PreviousVersion    string                `json:"previousVersion,omitempty"` // last version the user acknowledged
	Sentry             *SentryFrontendConfig `json:"sentry,omitempty"`          // frontend telemetry config (only when enabled)
	ProjectLinks       ProjectLinksConfig    `json:"projectLinks"`              // project identity/links served to the frontend
}

// SentryFrontendConfig exposes telemetry configuration to the frontend.
// Only included in AppConfigResponse when telemetry is enabled.
type SentryFrontendConfig struct {
	Enabled  bool   `json:"enabled"`
	DSN      string `json:"dsn"`
	SystemID string `json:"systemId"`
}

// ProjectLinksConfig exposes the configurable project identity (name and links)
// to the frontend so in-app links follow the branding package rather than
// hardcoded upstream URLs. It is always populated (not gated on telemetry).
type ProjectLinksConfig struct {
	Name           string `json:"name"`
	RepoURL        string `json:"repoUrl"`
	IssuesURL      string `json:"issuesUrl"`
	NewIssueURL    string `json:"newIssueUrl"`
	SupportURL     string `json:"supportUrl"`
	DiscussionsURL string `json:"discussionsUrl"`
	ReleasesURL    string `json:"releasesUrl"`
	CommunityURL   string `json:"communityUrl"`
}

// SecurityConfigDTO represents the security configuration for the frontend.
type SecurityConfigDTO struct {
	Enabled       bool            `json:"enabled"`
	AccessAllowed bool            `json:"accessAllowed"`
	AuthConfig    AuthConfigDTO   `json:"authConfig"`
	PublicAccess  PublicAccessDTO `json:"publicAccess"`
	// PrivateMode mirrors Security.PrivateMode so the frontend knows to
	// redirect to login on any 401 instead of silently treating gated
	// endpoints as graceful guest-mode limitations.
	PrivateMode bool `json:"privateMode"`
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

// RegisterAppRoutes registers application-level API endpoints on the provided v2
// API group, preserving the exact route order and middleware the monolithic
// initAppRoutes used. When a V2Manager is wired it also builds the app-metadata
// repository and records the startup onboarding state (recordOnboardingState).
func (c *Handler) RegisterAppRoutes(g *echo.Group) {
	// Initialize app metadata repository from V2Manager if available
	if c.V2Manager != nil {
		var useV2Prefix bool
		if tp, ok := c.V2Manager.(interface{ TablePrefix() string }); ok {
			useV2Prefix = tp.TablePrefix() != ""
		}
		c.appMetadataRepo = repository.NewAppMetadataRepository(
			c.V2Manager.DB(),
			nil,
			useV2Prefix,
			c.V2Manager.IsMySQL(),
		)

		// Record onboarding state at startup rather than on the first UI visit:
		// the default source starts capturing immediately, so detections can exist
		// before anyone opens the UI, and the persisted flag survives restarts.
		c.recordOnboardingState(context.Background())
	}

	// App config endpoint - publicly accessible (no auth required)
	// This endpoint provides the frontend with configuration data
	// that was previously injected server-side into the HTML template.
	g.GET(AppConfigEndpoint, c.GetAppConfig)

	// Wizard dismiss endpoint (requires authentication when auth is configured,
	// matching GetAppConfig, which reports wizard state only to a request with
	// access; without auth a fresh install is onboarded without logging in).
	// Not on the privateModeAuth exempt allow-list (see isPrivateModeExempt).
	g.POST(WizardDismissEndpoint, c.DismissWizard, c.AuthMiddleware)
}

// GetAppConfig handles GET /api/v2/app/config
// Returns the application configuration needed by the frontend SPA.
// This endpoint is public because:
// 1. It provides data needed before authentication can occur
// 2. The security.accessAllowed field tells the frontend if auth is needed
// 3. CSRF token is needed for any subsequent authenticated requests
//
// Wizard state (freshInstall, newVersion, previousVersion) is reported only to a
// request with access, so with auth configured an unauthenticated visitor is not
// offered a wizard.
func (c *Handler) GetAppConfig(ctx echo.Context) error {
	// Prevent caching of this response (contains user-specific CSRF token)
	ctx.Response().Header().Set("Cache-Control", "no-store, no-cache, must-revalidate, private")
	ctx.Response().Header().Set("Pragma", "no-cache")
	ctx.Response().Header().Set("Expires", "0")

	// Ensure a CSRF token is available for the SPA. The CSRF middleware forces
	// Echo's token path (GHSA-9fhj-f35q-w532), so a real token is normally already
	// in context; EnsureCSRFToken is the backstop that always returns one here.
	csrfToken, err := middleware.EnsureCSRFToken(ctx)
	if err != nil {
		c.LogWarnIfEnabled("Failed to generate CSRF token", logger.Error(err))
		return c.HandleError(ctx, err, "Failed to generate CSRF token", http.StatusInternalServerError)
	}

	// Snapshot the live settings once so the response reflects the latest
	// published configuration and every read below is race-free against a
	// concurrent settings save (see currentSettings).
	settings := c.CurrentSettings()
	if settings == nil {
		return c.HandleError(ctx, nil, "Settings not initialized", http.StatusServiceUnavailable)
	}

	// Get enabled OAuth providers from the new array-based config
	// This returns provider IDs for all enabled providers with valid credentials
	enabledProviders := settings.GetEnabledOAuthProviders()
	// Ensure we always return an array (not null) for JSON serialization
	if enabledProviders == nil {
		enabledProviders = []string{}
	}

	// Determine if any security method is enabled
	securityEnabled := settings.IsAuthProviderConfigured()

	// Determine if access is currently allowed
	accessAllowed := c.determineAccessAllowed(ctx, securityEnabled)

	// Determine the effective base path for reverse proxy support.
	// Priority: X-Ingress-Path > X-Forwarded-Prefix > config BasePath > empty.
	basePath := requestBasePath(ctx, settings)

	// Determine wizard state (freshInstall, newVersion, previousVersion) from the
	// same snapshot so the whole response is internally consistent.
	freshInstall, newVersion, previousVersion := c.determineWizardState(ctx.Request().Context(), settings)
	if !accessAllowed {
		freshInstall, newVersion, previousVersion = false, false, ""
	}

	// Build response
	response := AppConfigResponse{
		CSRFToken: csrfToken,
		Security: SecurityConfigDTO{
			Enabled:       securityEnabled,
			AccessAllowed: accessAllowed,
			AuthConfig: AuthConfigDTO{
				BasicEnabled:     settings.Security.BasicAuth.Enabled,
				EnabledProviders: enabledProviders,
			},
			PublicAccess: PublicAccessDTO{
				LiveAudio: settings.Security.PublicAccess.LiveAudio,
			},
			PrivateMode: settings.Security.PrivateMode,
		},
		Version:            settings.Version,
		SpeciesDictVersion: speciesdict.Version(),
		BasePath:           basePath,
		ColorScheme:        settings.Realtime.Dashboard.ColorScheme,
		CustomColors:       settings.Realtime.Dashboard.CustomColors,
		LogoStyle:          settings.Realtime.Dashboard.LogoStyle,
		LiveSpectrogram:    settings.Realtime.Dashboard.LiveSpectrogram,
		AudioExportEnabled: settings.Realtime.Audio.Export.Enabled,
		FreshInstall:       freshInstall,
		NewVersion:         newVersion,
		PreviousVersion:    previousVersion,
		// Project identity/links are always served (independent of telemetry) so
		// in-app links follow the configured branding instead of hardcoded URLs.
		ProjectLinks: ProjectLinksConfig{
			Name:           branding.Name(),
			RepoURL:        branding.RepoURL(),
			IssuesURL:      branding.IssuesURL(),
			NewIssueURL:    branding.NewIssueURL(),
			SupportURL:     branding.SupportURL(),
			DiscussionsURL: branding.DiscussionsURL(),
			ReleasesURL:    branding.ReleasesURL(),
			CommunityURL:   branding.CommunityURL(),
		},
	}

	// Include dashboard layout for guest/pre-auth rendering if configured
	if len(settings.Realtime.Dashboard.Layout.Elements) > 0 {
		response.Layout = &settings.Realtime.Dashboard.Layout
	}

	// Include Sentry frontend config when telemetry is enabled AND a DSN is
	// actually configured for this build. Builds without a DSN (plain `go build`
	// or forks) must not hand the frontend an empty DSN, which would make the
	// browser SDK fail to initialize.
	if settings.Sentry.Enabled {
		if dsn := telemetry.GetFrontendDSN(); dsn != "" {
			response.Sentry = &SentryFrontendConfig{
				Enabled:  true,
				DSN:      dsn,
				SystemID: settings.SystemID,
			}
		}
	}

	c.LogDebugIfEnabled("Serving app config",
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
//   - If last_seen_version is missing and onboarding_pending was recorded at startup:
//     freshInstall = true, without auto-seeding.
//   - If last_seen_version is missing and onboarding_pending cannot be read: both
//     flags false, without auto-seeding, so a later request can still decide.
//   - If last_seen_version is missing and isExistingInstall returns true: auto-seed and skip wizard.
//   - If last_seen_version is missing and no install signals: freshInstall = true.
//   - If last_seen_version differs from the current version: newVersion = true.
func (c *Handler) determineWizardState(ctx context.Context, settings *conf.Settings) (freshInstall, newVersion bool, previousVersion string) {
	// Skip wizard triggers for dev builds
	if isDevBuild(settings.Version) {
		return false, false, ""
	}

	// If metadata repository is not available, skip wizard state
	if c.appMetadataRepo == nil {
		return false, false, ""
	}

	lastSeenVersion, err := c.appMetadataRepo.Get(ctx, appMetadataKeyLastSeenVersion)
	if err != nil {
		c.LogWarnIfEnabled("Failed to read last_seen_version from app_metadata", logger.Error(err))
		return false, false, ""
	}

	// If last_seen_version has never been set, distinguish fresh from existing install
	if lastSeenVersion == "" {
		// A read error must not fall through to the auto-seed below: that would
		// record a version and hide onboarding for good on a transient failure.
		pending, err := c.appMetadataRepo.Get(ctx, appMetadataKeyOnboardingPending)
		if err != nil {
			c.LogWarnIfEnabled("Failed to read onboarding_pending from app_metadata", logger.Error(err))
			return false, false, ""
		}
		if pending == onboardingPendingValue {
			return true, false, ""
		}
		if c.isExistingInstall(ctx, settings) {
			// Auto-seed: existing install predates wizard tracking.
			// Intentional write inside GET handler (idempotent upsert, fires once per install).
			if err := c.appMetadataRepo.Set(ctx, appMetadataKeyLastSeenVersion, settings.Version); err != nil {
				c.LogWarnIfEnabled("Failed to auto-seed last_seen_version", logger.Error(err))
			}
			return false, false, settings.Version
		}
		return true, false, ""
	}

	// If versions differ, this is an upgrade
	if lastSeenVersion != settings.Version {
		return false, true, lastSeenVersion
	}

	// Version matches; no wizard needed
	return false, false, lastSeenVersion
}

// recordOnboardingState records onboarding_pending in app_metadata when the install
// has never been onboarded (no last_seen_version) and its database holds no
// detections. It runs once at startup so the decision does not depend on the
// config, which is never empty on a fresh install, nor on detections that
// accumulate before the first UI visit.
//
// It only records in enhanced database mode. In legacy mode the v2 database is
// the migration target and is empty while the user's data lives in the legacy
// database, so an empty v2 database proves nothing. Notes are not checked: the
// v2 schema has no notes table. Every failure leaves the flag unset, which falls
// back to the config-based isExistingInstall check.
func (c *Handler) recordOnboardingState(ctx context.Context) {
	if c.appMetadataRepo == nil || c.V2Manager == nil {
		return
	}
	if c.isEnhancedDatabase == nil || !c.isEnhancedDatabase() {
		return
	}

	ctx, cancel := context.WithTimeout(ctx, onboardingSeedTimeout)
	defer cancel()

	lastSeenVersion, err := c.appMetadataRepo.Get(ctx, appMetadataKeyLastSeenVersion)
	if err != nil {
		c.LogWarnIfEnabled("Failed to read last_seen_version for onboarding state", logger.Error(err))
		return
	}
	if lastSeenVersion != "" {
		return
	}

	if !c.hasZeroDetections(ctx) {
		return
	}

	// Set is an idempotent upsert, so a flag left by an earlier start is
	// simply rewritten.
	if err := c.appMetadataRepo.Set(ctx, appMetadataKeyOnboardingPending, onboardingPendingValue); err != nil {
		c.LogWarnIfEnabled("Failed to record pending onboarding", logger.Error(err))
		return
	}
	c.LogInfoIfEnabled("Recorded pending onboarding for fresh install")
}

// hasZeroDetections returns true if the V2 database contains no detections.
// Used to distinguish a fresh install (no data) from an existing install
// that predates wizard version tracking.
func (c *Handler) hasZeroDetections(ctx context.Context) bool {
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
		c.LogWarnIfEnabled("Failed to check detections for wizard state", logger.Error(err))
		// On error, assume not a fresh install to avoid showing wizard incorrectly
		return false
	}
	return exists == 0
}

// isExistingInstall returns true if multiple signals indicate this is a configured
// installation rather than a genuinely fresh one. Checks are ordered cheapest-first.
func (c *Handler) isExistingInstall(ctx context.Context, settings *conf.Settings) bool {
	if settings.BirdNET.Latitude != 0 || settings.BirdNET.Longitude != 0 {
		return true
	}
	if len(settings.Realtime.Audio.Sources) > 0 {
		return true
	}
	if settings.IsAuthProviderConfigured() {
		return true
	}
	if !c.hasZeroDetections(ctx) {
		return true
	}
	if c.hasNotes(ctx) {
		return true
	}
	return false
}

// hasNotes returns true if the notes table contains at least one row.
func (c *Handler) hasNotes(ctx context.Context) bool {
	if c.V2Manager == nil {
		return false
	}

	tableName := "notes"
	if tp, ok := c.V2Manager.(interface{ TablePrefix() string }); ok && tp.TablePrefix() != "" {
		tableName = tp.TablePrefix() + tableName
	}

	var exists int
	err := c.V2Manager.DB().WithContext(ctx).Table(tableName).
		Select("1").Limit(1).Scan(&exists).Error
	if err != nil {
		c.LogWarnIfEnabled("Failed to check notes for wizard state", logger.Error(err))
		return false
	}
	return exists != 0
}

// isDevBuild returns true for development/unversioned builds where wizard should be suppressed.
func isDevBuild(version string) bool {
	return version == "" || version == "Development Build"
}

// DismissWizard handles POST /api/v2/app/wizard/dismiss
// Updates the last_seen_version in app_metadata to the current application version,
// preventing the wizard from showing again until the next upgrade, then clears
// onboarding_pending. The clear is best effort: once last_seen_version is set the
// flag is never consulted.
func (c *Handler) DismissWizard(ctx echo.Context) error {
	if c.appMetadataRepo == nil {
		return c.HandleError(ctx, nil, "App metadata not available", http.StatusServiceUnavailable)
	}

	settings := c.CurrentSettings()
	if settings == nil {
		return c.HandleError(ctx, nil, "Settings not initialized", http.StatusServiceUnavailable)
	}
	if err := c.appMetadataRepo.Set(ctx.Request().Context(), appMetadataKeyLastSeenVersion, settings.Version); err != nil {
		return c.HandleError(ctx, err, "Failed to dismiss wizard", http.StatusInternalServerError)
	}
	if err := c.appMetadataRepo.Set(ctx.Request().Context(), appMetadataKeyOnboardingPending, ""); err != nil {
		c.LogWarnIfEnabled("Failed to clear onboarding_pending", logger.Error(err))
	}

	return ctx.NoContent(http.StatusNoContent)
}

// determineAccessAllowed checks if the current request has access.
// Returns true if:
// - Security is disabled (no auth required)
// - Auth service confirms the request is authenticated
func (c *Handler) determineAccessAllowed(ctx echo.Context, securityEnabled bool) bool {
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
//
// Header values are trimmed of trailing slashes before the safety check so that
// real-world values like "/ingress/token///" are accepted (and normalized) while
// embedded dangerous sequences like "//evil" or "/../admin" still get rejected.
func requestBasePath(c echo.Context, settings *conf.Settings) string {
	if p := strings.TrimRight(c.Request().Header.Get("X-Ingress-Path"), "/"); isSafePathPrefix(p) {
		return p
	}
	if p := strings.TrimRight(c.Request().Header.Get("X-Forwarded-Prefix"), "/"); isSafePathPrefix(p) {
		return p
	}
	if settings != nil && settings.WebServer.BasePath != "" {
		return strings.TrimRight(settings.WebServer.BasePath, "/")
	}
	return ""
}

// isSafePathPrefix validates that a path prefix (typically from a proxy header)
// is safe for use in redirects. Must stay in sync with the copies in
// internal/api/server.go and internal/api/auth/middleware.go. Rules align with
// validateBasePath() in internal/conf/validate.go so header-supplied and
// YAML-configured basepaths share the same rejection rules.
//
// Rejects:
//   - empty strings
//   - values not starting with "/"
//   - protocol-relative URLs ("//...") and embedded "//" sequences
//   - absolute URLs ("://")
//   - backslashes ("/\..." normalizes to "//" in browsers)
//   - path traversal ("..")
//   - CR/LF/NUL injection
func isSafePathPrefix(p string) bool {
	if p == "" || !strings.HasPrefix(p, "/") {
		return false
	}
	for _, bad := range []string{"//", "\\", "://", "..", "\n", "\r", "\x00"} {
		if strings.Contains(p, bad) {
			return false
		}
	}
	return true
}
