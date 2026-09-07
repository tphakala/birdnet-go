// internal/api/v2/settings.go
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"reflect"
	"slices"
	"sort"
	"strings"
	"text/template"
	"time"
	"unicode/utf8"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/audiocore/schedule"
	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/classifier/region"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/events"
	"github.com/tphakala/birdnet-go/internal/imageprovider"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/notification"
	"github.com/tphakala/birdnet-go/internal/profiling"
	"github.com/tphakala/birdnet-go/internal/restart"
	"github.com/tphakala/birdnet-go/internal/support"
	"github.com/tphakala/birdnet-go/internal/telemetry"
	"gopkg.in/yaml.v3"
)

// Settings validation and UI constants (file-local)
const (
	maxNodeNameLength     = 100                    // Maximum characters for node name
	actionDelay           = 100 * time.Millisecond // Delay between reconfiguration actions
	toastDurationShort    = 3000                   // Short toast duration (3 seconds)
	toastDurationMedium   = 4000                   // Medium toast duration (4 seconds)
	toastDurationLong     = 5000                   // Long toast duration (5 seconds)
	toastDurationExtended = 8000                   // Extended toast duration (8 seconds)
	minSortableElements   = 2                      // Minimum elements needed after first for sorting
)

// UpdateRequest represents a request to update settings
type UpdateRequest struct {
	Path  string `json:"path"`
	Value any    `json:"value"`
}

// initSettingsRoutes registers all settings-related API endpoints
func (c *Controller) initSettingsRoutes() {
	c.LogInfoIfEnabled("Initializing settings routes")

	// Public read-only endpoint: the Dashboard section contains only
	// layout/display preferences (no secrets, tokens, or PII) and must be
	// readable by unauthenticated guests so the SPA can render the dashboard
	// (species summary limit, layout, locale, thumbnails settings, etc.)
	// before login. The Layout is already exposed publicly via
	// /api/v2/app/config (see PR #2402). Mutations (PATCH) on this section
	// remain auth-protected — see the settingsGroup PATCH handler below.
	// Registered on the parent group so that Echo's router matches this
	// static path before the auth-protected `/:section` parameter route.
	c.Group.GET("/settings/dashboard", c.GetDashboardSettings)

	// Create auth-protected settings API group for everything else.
	settingsGroup := c.Group.Group("/settings", c.AuthMiddleware)

	// Routes for settings
	// GET /api/v2/settings - Retrieves all application settings
	settingsGroup.GET("", c.GetAllSettings)
	// GET /api/v2/settings/locales - Retrieves available locales for BirdNET (must be before /:section)
	settingsGroup.GET("/locales", c.GetLocales)
	// GET /api/v2/settings/imageproviders - Retrieves available image providers (must be before /:section)
	settingsGroup.GET("/imageproviders", c.GetImageProviders)
	// GET /api/v2/settings/systemid - Retrieves the system ID for support tracking (must be before /:section)
	settingsGroup.GET("/systemid", c.GetSystemID)
	// GET /api/v2/settings/:section - Retrieves settings for a specific section (e.g., birdnet, webserver)
	// NOTE: /settings/dashboard is intentionally registered publicly above and
	// will match before this parameterized route.
	settingsGroup.GET("/:section", c.GetSectionSettings)
	// PUT /api/v2/settings - Updates multiple settings sections with complete replacement
	settingsGroup.PUT("", c.UpdateSettings)
	// PATCH /api/v2/settings/:section - Updates a specific settings section with partial replacement
	// (includes /settings/dashboard — writes remain auth-protected).
	settingsGroup.PATCH("/:section", c.UpdateSectionSettings)

	c.LogInfoIfEnabled("Settings routes initialized successfully")
}

// GetAllSettings handles GET /api/v2/settings
func (c *Controller) GetAllSettings(ctx echo.Context) error {
	c.LogInfoIfEnabled("Getting all settings",
		logger.String("path", ctx.Request().URL.Path),
		logger.String("ip", ctx.RealIP()),
	)

	// Read the controller's lock-free settings snapshot.
	settings := c.ControllerSettings()
	if settings == nil {
		// Fallback to global settings if controller settings not set
		settings = conf.Setting()
		if settings == nil {
			c.LogErrorIfEnabled("Settings not initialized when trying to get all settings",
				logger.String("path", ctx.Request().URL.Path),
				logger.String("ip", ctx.RealIP()),
			)
			return c.HandleError(ctx, fmt.Errorf("settings not initialized"), "Failed to get settings", http.StatusInternalServerError)
		}
	}

	c.LogInfoIfEnabled("Retrieved all settings successfully",
		logger.String("path", ctx.Request().URL.Path),
		logger.String("ip", ctx.RealIP()),
	)

	// Return a sanitized copy with secrets redacted
	sanitized := sanitizeSettingsForAPI(settings)
	return ctx.JSON(http.StatusOK, sanitized)
}

// dashboardSectionName is the settings section key used for the publicly
// readable dashboard endpoint.
const dashboardSectionName = "dashboard"

// GetDashboardSettings handles the publicly accessible
// GET /api/v2/settings/dashboard endpoint. It returns the sanitized Dashboard
// section so that unauthenticated guests can render the SPA dashboard
// (species summary limit, layout, locale, thumbnails, etc.) without first
// completing login. The Dashboard section contains no secrets, tokens, or
// PII — the full settings payload (which does) remains behind auth. Writes
// to this section are handled by UpdateSectionSettings and remain
// auth-protected.
func (c *Controller) GetDashboardSettings(ctx echo.Context) error {
	c.LogAPIRequest(ctx, logger.LogLevelInfo, "Getting public dashboard settings")

	// Read the controller's lock-free settings snapshot.
	settings := c.ControllerSettings()
	if settings == nil {
		// Fallback to global settings if controller settings not set.
		settings = conf.Setting()
		if settings == nil {
			c.LogAPIRequest(ctx, logger.LogLevelError,
				"Settings not initialized when trying to get dashboard settings")
			return c.HandleError(ctx, fmt.Errorf("settings not initialized"),
				"Failed to get settings", http.StatusInternalServerError)
		}
	}

	// Sanitize first, then extract the dashboard section from the sanitized
	// copy. The Dashboard struct has no secret-bearing fields today, but
	// routing through sanitizeSettingsForAPI keeps this endpoint safe against
	// future additions.
	sanitized := sanitizeSettingsForAPI(settings)
	sectionValue, err := getSettingsSection(sanitized, dashboardSectionName)
	if err != nil {
		c.LogAPIRequest(ctx, logger.LogLevelError,
			"Failed to get dashboard settings section", logger.Error(err))
		return c.HandleError(ctx, err, "Failed to get settings section",
			http.StatusInternalServerError)
	}

	return ctx.JSON(http.StatusOK, sectionValue)
}

// GetSectionSettings handles GET /api/v2/settings/:section
func (c *Controller) GetSectionSettings(ctx echo.Context) error {
	section := ctx.Param("section")
	c.LogAPIRequest(ctx, logger.LogLevelInfo, "Getting settings for section", logger.String("section", section))

	if section == "" {
		c.LogAPIRequest(ctx, logger.LogLevelError, "Missing section parameter")
		return c.HandleError(ctx, fmt.Errorf("section not specified"), "Section parameter is required", http.StatusBadRequest)
	}

	// Read the controller's lock-free settings snapshot.
	settings := c.ControllerSettings()
	if settings == nil {
		// Fallback to global settings if controller settings not set
		settings = conf.Setting()
		if settings == nil {
			c.LogAPIRequest(ctx, logger.LogLevelError, "Settings not initialized when trying to get section settings", logger.String("section", section))
			return c.HandleError(ctx, fmt.Errorf("settings not initialized"), "Failed to get settings", http.StatusInternalServerError)
		}
	}

	// Sanitize first, then extract the section from the sanitized copy
	sanitized := sanitizeSettingsForAPI(settings)
	sectionValue, err := getSettingsSection(sanitized, section)
	if err != nil {
		c.LogAPIRequest(ctx, logger.LogLevelError, "Failed to get settings section", logger.String("section", section), logger.Error(err))
		return c.HandleError(ctx, err, "Failed to get settings section", http.StatusNotFound)
	}

	c.LogAPIRequest(ctx, logger.LogLevelInfo, "Retrieved settings section successfully", logger.String("section", section))

	return ctx.JSON(http.StatusOK, sectionValue)
}

// UpdateSettings handles PUT /api/v2/settings.
//
// The flow is copy-on-write: we clone the current *conf.Settings snapshot,
// apply the inbound update to the clone, validate, and atomically publish the
// clone via conf.StoreSettings. Readers on the hot path (e.g. the basepath
// strip middleware in internal/api/server.go) see either the old snapshot or
// the new one, never a torn view. Rollback after a validation or disk-write
// failure is a republish of the previous snapshot.
func (c *Controller) UpdateSettings(ctx echo.Context) error {
	c.LogAPIRequest(ctx, logger.LogLevelInfo, "Attempting to update settings")
	// Serialise concurrent PUT /api/v2/settings calls; each must see the
	// latest published snapshot as its baseline.
	c.settingsMutex.Lock()
	defer c.settingsMutex.Unlock()

	// Read the controller's own snapshot when set (tests inject this
	// directly); fall back to the global publisher. In production these are
	// the same pointer at boot and stay in sync because every successful
	// publish below updates both the atomic Settings pointer and conf.settingsInstance.
	current := c.getSettingsOrFallback()
	if current == nil {
		c.LogAPIRequest(ctx, logger.LogLevelError, "Settings not initialized during update attempt")
		return c.HandleError(ctx, fmt.Errorf("settings not initialized"), "Failed to get settings", http.StatusInternalServerError)
	}

	// Build a mutable clone; never mutate current in place. Readers holding
	// current through conf.GetSettings continue to see a consistent snapshot
	// until StoreSettings publishes the new one.
	updated := conf.CloneSettings(current)

	// Publish to the global atomic pointer only when this controller owns
	// it (production). Determined once at construction, so out-of-band
	// StoreSettings calls (range filter, etc.) cannot desynchronize it.
	publishGlobal := c.isGlobalOwner

	// Parse the request body as raw JSON. Binding into a typed conf.Settings (the
	// previous behavior) filled every field the body omitted with its Go zero
	// value, which the reflective apply then wrote over the live value, silently
	// blanking unmentioned settings (issue #3993: lat/long reset to 0,0,
	// output.sqlite.path cleared, species lists and integrations dropped). Merging
	// the raw JSON into the clone preserves keys the caller did not send.
	requestBody, err := parseAndValidateJSON(ctx)
	if err != nil {
		c.LogAPIRequest(ctx, logger.LogLevelError, "Failed to parse request body for settings update", logger.Error(err))
		return c.HandleError(ctx, err, "Failed to parse request body", http.StatusBadRequest)
	}

	// Deep-merge the incoming JSON into the clone, preserving keys absent from the
	// request. This is the same omission-preserving merge PATCH uses per section,
	// applied to the whole settings object; Go map-typed fields (species config,
	// taxonomy synonyms) are replaced wholesale when present so the UI can still
	// delete or rename a species-config entry by omitting its key.
	if err := mergeFullSettings(updated, requestBody); err != nil {
		c.LogAPIRequest(ctx, logger.LogLevelError, "Failed to merge settings update", logger.Error(err))
		return c.HandleError(ctx, err, "Failed to apply settings update", http.StatusBadRequest)
	}

	// Restore redacted secret placeholders to their live values so the merge does
	// not persist the placeholder over a real secret. current is the source of
	// truth for pre-update secrets (same argument order and ordering as PATCH).
	if err := restoreRedactedSecrets(current, updated); err != nil {
		c.LogAPIRequest(ctx, logger.LogLevelWarn, "Redacted sentinel validation failed", logger.Error(err))
		return c.HandleError(ctx, err, "Cannot save: some secret fields contain the redacted placeholder because their identifying key was changed while the secret was hidden. Re-enter the secret values.", http.StatusBadRequest)
	}

	// Enforce the blocked-field map: revert every blocked leaf to its pre-update
	// value. The merge above wrote whatever the request carried, so this is what
	// stops a client changing a never-updatable-via-API field, and it is what
	// restores Security.BasicAuth.ClientID/ClientSecret, which sanitizeSettingsForAPI
	// BLANKS (not redacts) so a full-object round trip carries empty strings for
	// them. Same mechanism PATCH uses (restoreBlockedFields), replacing the
	// field-by-field skip the old typed walk performed.
	skippedFields := restoreBlockedFields(current, updated)
	if len(skippedFields) > 0 {
		// Debug, not Warn as on the PATCH path, and deliberately so: the frontend
		// sends the full settings object on every save, and the blanked BasicAuth
		// client credentials are reverted (and thus reported) on every save that
		// has BasicAuth configured. Logging that at Warn would fire on every save.
		// PATCH warns because it only touches the security section when the client
		// actually PATCHes it.
		c.LogAPIRequest(ctx, logger.LogLevelDebug, "Reverted blocked settings fields during full settings update", logger.Any("skipped_fields", skippedFields))
	}

	// Normalize species config keys to lowercase for case-insensitive matching.
	if updated.Realtime.Species.Config != nil {
		updated.Realtime.Species.Config = conf.NormalizeSpeciesConfigKeys(updated.Realtime.Species.Config)
	}

	// Canonicalize the species exclude list to scientific names so a localized or
	// common name typed into the Settings exclude editor is stored in the same form
	// the per-detection filter and the detection-card toggle match. Idempotent for
	// an already-canonical list, so this does not spuriously trigger a rebuild.
	updated.Realtime.Species.Exclude = c.canonicalizeExcludeList(updated.Realtime.Species.Exclude)

	// Ensure LocationConfigured is set when birdnet coordinates are present.
	// Backward compatibility with older frontends that don't send the flag.
	if updated.BirdNET.Latitude != 0 || updated.BirdNET.Longitude != 0 {
		updated.BirdNET.LocationConfigured = true
	}

	// Migrate legacy single audio source if a cached frontend sent it.
	updated.MigrateAudioSourceConfig()

	// Validate the clone before publishing. No rollback needed on validation
	// failure: we simply never publish.
	if err := conf.ValidateSettings(updated); err != nil {
		return c.HandleError(ctx, err, "Invalid settings", http.StatusBadRequest)
	}

	// Publish the new snapshot. conf.StoreSettings publishes atomically to
	// the global (readers via conf.GetSettings immediately see this version;
	// existing pointer holders stay on the old). c.Settings.Store republishes
	// the controller's own atomic snapshot so per-controller readers
	// (controllerSettings) return the freshly published value. Both stores are
	// lock-free; settingsMutex here only serialises this read-modify-write
	// against other update handlers, not against readers.
	ensureProfilingTokenForSave(updated)
	if publishGlobal {
		conf.StoreSettings(updated)
	}
	c.Settings.Store(updated)

	// Run cross-field side-effects (interval tracking, telemetry toggles, etc.)
	// against the published pair. handleSettingsChanges is read-only on both.
	if err := c.handleSettingsChanges(current, updated); err != nil {
		// Rollback: republish the previous snapshot so in-memory state matches
		// what is on disk (which was never overwritten).
		if publishGlobal {
			conf.StoreSettings(current)
		}
		c.Settings.Store(current)
		c.LogAPIRequest(ctx, logger.LogLevelError, "Failed to apply settings changes, rolling back", logger.Error(err))
		return c.HandleError(ctx, err, "Failed to apply settings changes, rolled back to previous settings", http.StatusInternalServerError)
	}

	// Persist to disk only when this controller owns the global snapshot
	// (production path) AND DisableSaveSettings is not set. conf.SaveSettings
	// reads conf.GetSettings internally; persisting from a test that stored a
	// standalone snapshot via c.Settings.Store would save an unrelated snapshot.
	if publishGlobal && !c.DisableSaveSettings {
		if err := conf.SaveSettings(); err != nil {
			// Rollback in-memory; disk write never happened successfully.
			conf.StoreSettings(current)
			c.Settings.Store(current)
			restoreProfilingRates(current)
			c.LogAPIRequest(ctx, logger.LogLevelError, "Failed to save settings to disk, rolling back", logger.Error(err))
			return c.HandleError(ctx, err, "Failed to save settings, rolled back to previous settings", http.StatusInternalServerError)
		}
	}

	// Emit settings_saved event with key-level diff (fire-and-forget).
	if changes := diffSettings(current, updated); len(changes) > 0 {
		events.Emit(context.Background(), "settings", "settings_saved", "Settings saved via UI", map[string]any{
			"source":       "ui",
			"change_count": len(changes),
			"changes":      changes,
		})
	}

	telemetry.UpdateTelemetryEnabled()
	imageprovider.SetCustomSynonyms(updated.TaxonomySynonyms, updated.BirdNET.Labels)

	if publishGlobal && !c.DisableSaveSettings {
		c.LogAPIRequest(ctx, logger.LogLevelInfo, "Settings updated and saved successfully",
			logger.Int("skipped_fields_count", len(skippedFields)))
	} else {
		c.LogAPIRequest(ctx, logger.LogLevelDebug, "Settings updated (save to disk skipped)",
			logger.Bool("publishGlobal", publishGlobal),
			logger.Bool("disableSaveSettings", c.DisableSaveSettings))
	}

	return ctx.JSON(http.StatusOK, map[string]any{
		"message":          "Settings updated successfully",
		"skippedFields":    skippedFields,
		"restart_required": restart.IsRestartRequired(),
		"restart_reasons":  restart.GetRestartReasons(),
	})
}

// publishAndSaveSettings publishes updated settings and persists to disk.
// Must be called while c.settingsMutex is held. On save failure, the atomic
// Settings snapshot is rolled back to current.
// ensureProfilingTokenForSave mints the profiling token into a settings
// snapshot that is about to be published and persisted, so enabling pprof at
// runtime on an instance with no authentication provider yields a usable
// credential rather than an endpoint that refuses every request until restart.
//
// It is called at each of the three publish points rather than from one shared
// save helper because the settings-write paths each inline their own
// publish-then-persist sequence. Hooking only one of them is exactly how the
// first attempt at this shipped a dead feature: publishAndSaveSettings is used
// by the TLS, integrations and detections writers, none of which can carry a
// diagnostics change, so the two handlers that CAN (PUT /settings and
// PATCH /settings/:section) never minted anything.
//
// Non-fatal by design. The pprof routes fail closed without a token, and a
// diagnostics credential must not cost the user the rest of their settings
// change. EnsureProfilingToken is a no-op unless profiling is enabled, the
// token is empty, and no auth provider is configured, so the normal save path
// pays a boolean check and nothing else.
func ensureProfilingTokenForSave(updated *conf.Settings) {
	if _, err := conf.EnsureProfilingToken(updated); err != nil {
		apicore.GetLogger().Warn(
			"Failed to generate profiling token; the profiling endpoints will refuse requests",
			logger.Error(err))
	}
}

func (c *Controller) publishAndSaveSettings(current, updated *conf.Settings) error {
	ensureProfilingTokenForSave(updated)
	if c.isGlobalOwner {
		conf.StoreSettings(updated)
	}
	c.Settings.Store(updated)

	if c.isGlobalOwner && !c.DisableSaveSettings {
		if err := conf.SaveSettings(); err != nil {
			conf.StoreSettings(current)
			c.Settings.Store(current)
			return fmt.Errorf("failed to save settings: %w", err)
		}
	}

	// Emit settings_saved event with key-level diff (fire-and-forget).
	if changes := diffSettings(current, updated); len(changes) > 0 {
		events.Emit(context.Background(), "settings", "settings_saved", "Settings saved via UI", map[string]any{
			"source":       "ui",
			"change_count": len(changes),
			"changes":      changes,
		})
	}

	return nil
}

// getSettingsOrFallback returns the current settings snapshot for write handlers.
// When this controller owns the global singleton (production), it reads from
// conf.GetSettings() so that out-of-band publishers (range filter rebuild,
// ShouldUpdateRangeFilterToday, etc.) are not silently overwritten by a stale
// per-controller pointer. For test controllers that inject a standalone *Settings,
// the controller's own atomic snapshot is returned as-is.
func (c *Controller) getSettingsOrFallback() *conf.Settings {
	if c.isGlobalOwner {
		if s := conf.GetSettings(); s != nil {
			return s
		}
	}
	if s := c.Settings.Load(); s != nil {
		return s
	}
	return conf.Setting()
}

// parseAndValidateJSON binds and validates the request body as JSON.
func parseAndValidateJSON(ctx echo.Context) (json.RawMessage, error) {
	var requestBody json.RawMessage
	if err := ctx.Bind(&requestBody); err != nil {
		return nil, err
	}
	var tempValue any
	if err := json.Unmarshal(requestBody, &tempValue); err != nil {
		return nil, err
	}
	return requestBody, nil
}

// UpdateSectionSettings handles PATCH /api/v2/settings/:section.
//
// Uses the same copy-on-write flow as UpdateSettings: clone the current
// *conf.Settings snapshot, apply the section merge to the clone, validate,
// and publish via conf.StoreSettings. Rollback on failure republishes the
// previous snapshot. Keeps PATCH race-free against basepath middleware reads
// and any other reader that goes through conf.GetSettings().
func (c *Controller) UpdateSectionSettings(ctx echo.Context) error {
	c.settingsMutex.Lock()
	defer c.settingsMutex.Unlock()

	section := ctx.Param("section")
	if section == "" {
		return c.HandleError(ctx, fmt.Errorf("section not specified"), "Section parameter is required", http.StatusBadRequest)
	}

	current := c.getSettingsOrFallback()
	if current == nil {
		return c.HandleError(ctx, fmt.Errorf("settings not initialized"), "Failed to get settings", http.StatusInternalServerError)
	}

	// Build a mutable clone; never mutate current in place. Readers holding
	// current through conf.GetSettings keep seeing a consistent snapshot
	// until StoreSettings publishes the new one.
	updated := conf.CloneSettings(current)

	// Publish globally when the controller owns the global singleton.
	// Determined once at construction, immune to pointer desync from
	// out-of-band StoreSettings calls (range filter rebuild, etc.).
	publishGlobal := c.isGlobalOwner

	requestBody, err := parseAndValidateJSON(ctx)
	if err != nil {
		return c.HandleError(ctx, err, "Failed to parse request body", http.StatusBadRequest)
	}

	if err := updateSettingsSection(updated, section, requestBody); err != nil {
		return c.HandleError(ctx, err, fmt.Sprintf("Failed to update %s settings", section), http.StatusBadRequest)
	}

	// Restore redacted secret fields to their current values so the merge
	// does not overwrite real secrets with the placeholder. current is the
	// source of truth for the pre-update values.
	if err := restoreRedactedSecrets(current, updated); err != nil {
		c.LogAPIRequest(ctx, logger.LogLevelWarn, "Redacted sentinel validation failed", logger.Error(err))
		return c.HandleError(ctx, err, "Cannot save: some secret fields contain the redacted placeholder because their identifying key was changed while the secret was hidden. Re-enter the secret values.", http.StatusBadRequest)
	}

	// Enforce the blocked-field map. The section merge above writes whatever the
	// request contained, so this is the only thing standing between a client and
	// a field the map marks never-updatable-via-API (Security.SessionSecret,
	// Diagnostics.Profiling.Token, the audio tool paths, ...). It runs after
	// restoreRedactedSecrets so that a normal GET-modify-PATCH round trip, which
	// sends the redacted placeholder back for SessionSecret, is not reported as
	// an attempt to change it; TestPatchReportsBlockedFieldsItRejected pins that
	// ordering. The exception is Security.BasicAuth.ClientID/ClientSecret, which
	// sanitizeSettingsForAPI BLANKS rather than redacts, so restoreRedactedSecrets
	// does not cover them and such a round trip does report them. That is honest
	// (the request really did carry a different value) and it is also what stops
	// the round trip from wiping both credentials, as it did before this check
	// existed.
	skippedFields := restoreBlockedFields(current, updated)
	if len(skippedFields) > 0 {
		// Same field key as the PUT path and as the response JSON, so one query
		// finds a rejection on either verb. The two differ only in log level: this
		// one is Warn, PUT's is Debug, because a full-object PUT reverts the blanked
		// BasicAuth client credentials on every save (see the PUT call site).
		c.LogAPIRequest(ctx, logger.LogLevelWarn, "Rejected update to blocked settings fields",
			logger.String("section", section),
			logger.Any("skipped_fields", skippedFields))
	}

	// Ensure LocationConfigured is set when birdnet coordinates are present.
	// Backward compatibility with older frontends that don't send the flag.
	if strings.EqualFold(section, SettingsSectionBirdnet) {
		if updated.BirdNET.Latitude != 0 || updated.BirdNET.Longitude != 0 {
			updated.BirdNET.LocationConfigured = true
		}
	}

	// Migrate legacy single audio source if a cached frontend sent it.
	updated.MigrateAudioSourceConfig()

	// Canonicalize the species exclude list to scientific names (see UpdateSettings).
	// Only the realtime/species sections carry it, so skip other sections: an
	// unrelated section save must not rewrite the list or trigger a spurious
	// range-filter rebuild via rangeFilterSettingsChanged.
	if strings.EqualFold(section, SettingsSectionRealtime) || strings.EqualFold(section, SettingsSectionSpecies) {
		updated.Realtime.Species.Exclude = c.canonicalizeExcludeList(updated.Realtime.Species.Exclude)
	}

	// Validate the clone before publishing. No rollback needed on validation
	// failure: we simply never publish.
	if err := conf.ValidateSettings(updated); err != nil {
		return c.HandleError(ctx, err,
			fmt.Sprintf("Invalid %s settings", section), http.StatusBadRequest)
	}

	// Publish the new snapshot atomically when we own the global, then
	// republish the controller's own atomic snapshot. See the matching comment
	// in UpdateSettings for why Debug reads via conf.GetSettings() (the global)
	// rather than the per-controller snapshot.
	ensureProfilingTokenForSave(updated)
	if publishGlobal {
		conf.StoreSettings(updated)
	}
	c.Settings.Store(updated)

	if err := c.handleSettingsChanges(current, updated); err != nil {
		if publishGlobal {
			conf.StoreSettings(current)
		}
		c.Settings.Store(current)
		return c.HandleError(ctx, err, "Failed to apply settings changes, rolled back to previous settings", http.StatusInternalServerError)
	}

	// Persist to disk only when this controller owns the global snapshot
	// AND the test did not disable save. conf.SaveSettings persists the
	// conf.GetSettings value, which would be wrong under a standalone
	// snapshot injected by a test that bypassed the global publish.
	if publishGlobal && !c.DisableSaveSettings {
		if err := conf.SaveSettings(); err != nil {
			conf.StoreSettings(current)
			c.Settings.Store(current)
			restoreProfilingRates(current)
			return c.HandleError(ctx, err, "Failed to save settings, rolled back to previous settings", http.StatusInternalServerError)
		}
		c.LogAPIRequest(ctx, logger.LogLevelInfo, "Section settings saved successfully",
			logger.String("section", section))
	}

	if !publishGlobal || c.DisableSaveSettings {
		c.LogAPIRequest(ctx, logger.LogLevelDebug, "Section settings updated (save to disk skipped)",
			logger.String("section", section),
			logger.Bool("publishGlobal", publishGlobal),
			logger.Bool("disableSaveSettings", c.DisableSaveSettings))
	}

	// Emit settings_saved event with key-level diff (fire-and-forget).
	if changes := diffSettings(current, updated); len(changes) > 0 {
		events.Emit(context.Background(), "settings", "settings_saved", "Settings saved via UI", map[string]any{
			"source":       "ui",
			"change_count": len(changes),
			"changes":      changes,
		})
	}

	telemetry.UpdateTelemetryEnabled()

	// Rebuild taxonomy synonym lookup cache if overrides changed
	imageprovider.SetCustomSynonyms(updated.TaxonomySynonyms, updated.BirdNET.Labels)

	return ctx.JSON(http.StatusOK, map[string]any{
		"message":          fmt.Sprintf("%s settings updated successfully", section),
		"skippedFields":    skippedFields,
		"restart_required": restart.IsRestartRequired(),
		"restart_reasons":  restart.GetRestartReasons(),
	})
}

// updateSettingsSection validates and merges a request body into one section of
// settings. It does not enforce the blocked-field map; UpdateSectionSettings
// calls restoreBlockedFields after this returns. See handleGenericSection.
func updateSettingsSection(settings *conf.Settings, section string, data json.RawMessage) error {
	section = strings.ToLower(section)

	var tempValue any
	if err := json.Unmarshal(data, &tempValue); err != nil {
		return fmt.Errorf("invalid JSON for section %s: %w", section, err)
	}

	// First, check if there's a special validator for this section
	validators := getSectionValidators()
	if validator, exists := validators[section]; exists {
		if err := validator(data); err != nil {
			return fmt.Errorf("validation failed for section %s: %w", section, err)
		}
	}

	// Get the settings section by name
	sectionValue, err := getSettingsSectionValue(settings, section)
	if err != nil {
		return err
	}

	// Use the generic handler with merging for ALL sections
	return handleGenericSection(sectionValue, data, section)
}

// mergeJSONIntoStruct merges JSON data into an existing struct, preserving fields not
// present in the update. It deep-merges the current struct state with the incoming JSON
// at the map level, then writes the merged result back into the target struct.
func mergeJSONIntoStruct(data json.RawMessage, target any) error {
	// First unmarshal into a map
	var updateMap map[string]any
	if err := json.Unmarshal(data, &updateMap); err != nil {
		return err
	}

	// Get current values as a map
	currentJSON, err := json.Marshal(target)
	if err != nil {
		return err
	}

	var currentMap map[string]any
	if err := json.Unmarshal(currentJSON, &currentMap); err != nil {
		return err
	}

	// Deep merge the maps
	mergedMap := deepMergeMaps(currentMap, updateMap)

	// Marshal back to JSON and unmarshal into the target
	mergedJSON, err := json.Marshal(mergedMap)
	if err != nil {
		return err
	}

	// Nil out all slice fields before unmarshaling the merged result.
	// json.Unmarshal reuses existing slice backing arrays, so fields omitted
	// from JSON (e.g. width:"" via omitempty) retain their old values in
	// slice elements. By nilling slices first, json.Unmarshal allocates fresh
	// backing arrays and every element starts at its zero value.
	//
	// Only slices are affected — scalar, map, and struct fields are correctly
	// overwritten by json.Unmarshal. We must NOT zero the entire struct because
	// fields tagged json:"-" (runtime values like Labels, SoxAudioTypes) would
	// be destroyed and are absent from mergedJSON.
	zeroJSONSliceFields(reflect.ValueOf(target))

	return json.Unmarshal(mergedJSON, target)
}

// zeroJSONSliceFields recursively nils all JSON-visible slice fields in a
// struct so that json.Unmarshal allocates fresh backing arrays instead of
// reusing stale ones. Fields tagged json:"-" are skipped because they hold
// runtime values that are absent from the merged JSON and must be preserved.
func zeroJSONSliceFields(v reflect.Value) {
	if v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return
	}
	for sf, field := range v.Fields() {
		if !field.CanSet() {
			continue
		}
		// Skip fields invisible to JSON — they hold runtime values not
		// present in mergedJSON and would be permanently lost.
		if tag, ok := sf.Tag.Lookup("json"); ok && tag == "-" {
			continue
		}
		switch field.Kind() {
		case reflect.Slice:
			field.Set(reflect.Zero(field.Type()))
		case reflect.Struct:
			zeroJSONSliceFields(field)
		case reflect.Pointer:
			if !field.IsNil() && field.Elem().Kind() == reflect.Struct {
				zeroJSONSliceFields(field)
			}
		default:
			// Only slices need zeroing — scalars, maps, etc. are correctly
			// overwritten by json.Unmarshal without stale value issues.
		}
	}
}

// deepMergeMaps recursively merges two maps, with values from src overwriting dst
func deepMergeMaps(dst, src map[string]any) map[string]any {
	result := make(map[string]any)

	// Copy all values from dst
	maps.Copy(result, dst)

	// Merge values from src
	for k, v := range src {
		if v == nil {
			// If src value is explicitly null, set it to null
			result[k] = nil
			continue
		}

		// Check if both dst and src have maps at this key
		if dstMap, dstOk := dst[k].(map[string]any); dstOk {
			if srcMap, srcOk := v.(map[string]any); srcOk {
				// Both are maps, merge recursively
				result[k] = deepMergeMaps(dstMap, srcMap)
				continue
			}
		}

		// Otherwise, just use the src value
		result[k] = v
	}

	return result
}

// mergeFullSettings merges a full-settings PUT body into target, preserving keys
// the caller omitted (issue #3993). It mirrors mergeJSONIntoStruct but differs in
// two ways that matter only for the whole-settings object:
//
//   - it uses deepMergeSettingsMaps, which REPLACES Go map-typed fields wholesale
//     instead of merging them key-by-key, and
//   - it zeroes the target's JSON-visible slices AND maps (zeroJSONSliceAndMapFields)
//     before the final unmarshal.
//
// Both are needed so a full-object PUT can delete or rename a species-config entry
// by omitting its key: deepMergeSettingsMaps drops the key from the merged JSON,
// and zeroing the target map first stops json.Unmarshal (which keeps existing
// entries when unmarshaling an object into a non-nil map) from resurrecting it.
// PATCH keeps its own key-by-key merge (mergeJSONIntoStruct), so a partial section
// PATCH still merges into the existing map.
func mergeFullSettings(target *conf.Settings, data json.RawMessage) error {
	var updateMap map[string]any
	if err := json.Unmarshal(data, &updateMap); err != nil {
		return err
	}

	currentJSON, err := json.Marshal(target)
	if err != nil {
		return err
	}
	var currentMap map[string]any
	if err := json.Unmarshal(currentJSON, &currentMap); err != nil {
		return err
	}

	merged := deepMergeSettingsMaps(currentMap, updateMap, reflect.TypeFor[conf.Settings]())

	mergedJSON, err := json.Marshal(merged)
	if err != nil {
		return err
	}

	// Clear slices and maps first so json.Unmarshal repopulates them from the
	// merged JSON alone (see zeroJSONSliceAndMapFields).
	zeroJSONSliceAndMapFields(reflect.ValueOf(target))

	return json.Unmarshal(mergedJSON, target)
}

// deepMergeSettingsMaps deep-merges src into dst like deepMergeMaps, EXCEPT that a
// key whose target struct field is a Go map (reflect.Map) is REPLACED wholesale
// rather than merged key-by-key. t is the Go type of the struct dst represents and
// is used only to tell struct fields (which merge, preserving keys the caller
// omitted: the #3993 fix) from Go map fields (which replace, so the caller can
// delete a map entry by omitting it). When t is nil or the field cannot be
// resolved it falls back to deepMergeMaps behavior (merge), so a key that cannot
// be typed can never silently drop sibling data.
func deepMergeSettingsMaps(dst, src map[string]any, t reflect.Type) map[string]any {
	result := make(map[string]any, len(dst))
	maps.Copy(result, dst)

	for k, v := range src {
		if v == nil {
			// Explicit null: honor it (clears the field), matching deepMergeMaps.
			result[k] = nil
			continue
		}

		fieldType := settingsFieldType(t, k)
		isGoMap := fieldType != nil && fieldType.Kind() == reflect.Map

		// Recurse only for struct-shaped fields; Go maps and everything else are
		// replaced with the incoming value.
		if !isGoMap {
			if dstMap, dstOk := dst[k].(map[string]any); dstOk {
				if srcMap, srcOk := v.(map[string]any); srcOk {
					result[k] = deepMergeSettingsMaps(dstMap, srcMap, fieldType)
					continue
				}
			}
		}

		result[k] = v
	}

	return result
}

// settingsFieldType returns the pointer-dereferenced type of the struct field in t
// whose JSON name matches key, or nil if t is not a struct or has no such field.
// Matching mirrors encoding/json: the json tag name (the part before the first
// comma) wins case-insensitively, otherwise the Go field name case-insensitively.
// Fields tagged json:"-" are ignored. It recurses into untagged embedded structs
// to reach promoted fields and carries no cycle guard; that is safe because it is
// only called on conf.Settings, an acyclic type with no self-referential pointer
// embedding.
func settingsFieldType(t reflect.Type, key string) reflect.Type {
	if t == nil {
		return nil
	}
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return nil
	}

	for f := range t.Fields() {
		// encoding/json ignores unexported fields, so mirror that: an unexported
		// field whose name matches key must not shadow the exported field the
		// request key actually refers to.
		if !f.IsExported() {
			continue
		}
		name := f.Name
		tagged := false
		if tag := f.Tag.Get("json"); tag != "" {
			if comma := strings.IndexByte(tag, ','); comma >= 0 {
				tag = tag[:comma]
			}
			if tag == "-" {
				continue
			}
			if tag != "" {
				name = tag
				tagged = true
			}
		}
		if strings.EqualFold(name, key) {
			ft := f.Type
			for ft.Kind() == reflect.Pointer {
				ft = ft.Elem()
			}
			return ft
		}
		// An untagged embedded struct promotes its fields to the parent level
		// (encoding/json semantics), so resolve key among the promoted fields.
		// reflect.Type.Fields yields the embedded field itself, not its promoted
		// members, so this recursion is what reaches them.
		if f.Anonymous && !tagged {
			embedded := f.Type
			for embedded.Kind() == reflect.Pointer {
				embedded = embedded.Elem()
			}
			if embedded.Kind() == reflect.Struct {
				if ft := settingsFieldType(embedded, key); ft != nil {
					return ft
				}
			}
		}
	}
	return nil
}

// zeroJSONSliceAndMapFields is zeroJSONSliceFields extended to also zero
// JSON-visible Go map fields. mergeFullSettings replaces map fields wholesale in
// the merged JSON, but json.Unmarshal keeps existing entries when it unmarshals a
// JSON object into a non-nil Go map, so a species-config entry the caller deleted
// would survive in the target clone unless the map is cleared first. Slices are
// zeroed for the same stale-backing-array reason as zeroJSONSliceFields. Fields
// tagged json:"-" are skipped because they hold runtime values absent from the
// merged JSON. Kept separate from zeroJSONSliceFields so the PATCH merge keeps its
// key-by-key map merge.
func zeroJSONSliceAndMapFields(v reflect.Value) {
	if v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return
	}
	for sf, field := range v.Fields() {
		if !field.CanSet() {
			continue
		}
		// Skip fields invisible to JSON — they hold runtime values not present in
		// mergedJSON and would be permanently lost.
		if tag, ok := sf.Tag.Lookup("json"); ok && tag == "-" {
			continue
		}
		switch field.Kind() {
		case reflect.Slice, reflect.Map:
			field.Set(reflect.Zero(field.Type()))
		case reflect.Struct:
			zeroJSONSliceAndMapFields(field)
		case reflect.Pointer:
			if !field.IsNil() && field.Elem().Kind() == reflect.Struct {
				zeroJSONSliceAndMapFields(field)
			}
		default:
			// Slices and maps are zeroed above; scalars and structs are left as
			// they are and json.Unmarshal overwrites them from the merged JSON. A
			// JSON null is the one value json.Unmarshal ignores for a non-pointer
			// scalar or struct, so a null in the body leaves such a field at its
			// current value rather than clearing it. This matches PATCH's
			// mergeJSONIntoStruct exactly (null clears slices and maps, is a no-op
			// for scalars and structs).
		}
	}
}

// normalizeSpeciesConfigKeysInJSON normalizes species config map keys to lowercase in the JSON data.
// This ensures case-insensitive key matching during deep merge operations.
// For "species" section: normalizes keys in the "config" field
// For "realtime" section: normalizes keys in the "species.config" field
func normalizeSpeciesConfigKeysInJSON(data json.RawMessage, sectionName string) (json.RawMessage, error) {
	// Only process species-related sections
	if sectionName != SettingsSectionSpecies && sectionName != SettingsSectionRealtime {
		return data, nil
	}

	var dataMap map[string]any
	if err := json.Unmarshal(data, &dataMap); err != nil {
		// For species/realtime sections, we expect a JSON object
		// Return error for clearer feedback on malformed requests
		return nil, fmt.Errorf("failed to unmarshal section data as JSON object: %w", err)
	}

	modified := false

	switch sectionName {
	case SettingsSectionSpecies:
		// Direct species section: normalize "config" keys
		if configMap, ok := dataMap["config"].(map[string]any); ok {
			dataMap["config"] = normalizeMapKeysToLowercase(configMap)
			modified = true
		}
	case SettingsSectionRealtime:
		// Realtime section: normalize "species.config" keys
		if speciesMap, ok := dataMap["species"].(map[string]any); ok {
			if configMap, ok := speciesMap["config"].(map[string]any); ok {
				speciesMap["config"] = normalizeMapKeysToLowercase(configMap)
				modified = true
			}
		}
	}

	if !modified {
		return data, nil
	}

	return json.Marshal(dataMap)
}

// normalizeMapKeysToLowercase converts all keys in a map to lowercase.
// Uses a two-pass algorithm to ensure deterministic behavior when the input
// contains conflicting keys (e.g., "Bird" and "bird"): mixed-case keys
// take precedence over their lowercase counterparts.
func normalizeMapKeysToLowercase(m map[string]any) map[string]any {
	result := make(map[string]any, len(m))

	// First pass: add all already-lowercase keys
	for k, v := range m {
		if k == strings.ToLower(k) {
			result[k] = v
		}
	}

	// Second pass: add non-lowercase keys (normalized), overwriting any
	// existing lowercase versions
	for k, v := range m {
		if k != strings.ToLower(k) {
			result[strings.ToLower(k)] = v
		}
	}

	return result
}

// Helper functions

// getSettingsSectionValue returns a pointer to the requested section of settings for in-place updates
func getSettingsSectionValue(settings *conf.Settings, section string) (any, error) {
	section = strings.ToLower(section)

	// Map section names to their corresponding pointers
	switch section {
	case SettingsSectionBirdnet:
		return &settings.BirdNET, nil
	case SettingsSectionWebserver:
		return &settings.WebServer, nil
	case "security":
		return &settings.Security, nil
	case "main":
		return &settings.Main, nil
	case SettingsSectionRealtime:
		return &settings.Realtime, nil
	case SettingsSectionAudio:
		return getAudioSectionValue(settings), nil
	case "dashboard":
		return &settings.Realtime.Dashboard, nil
	case "weather":
		return &settings.Realtime.Weather, nil
	case "mqtt":
		return &settings.Realtime.MQTT, nil
	case "birdweather":
		return &settings.Realtime.Birdweather, nil
	case SettingsSectionSpecies:
		return &settings.Realtime.Species, nil
	case "rtsp":
		return &settings.Realtime.RTSP, nil
	case "privacyfilter":
		return &settings.Realtime.PrivacyFilter, nil
	case "dogbarkfilter":
		return &settings.Realtime.DogBarkFilter, nil
	case "telemetry":
		return &settings.Realtime.Telemetry, nil
	case "sentry":
		return &settings.Sentry, nil
	case "diagnostics":
		// The generated profiling token in this section is never client-settable:
		// UpdateSectionSettings reverts it via restoreBlockedFields after the
		// merge. This case was withheld until that enforcement existed, because
		// without it an unauthenticated client on a no-auth instance could set a
		// token it chose and read pprof profiles with it.
		return &settings.Diagnostics, nil
	case "notification":
		return &settings.Notification, nil
	case "logging":
		return &settings.Logging, nil
	case "alerting":
		return &settings.Alerting, nil
	case "backup":
		return &settings.Backup, nil
	case "output":
		return &settings.Output, nil
	case "perch":
		return &settings.Perch, nil
	case "birdnetv3":
		return &settings.BirdNETV3, nil
	case "bat":
		return &settings.Bat, nil
	case "models":
		return &settings.Models, nil
	case "taxonomysynonyms":
		return &settings.TaxonomySynonyms, nil
	default:
		return nil, fmt.Errorf("unknown settings section: %s", section)
	}
}

// handleGenericSection handles updates to any settings section using merging
func handleGenericSection(sectionPtr any, data json.RawMessage, sectionName string) error {
	// Normalize species config keys in the incoming data BEFORE merging
	// This ensures case-insensitive key matching during the deep merge
	normalizedData, err := normalizeSpeciesConfigKeysInJSON(data, sectionName)
	if err != nil {
		return fmt.Errorf("failed to normalize species config keys: %w", err)
	}

	// Use mergeJSONIntoStruct to preserve fields not included in the update
	if err := mergeJSONIntoStruct(normalizedData, sectionPtr); err != nil {
		return fmt.Errorf("failed to merge settings for section %s: %w", sectionName, err)
	}

	// Blocked fields (including Realtime.Species.Confirmed, which is
	// analytics-only and managed exclusively via /api/v2/detections/confirm) are
	// NOT enforced here. This function only merges; the merge deliberately
	// writes every key the request carried, and UpdateSectionSettings then calls
	// restoreBlockedFields to revert the blocked ones — Confirmed included —
	// against the pre-update snapshot. Enforcing here is not possible anyway: a
	// section name does not always map to a top-level key of getBlockedFieldMap
	// (PATCH /settings/audio targets Realtime.Audio), so a per-section lookup
	// would silently miss exactly the nested section that carries blocked
	// leaves: the audio tool paths live under the map's "Realtime" key, which a
	// lookup keyed on the section name "audio" would never find.
	//
	// Blocked-field enforcement for both write paths lives in restoreBlockedFields,
	// called after the merge by UpdateSettings (PUT) and UpdateSectionSettings
	// (PATCH). Do not reintroduce enforcement here: this function only merges.
	return nil
}

// sectionValidator is a function that validates section-specific data
type sectionValidator func(data json.RawMessage) error

// getSectionValidators returns validators for sections that need special validation
func getSectionValidators() map[string]sectionValidator {
	return map[string]sectionValidator{
		"mqtt":                   validateMQTTSection,
		"rtsp":                   validateStreamsSection,
		"security":               validateSecuritySection,
		"main":                   validateMainSection,
		SettingsSectionBirdnet:   validateBirdNETSection,
		SettingsSectionWebserver: validateWebServerSection,
		SettingsSectionSpecies:   validateSpeciesSection,
		SettingsSectionRealtime:  validateRealtimeSection,
		"notification":           validateNotificationSection,
		"alerting":               validateAlertingSection,
	}
}

// validateMQTTSection validates MQTT settings
func validateMQTTSection(data json.RawMessage) error {
	var mqttSettings conf.MQTTSettings
	if err := json.Unmarshal(data, &mqttSettings); err != nil {
		return err
	}

	// Validate MQTT settings
	if mqttSettings.Enabled && mqttSettings.Broker == "" {
		return fmt.Errorf("broker is required when MQTT is enabled")
	}

	return nil
}

// validateStreamsSection validates stream settings
func validateStreamsSection(data json.RawMessage) error {
	var rtspSettings conf.RTSPSettings
	if err := json.Unmarshal(data, &rtspSettings); err != nil {
		return err
	}

	// Apply default transport before validation (this path doesn't go through validateRealtimeSettings)
	rtspSettings.ApplyStreamDefaults()

	// Validate RTSP streams
	return rtspSettings.ValidateStreams()
}

// securitySectionAllowedFields defines which fields in the security section can be updated via API
var securitySectionAllowedFields = map[string]bool{
	"host":              true, // Server hostname for TLS
	"baseUrl":           true, // Base URL for OAuth redirects
	"autoTls":           true, // AutoTLS setting
	"basicAuth":         true, // Basic authentication settings
	"oauthProviders":    true, // New array-based OAuth provider configuration
	"googleAuth":        true, // Legacy Google OAuth settings (deprecated)
	"githubAuth":        true, // Legacy GitHub OAuth settings (deprecated)
	"microsoftAuth":     true, // Legacy Microsoft OAuth settings (deprecated)
	"allowSubnetBypass": true, // Subnet bypass settings
	"redirectToHttps":   true, // HTTPS redirect setting
	// sessionSecret is NOT allowed - it's generated internally
	// sessionDuration is NOT allowed - it's a runtime setting
}

// validateSecuritySection validates security settings
func validateSecuritySection(data json.RawMessage) error {
	var updateMap map[string]any
	if err := json.Unmarshal(data, &updateMap); err != nil {
		return err
	}

	return validateSecuritySectionValues(updateMap)
}

// validateSecuritySectionValues validates the values of security section fields
func validateSecuritySectionValues(updateMap map[string]any) error {
	// Validate host
	if err := validateHostField(updateMap); err != nil {
		return err
	}

	// Validate autoTls
	if err := validateAutoTLSField(updateMap); err != nil {
		return err
	}

	// Validate basicAuth
	if err := validateBasicAuthField(updateMap); err != nil {
		return err
	}

	// Validate new array-based OAuth providers
	if err := validateOAuthProvidersArray(updateMap); err != nil {
		return err
	}

	// Validate legacy OAuth settings (deprecated, but still supported)
	if err := validateOAuthSettings("googleAuth", updateMap); err != nil {
		return err
	}
	if err := validateOAuthSettings("githubAuth", updateMap); err != nil {
		return err
	}

	// Validate allowSubnetBypass
	if err := validateSubnetBypassField(updateMap); err != nil {
		return err
	}

	return nil
}

// validateHostField validates the host field
func validateHostField(updateMap map[string]any) error {
	host, exists := updateMap["host"]
	if !exists {
		return nil
	}

	str, ok := host.(string)
	if !ok {
		return fmt.Errorf("host must be a string")
	}

	if str != "" && len(str) > 255 {
		return fmt.Errorf("host must not exceed 255 characters")
	}

	return nil
}

// validateAutoTLSField validates the autoTls field
func validateAutoTLSField(updateMap map[string]any) error {
	autoTls, exists := updateMap["autoTls"]
	if !exists {
		return nil
	}

	if _, ok := autoTls.(bool); !ok {
		return fmt.Errorf("autoTls must be a boolean value")
	}

	return nil
}

// validateBasicAuthField validates the basicAuth field
func validateBasicAuthField(updateMap map[string]any) error {
	basicAuth, exists := updateMap["basicAuth"]
	if !exists {
		return nil
	}

	basicAuthMap, ok := basicAuth.(map[string]any)
	if !ok {
		return nil
	}

	// Validate enabled field
	if enabled, exists := basicAuthMap["enabled"]; exists {
		if _, ok := enabled.(bool); !ok {
			return fmt.Errorf("basicAuth.enabled must be a boolean")
		}
	}
	// Password complexity is validated elsewhere

	return nil
}

// validateSubnetBypassField validates the allowSubnetBypass field
func validateSubnetBypassField(updateMap map[string]any) error {
	subnetBypass, exists := updateMap["allowSubnetBypass"]
	if !exists {
		return nil
	}

	bypassMap, ok := subnetBypass.(map[string]any)
	if !ok {
		return nil
	}

	// Validate enabled field
	if enabled, exists := bypassMap["enabled"]; exists {
		if _, ok := enabled.(bool); !ok {
			return fmt.Errorf("allowSubnetBypass.enabled must be a boolean")
		}
	}

	// Validate subnet field
	if subnet, exists := bypassMap["subnet"]; exists {
		str, ok := subnet.(string)
		if !ok {
			return fmt.Errorf("subnet must be a string")
		}

		if str != "" && !strings.Contains(str, "/") {
			return fmt.Errorf("subnet must be in CIDR format (e.g., 192.168.1.0/24)")
		}
	}

	return nil
}

// validOAuthProviders defines the valid OAuth provider names
var validOAuthProviders = map[string]bool{
	"google":    true,
	"github":    true,
	"microsoft": true,
	"line":      true,
	"kakao":     true,
}

// getValidOAuthProviderNames returns sorted list of valid provider names for error messages
func getValidOAuthProviderNames() string {
	names := slices.Collect(maps.Keys(validOAuthProviders))
	slices.Sort(names)
	return strings.Join(names, ", ")
}

// validateOAuthProvidersArray validates the new array-based OAuth providers configuration
func validateOAuthProvidersArray(updateMap map[string]any) error {
	providers, exists := updateMap["oauthProviders"]
	if !exists {
		return nil
	}

	providersArray, ok := providers.([]any)
	if !ok {
		return fmt.Errorf("oauthProviders must be an array")
	}

	// Track configured providers to detect duplicates
	configuredProviders := make(map[string]bool)

	for i, item := range providersArray {
		providerName, err := validateOAuthProviderEntry(item, i, configuredProviders)
		if err != nil {
			return err
		}
		configuredProviders[providerName] = true
	}

	return nil
}

// validateOAuthProviderEntry validates a single OAuth provider entry in the array.
// Returns the provider name if valid, or an error if validation fails.
func validateOAuthProviderEntry(item any, index int, configuredProviders map[string]bool) (string, error) {
	providerMap, ok := item.(map[string]any)
	if !ok {
		return "", fmt.Errorf("oauthProviders[%d] must be an object", index)
	}

	providerName, err := validateOAuthProviderName(providerMap, index, configuredProviders)
	if err != nil {
		return "", err
	}

	if err := validateOAuthProviderEnabled(providerMap, index); err != nil {
		return "", err
	}

	return providerName, nil
}

// validateOAuthProviderName validates the provider name field and checks for duplicates.
func validateOAuthProviderName(providerMap map[string]any, index int, configuredProviders map[string]bool) (string, error) {
	providerName, ok := providerMap["provider"].(string)
	if !ok || providerName == "" {
		return "", fmt.Errorf("oauthProviders[%d].provider must be a non-empty string", index)
	}

	if !validOAuthProviders[providerName] {
		return "", fmt.Errorf("oauthProviders[%d].provider '%s' is not a valid provider (valid: %s)", index, providerName, getValidOAuthProviderNames())
	}

	if configuredProviders[providerName] {
		return "", fmt.Errorf("oauthProviders contains duplicate provider '%s'", providerName)
	}

	return providerName, nil
}

// validateOAuthProviderEnabled validates the enabled field and required credentials.
func validateOAuthProviderEnabled(providerMap map[string]any, index int) error {
	enabledVal, exists := providerMap["enabled"]
	if !exists {
		return nil
	}

	enabledBool, ok := enabledVal.(bool)
	if !ok {
		return fmt.Errorf("oauthProviders[%d].enabled must be a boolean", index)
	}

	if !enabledBool {
		return nil
	}

	// Provider is enabled, validate required fields
	if err := validateRequiredStringInProvider(providerMap, "clientId", index); err != nil {
		return err
	}
	return validateRequiredStringInProvider(providerMap, "clientSecret", index)
}

// validateRequiredStringInProvider validates a required string field in an OAuth provider config
func validateRequiredStringInProvider(providerMap map[string]any, fieldName string, index int) error {
	val, exists := providerMap[fieldName]
	if !exists {
		return fmt.Errorf("oauthProviders[%d].%s is required when enabled", index, fieldName)
	}

	str, ok := val.(string)
	if !ok {
		return fmt.Errorf("oauthProviders[%d].%s must be a string", index, fieldName)
	}

	if str == "" {
		return fmt.Errorf("oauthProviders[%d].%s cannot be empty when enabled", index, fieldName)
	}

	return nil
}

// validateOAuthSettings validates OAuth provider settings
func validateOAuthSettings(providerName string, updateMap map[string]any) error {
	provider, exists := updateMap[providerName]
	if !exists {
		return nil
	}

	providerMap, ok := provider.(map[string]any)
	if !ok {
		return fmt.Errorf("%s must be an object", providerName)
	}

	// Check enabled field
	enabled := false
	if enabledVal, exists := providerMap["enabled"]; exists {
		if enabledBool, ok := enabledVal.(bool); ok {
			enabled = enabledBool
		} else {
			return fmt.Errorf("%s.enabled must be a boolean", providerName)
		}
	}

	// If enabled, validate required fields
	if enabled {
		if err := validateRequiredStringWhenEnabled(providerMap, "clientId", providerName); err != nil {
			return err
		}
		if err := validateRequiredStringWhenEnabled(providerMap, "clientSecret", providerName); err != nil {
			return err
		}
	}

	return nil
}

// mainSectionAllowedFields defines which fields in the main section can be updated via API
var mainSectionAllowedFields = map[string]bool{
	"name":      true, // Node name is safe to update
	"timeAs24h": true, // Time format is safe to update
}

// validateMainSection validates main settings
func validateMainSection(data json.RawMessage) error {
	var updateMap map[string]any
	if err := json.Unmarshal(data, &updateMap); err != nil {
		return err
	}

	return validateMainSectionValues(updateMap)
}

// validateMainSectionValues validates the values of main section fields
func validateMainSectionValues(updateMap map[string]any) error {
	if err := validateNonEmptyString(updateMap, "name", maxNodeNameLength, "node name"); err != nil {
		return err
	}
	return validateBoolField(updateMap, "timeAs24h", "timeAs24h")
}

// validateBirdNETSection validates BirdNET settings
func validateBirdNETSection(data json.RawMessage) error {
	var updateMap map[string]any
	if err := json.Unmarshal(data, &updateMap); err != nil {
		return err
	}

	if err := validateFloatInRange(updateMap, "latitude", minLatitude, maxLatitude, "latitude"); err != nil {
		return err
	}
	if err := validateFloatInRange(updateMap, "longitude", minLongitude, maxLongitude, "longitude"); err != nil {
		return err
	}
	return validateModelRegionField(updateMap)
}

// validateModelRegionField rejects a malformed modelRegion in a PATCH/PUT
// payload. Unlike the startup validator (which warns and normalizes an aged
// config), a value submitted through the API is expected to be well-formed, so a
// bad one is a 400. "auto", "global", the empty string, and a well-formed region
// slug are accepted; an unknown but well-formed slug is allowed because the
// per-family resolver degrades it gracefully.
//
// The key match is case-insensitive on purpose: encoding/json binds struct
// fields case-insensitively, so a payload key like "modelregion" still reaches
// BirdNETConfig.ModelRegion on merge. Matching only the exact "modelRegion" key
// would let a case-variant key slip past this check and persist an unvalidated
// value.
func validateModelRegionField(updateMap map[string]any) error {
	var matched []string
	for key := range updateMap {
		if strings.EqualFold(key, "modelRegion") {
			matched = append(matched, key)
		}
	}
	switch len(matched) {
	case 0:
		return nil
	case 1:
		// single key, validated below
	default:
		// More than one case-variant of the key (e.g. "modelRegion" and
		// "modelregion"). json binds one of them ambiguously on merge, so reject
		// the payload rather than silently pick one.
		return fmt.Errorf("modelRegion specified multiple times with different key casing")
	}
	raw := updateMap[matched[0]]
	if raw == nil {
		return nil
	}
	value, ok := raw.(string)
	if !ok {
		return fmt.Errorf("modelRegion must be a string")
	}
	if value == "" || value == conf.ModelRegionAuto || value == conf.ModelRegionGlobal ||
		conf.ModelRegionSlugPattern.MatchString(value) {
		return nil
	}
	return fmt.Errorf("modelRegion must be 'auto', 'global', or a region slug")
}

// validateWebServerSection validates WebServer settings including LiveStream fields.
func validateWebServerSection(data json.RawMessage) error {
	var updateMap map[string]any
	if err := json.Unmarshal(data, &updateMap); err != nil {
		return err
	}

	if err := validatePortField(updateMap, "port"); err != nil {
		return err
	}

	return validateLiveStreamFields(updateMap)
}

// validateLiveStreamFields validates LiveStream sub-fields if the liveStream
// key is present in the PATCH payload.
func validateLiveStreamFields(updateMap map[string]any) error {
	lsRaw, ok := updateMap["liveStream"]
	if !ok || lsRaw == nil {
		return nil
	}
	ls, ok := lsRaw.(map[string]any)
	if !ok {
		return fmt.Errorf("liveStream must be an object")
	}

	if err := validateFloatInRange(ls, "bitRate",
		float64(conf.MinLiveStreamBitRate), float64(conf.MaxLiveStreamBitRate),
		"LiveStream bitrate (kbps)"); err != nil {
		return err
	}
	if err := validateFloatInRange(ls, "segmentLength",
		float64(conf.MinLiveStreamSegmentLength), float64(conf.MaxLiveStreamSegmentLength),
		"LiveStream segment length (seconds)"); err != nil {
		return err
	}
	return validateFloatInRange(ls, "sampleRate",
		float64(conf.MinLiveStreamSampleRate), float64(conf.MaxLiveStreamSampleRate),
		"LiveStream sample rate (Hz)")
}

// validateSpeciesSection validates species settings
func validateSpeciesSection(data json.RawMessage) error {
	var speciesSettings conf.SpeciesSettings
	if err := json.Unmarshal(data, &speciesSettings); err != nil {
		return err
	}

	// Call the existing species config validation from the conf package
	// This will validate threshold range (0-1) and interval (>= 0)
	for speciesName, config := range speciesSettings.Config {
		// Check if interval is non-negative
		if config.Interval < 0 {
			return fmt.Errorf("species config for '%s': interval must be non-negative, got %d", speciesName, config.Interval)
		}

		// Check if threshold is within valid range
		if config.Threshold < 0 || config.Threshold > 1 {
			return fmt.Errorf("species config for '%s': threshold must be between 0 and 1, got %f", speciesName, config.Threshold)
		}
	}

	return nil
}

// validateRealtimeSection validates realtime settings that contain species
func validateRealtimeSection(data json.RawMessage) error {
	var realtimeSettings conf.RealtimeSettings
	if err := json.Unmarshal(data, &realtimeSettings); err != nil {
		return err
	}

	// Validate species config if present
	for speciesName, config := range realtimeSettings.Species.Config {
		// Check if interval is non-negative
		if config.Interval < 0 {
			return fmt.Errorf("species config for '%s': interval must be non-negative, got %d", speciesName, config.Interval)
		}

		// Check if threshold is within valid range
		if config.Threshold < 0 || config.Threshold > 1 {
			return fmt.Errorf("species config for '%s': threshold must be between 0 and 1, got %f", speciesName, config.Threshold)
		}
	}

	return nil
}

// validateNotificationSection validates notification settings including template syntax
func validateNotificationSection(data json.RawMessage) error {
	var notificationConfig conf.NotificationConfig
	if err := json.Unmarshal(data, &notificationConfig); err != nil {
		return err
	}

	// Validate new species notification templates if present
	if notificationConfig.Templates.NewSpecies.Title != "" {
		if _, err := template.New("title").Funcs(notification.TemplateFuncs).Parse(notificationConfig.Templates.NewSpecies.Title); err != nil {
			return fmt.Errorf("invalid template syntax in new species title: %w", err)
		}
	}

	if notificationConfig.Templates.NewSpecies.Message != "" {
		if _, err := template.New("message").Funcs(notification.TemplateFuncs).Parse(notificationConfig.Templates.NewSpecies.Message); err != nil {
			return fmt.Errorf("invalid template syntax in new species message: %w", err)
		}
	}

	return nil
}

// validateAlertingSection validates alerting settings
func validateAlertingSection(data json.RawMessage) error {
	var alertSettings conf.AlertSettings
	if err := json.Unmarshal(data, &alertSettings); err != nil {
		return err
	}

	if alertSettings.HistoryRetentionDays < 0 {
		return fmt.Errorf("historyRetentionDays must be non-negative, got %d", alertSettings.HistoryRetentionDays)
	}

	return nil
}

// getSettingsSection returns the requested section of settings
func getSettingsSection(settings *conf.Settings, section string) (any, error) {
	section = strings.ToLower(section)

	// Use reflection to get the field
	settingsValue := reflect.ValueOf(settings).Elem()
	settingsType := settingsValue.Type()

	// Check direct fields first
	//nolint:gocritic // Need index i for settingsValue.Field(i) call
	for i := range settingsType.NumField() {
		field := settingsType.Field(i)
		if strings.EqualFold(field.Name, section) {
			return settingsValue.Field(i).Interface(), nil
		}
	}

	// Check nested fields
	switch section {
	case SettingsSectionBirdnet:
		return settings.BirdNET, nil
	case SettingsSectionWebserver:
		return settings.WebServer, nil
	case "security":
		return settings.Security, nil
	case "main":
		return settings.Main, nil
	case SettingsSectionRealtime:
		return settings.Realtime, nil
	case SettingsSectionAudio:
		return getAudioSection(settings), nil
	case "dashboard":
		return settings.Realtime.Dashboard, nil
	case "weather":
		return settings.Realtime.Weather, nil
	case "mqtt":
		return settings.Realtime.MQTT, nil
	case "birdweather":
		return settings.Realtime.Birdweather, nil
	case SettingsSectionSpecies:
		return settings.Realtime.Species, nil
	default:
		return nil, fmt.Errorf("unknown settings section: %s", section)
	}
}

// Coordinate validation constants, used by validateFloatInRange for the birdnet
// section's latitude/longitude bounds.
const (
	minLatitude  = -90
	maxLatitude  = 90
	minLongitude = -180
	maxLongitude = 180
)

// redactedValue is the placeholder used for secret fields in API responses.
// The frontend can check for this value to show a "secret is set" indicator.
// It aliases apicore.RedactedValue (the shared substrate sentinel) so the
// settings save flow and the integrations test-connection handlers match the
// exact same value and cannot drift apart.
const redactedValue = apicore.RedactedValue

// sanitizeSettingsForAPI returns a shallow copy of Settings with all secret
// fields replaced by a redacted placeholder. This prevents the GET endpoints
// from leaking credentials, session secrets, API keys, and other sensitive
// data. The original Settings struct is never modified.
func sanitizeSettingsForAPI(s *conf.Settings) *conf.Settings {
	// Shallow-copy the top-level struct (value copy of all non-pointer fields).
	sanitized := *s

	// --- Security section ---
	sanitized.Security.SessionSecret = redactedValue
	sanitized.Security.BasicAuth.Password = redact(s.Security.BasicAuth.Password)
	sanitized.Security.BasicAuth.ClientID = ""
	sanitized.Security.BasicAuth.ClientSecret = ""

	// --- Diagnostics section ---
	sanitized.Diagnostics.Profiling.Token = redact(s.Diagnostics.Profiling.Token)

	// Legacy OAuth providers
	sanitized.Security.GoogleAuth.ClientSecret = redact(s.Security.GoogleAuth.ClientSecret)
	sanitized.Security.GithubAuth.ClientSecret = redact(s.Security.GithubAuth.ClientSecret)
	sanitized.Security.MicrosoftAuth.ClientSecret = redact(s.Security.MicrosoftAuth.ClientSecret)

	// Array-based OAuth providers — must copy the slice to avoid mutating the original
	if len(s.Security.OAuthProviders) > 0 {
		providers := make([]conf.OAuthProviderConfig, len(s.Security.OAuthProviders))
		sanitized.Security.OAuthProviders = providers
		for i := range s.Security.OAuthProviders {
			p := s.Security.OAuthProviders[i]
			p.ClientSecret = redact(p.ClientSecret)
			providers[i] = p
		}
	}

	// --- MQTT ---
	sanitized.Realtime.MQTT.Password = redact(s.Realtime.MQTT.Password)

	// --- Database ---
	sanitized.Output.MySQL.Password = redact(s.Output.MySQL.Password)

	// --- Weather API keys ---
	sanitized.Realtime.Weather.OpenWeather.APIKey = redact(s.Realtime.Weather.OpenWeather.APIKey)
	sanitized.Realtime.Weather.Wunderground.APIKey = redact(s.Realtime.Weather.Wunderground.APIKey)

	// --- eBird API key ---
	sanitized.Realtime.EBird.APIKey = redact(s.Realtime.EBird.APIKey)

	// Backup targets may contain FTP/SFTP/S3 credentials in their Settings map.
	// Copy the slice and redact known secret keys.
	if len(s.Backup.Targets) > 0 {
		targets := make([]conf.BackupTarget, len(s.Backup.Targets))
		sanitized.Backup.Targets = targets
		for i, t := range s.Backup.Targets {
			if t.Settings != nil {
				sanitizedSettings := make(map[string]any, len(t.Settings))
				for k, v := range t.Settings {
					switch k {
					case "password", "secretaccesskey":
						if str, ok := v.(string); ok && str != "" {
							sanitizedSettings[k] = redactedValue
						} else {
							sanitizedSettings[k] = v
						}
					default:
						sanitizedSettings[k] = v
					}
				}
				t.Settings = sanitizedSettings
			}
			targets[i] = t
		}
	}

	// --- Notification webhook auth secrets ---
	sanitizeNotificationSecrets(&sanitized)

	return &sanitized
}

// sanitizeNotificationSecrets redacts auth credentials in push notification
// webhook endpoints. The copy's Notification field is modified in place.
func sanitizeNotificationSecrets(s *conf.Settings) {
	providers := s.Notification.Push.Providers
	if len(providers) == 0 {
		return
	}
	// Copy the providers slice to avoid mutating the original
	providersCopy := make([]conf.PushProviderConfig, len(providers))
	for i := range providers {
		p := providers[i]
		if len(p.Endpoints) > 0 {
			endpoints := make([]conf.WebhookEndpointConfig, len(p.Endpoints))
			for j := range p.Endpoints {
				ep := p.Endpoints[j]
				ep.Auth.Token = redact(ep.Auth.Token)
				ep.Auth.Pass = redact(ep.Auth.Pass)
				ep.Auth.Value = redact(ep.Auth.Value)
				endpoints[j] = ep
			}
			p.Endpoints = endpoints
		}
		providersCopy[i] = p
	}
	s.Notification.Push.Providers = providersCopy
}

// restoreRedactedSecrets replaces redacted placeholder values in the incoming
// settings with the current (real) values so that an update round-trip
// (GET → modify → PUT) does not overwrite real secrets with the placeholder.
//
// After restoring all fields, it validates that no sentinel values remain.
// A remaining sentinel means the user changed a lookup key (e.g. provider
// name, endpoint URL, or backup target type) while the auth field was still
// showing the redacted placeholder, so the restore could not match it.
// In that case the offending field is cleared to the empty string and an
// error is returned listing all affected fields.
func restoreRedactedSecrets(current, incoming *conf.Settings) error {
	restore := func(cur, inc *string) {
		apicore.RestoreRedactedSecret(*cur, inc)
	}

	// Security — defense-in-depth: restore even though SessionSecret is
	// also in the blocked field map (protects against future unblocking).
	restore(&current.Security.SessionSecret, &incoming.Security.SessionSecret)
	restore(&current.Security.BasicAuth.Password, &incoming.Security.BasicAuth.Password)
	restore(&current.Security.GoogleAuth.ClientSecret, &incoming.Security.GoogleAuth.ClientSecret)
	restore(&current.Security.GithubAuth.ClientSecret, &incoming.Security.GithubAuth.ClientSecret)
	restore(&current.Security.MicrosoftAuth.ClientSecret, &incoming.Security.MicrosoftAuth.ClientSecret)

	// Diagnostics
	restore(&current.Diagnostics.Profiling.Token, &incoming.Diagnostics.Profiling.Token)

	// Array-based OAuth providers — match by Provider name to handle reordering
	for i := range incoming.Security.OAuthProviders {
		if incoming.Security.OAuthProviders[i].ClientSecret != redactedValue {
			continue
		}
		for j := range current.Security.OAuthProviders {
			if current.Security.OAuthProviders[j].Provider == incoming.Security.OAuthProviders[i].Provider {
				incoming.Security.OAuthProviders[i].ClientSecret = current.Security.OAuthProviders[j].ClientSecret
				break
			}
		}
	}

	// MQTT
	restore(&current.Realtime.MQTT.Password, &incoming.Realtime.MQTT.Password)

	// MySQL
	restore(&current.Output.MySQL.Password, &incoming.Output.MySQL.Password)

	// Weather API keys
	restore(&current.Realtime.Weather.OpenWeather.APIKey, &incoming.Realtime.Weather.OpenWeather.APIKey)
	restore(&current.Realtime.Weather.Wunderground.APIKey, &incoming.Realtime.Weather.Wunderground.APIKey)

	// eBird
	restore(&current.Realtime.EBird.APIKey, &incoming.Realtime.EBird.APIKey)

	// Backup target secrets, match by Type to handle reordering
	for i := range incoming.Backup.Targets {
		if incoming.Backup.Targets[i].Settings == nil {
			continue
		}
		// Find the matching current target by type
		var curSettings map[string]any
		for j := range current.Backup.Targets {
			if current.Backup.Targets[j].Type == incoming.Backup.Targets[i].Type {
				curSettings = current.Backup.Targets[j].Settings
				break
			}
		}
		if curSettings == nil {
			continue
		}
		for _, key := range []string{"password", "secretaccesskey"} {
			if v, ok := incoming.Backup.Targets[i].Settings[key]; ok {
				if str, isStr := v.(string); isStr && str == redactedValue {
					incoming.Backup.Targets[i].Settings[key] = curSettings[key]
				}
			}
		}
	}

	// Webhook auth secrets — match by provider Name + endpoint URL to handle reordering
	// Build a map of current providers keyed by Name for O(1) lookup.
	curProvidersByName := make(map[string]*conf.PushProviderConfig, len(current.Notification.Push.Providers))
	for i := range current.Notification.Push.Providers {
		curProvidersByName[current.Notification.Push.Providers[i].Name] = &current.Notification.Push.Providers[i]
	}

	for i := range incoming.Notification.Push.Providers {
		curProvider, ok := curProvidersByName[incoming.Notification.Push.Providers[i].Name]
		if !ok {
			continue
		}
		// Build a map of current endpoints keyed by URL for this provider.
		curEndpointsByURL := make(map[string]*conf.WebhookEndpointConfig, len(curProvider.Endpoints))
		for j := range curProvider.Endpoints {
			curEndpointsByURL[curProvider.Endpoints[j].URL] = &curProvider.Endpoints[j]
		}

		for j := range incoming.Notification.Push.Providers[i].Endpoints {
			curEP, found := curEndpointsByURL[incoming.Notification.Push.Providers[i].Endpoints[j].URL]
			if !found {
				continue
			}
			incAuth := &incoming.Notification.Push.Providers[i].Endpoints[j].Auth
			restore(&curEP.Auth.Token, &incAuth.Token)
			restore(&curEP.Auth.Pass, &incAuth.Pass)
			restore(&curEP.Auth.Value, &incAuth.Value)
		}
	}

	// Validate that no redacted sentinels remain after restore.
	return validateNoRedactedSentinels(incoming)
}

// validateNoRedactedSentinels scans all secret fields in the settings for
// leftover redacted sentinel values. Any field still containing the sentinel
// after restoreRedactedSecrets means the restore lookup failed (the user
// changed a lookup key like provider name, endpoint URL, or backup type
// while the auth was still redacted). Such fields are cleared to the empty
// string to prevent persisting the sentinel literal, and an error listing
// all affected fields is returned.
func validateNoRedactedSentinels(s *conf.Settings) error {
	var stale []string

	check := func(field, path string) {
		if field == redactedValue {
			stale = append(stale, path)
		}
	}

	// Scalar secret fields — these always have a 1:1 restore and should
	// never remain as sentinel, but check defensively.
	check(s.Security.SessionSecret, "security.sessionSecret")
	check(s.Security.BasicAuth.Password, "security.basicAuth.password")
	check(s.Security.GoogleAuth.ClientSecret, "security.googleAuth.clientSecret")
	check(s.Security.GithubAuth.ClientSecret, "security.githubAuth.clientSecret")
	check(s.Security.MicrosoftAuth.ClientSecret, "security.microsoftAuth.clientSecret")
	check(s.Diagnostics.Profiling.Token, "diagnostics.profiling.token")
	check(s.Realtime.MQTT.Password, "realtime.mqtt.password")
	check(s.Output.MySQL.Password, "output.mysql.password")
	check(s.Realtime.Weather.OpenWeather.APIKey, "realtime.weather.openWeather.apiKey")
	check(s.Realtime.Weather.Wunderground.APIKey, "realtime.weather.wunderground.apiKey")
	check(s.Realtime.EBird.APIKey, "realtime.ebird.apiKey")

	// Array-based OAuth providers
	for i := range s.Security.OAuthProviders {
		p := &s.Security.OAuthProviders[i]
		check(p.ClientSecret, fmt.Sprintf("security.oauthProviders[%d(%s)].clientSecret", i, p.Provider))
	}

	// Backup target secrets
	for i := range s.Backup.Targets {
		t := &s.Backup.Targets[i]
		if t.Settings == nil {
			continue
		}
		for _, key := range []string{"password", "secretaccesskey"} {
			if v, ok := t.Settings[key]; ok {
				if str, isStr := v.(string); isStr {
					check(str, fmt.Sprintf("backup.targets[%d(%s)].settings.%s", i, t.Type, key))
				}
			}
		}
	}

	// Webhook auth secrets
	for i := range s.Notification.Push.Providers {
		prov := &s.Notification.Push.Providers[i]
		for j := range prov.Endpoints {
			ep := &prov.Endpoints[j]
			prefix := fmt.Sprintf("notification.push.providers[%d].endpoints[%d].auth", i, j)
			check(ep.Auth.Token, prefix+".token")
			check(ep.Auth.Pass, prefix+".pass")
			check(ep.Auth.Value, prefix+".value")
		}
	}

	if len(stale) == 0 {
		return nil
	}

	// Clear all stale sentinel values to prevent persisting the literal.
	clearRedactedSentinels(s)

	return fmt.Errorf("cannot save settings: %d secret field(s) contain the redacted placeholder "+
		"because the identifying key (provider name, endpoint URL, or target type) was changed "+
		"while the secret was hidden; re-enter the secret value for: %s",
		len(stale), strings.Join(stale, ", "))
}

// clearRedactedSentinels replaces any remaining redacted sentinel values
// with empty strings so they are never persisted to disk.
func clearRedactedSentinels(s *conf.Settings) {
	clearField := func(field *string) {
		if *field == redactedValue {
			*field = ""
		}
	}

	clearField(&s.Security.SessionSecret)
	clearField(&s.Security.BasicAuth.Password)
	clearField(&s.Security.GoogleAuth.ClientSecret)
	clearField(&s.Security.GithubAuth.ClientSecret)
	clearField(&s.Security.MicrosoftAuth.ClientSecret)
	clearField(&s.Diagnostics.Profiling.Token)
	clearField(&s.Realtime.MQTT.Password)
	clearField(&s.Output.MySQL.Password)
	clearField(&s.Realtime.Weather.OpenWeather.APIKey)
	clearField(&s.Realtime.Weather.Wunderground.APIKey)
	clearField(&s.Realtime.EBird.APIKey)

	for i := range s.Security.OAuthProviders {
		clearField(&s.Security.OAuthProviders[i].ClientSecret)
	}

	for i := range s.Backup.Targets {
		if s.Backup.Targets[i].Settings == nil {
			continue
		}
		for _, key := range []string{"password", "secretaccesskey"} {
			if v, ok := s.Backup.Targets[i].Settings[key]; ok {
				if str, isStr := v.(string); isStr && str == redactedValue {
					s.Backup.Targets[i].Settings[key] = ""
				}
			}
		}
	}

	for i := range s.Notification.Push.Providers {
		for j := range s.Notification.Push.Providers[i].Endpoints {
			auth := &s.Notification.Push.Providers[i].Endpoints[j].Auth
			clearField(&auth.Token)
			clearField(&auth.Pass)
			clearField(&auth.Value)
		}
	}
}

// redact returns the redacted placeholder if the input is non-empty,
// or an empty string if the field was never set. This lets the frontend
// distinguish "secret is configured" from "no secret set".
func redact(s string) string {
	if s != "" {
		return redactedValue
	}
	return ""
}

// getBlockedFieldMap returns a map of fields that are BLOCKED from being updated
// THROUGH THE SETTINGS API. It governs the two request-driven write paths only;
// server-side writers (the range filter rebuild, the model manager) set these
// fields directly and are not subject to it.
//
// Using BLACKLIST approach - all fields are allowed by default except the fields
// listed here.
//
// IMPORTANT: Only add fields here if they pose a security risk.
//
// A yaml:"-" tag is NOT an equivalent protection: a field with yaml:"-" but a live
// json tag is merged from the request like any other on both write paths, so it
// must be in this map (or re-derived downstream) to be safe. Both PUT and PATCH now
// merge the request and enforce this map alone. Any runtime-only field that must
// not be client-settable on BOTH paths belongs in this map, whatever its yaml tag.
// TestPatchYamlDashFieldsAreEitherBlockedOrRederived pins the current set so a new
// one cannot slip in unnoticed.
func getBlockedFieldMap() map[string]any {
	return map[string]any{
		// Block these top-level runtime fields
		"Version":            true, // Runtime version info
		"BuildDate":          true, // Build time info
		"SystemID":           true, // Unique system identifier
		"ValidationWarnings": true, // Runtime validation state
		"Input":              true, // File/directory analysis mode config

		// BirdNET section - block runtime fields
		"BirdNET": map[string]any{
			"Labels": true, // Runtime list populated from label file
			// Block RangeFilter runtime fields
			"RangeFilter": map[string]any{
				"Model":       true, // Model type is configured in config.yaml, frontend should not overwrite
				"Species":     true, // Runtime species list populated by range filter
				"LastUpdated": true, // Runtime timestamp of last filter update
			},
		},

		// Security section - block runtime/internal fields only
		"Security": map[string]any{
			"SessionSecret":   true, // Generated internally, never updated via API
			"SessionDuration": true, // Runtime setting
			// Note: The following OAuth2 server internal fields are in BasicAuth struct
			"BasicAuth": map[string]any{
				"ClientID":       true, // OAuth2 server internal field
				"ClientSecret":   true, // OAuth2 server internal field (different from user's password)
				"AuthCodeExp":    true, // OAuth2 server internal field
				"AccessTokenExp": true, // OAuth2 server internal field
			},
		},

		// Diagnostics section - block the generated credential, same class as
		// Security.SessionSecret above. Enabled stays writable; the token is
		// minted server-side when the config is loaded, so letting a client
		// pin it to a chosen value would only weaken it. This matters most in
		// the configuration the token exists for: with no auth provider the
		// settings API has no auth either, so an unblocked field would let any
		// LAN client set a token it knows and then read profiles.
		"Diagnostics": map[string]any{
			"Profiling": map[string]any{
				"Token": true, // Generated internally, never updated via API
			},
		},

		// Realtime section - block runtime fields
		"Realtime": map[string]any{
			"Audio": getAudioBlockedFields(),
			// Species.Confirmed is analytics-only and managed exclusively via
			// /api/v2/detections/confirm. Block it so a Settings save (which omits
			// it) cannot clobber the confirmed list.
			"Species": map[string]any{
				"Confirmed": true,
			},
		},

		// All other fields are allowed by default
	}
}

// restoreBlockedFields reverts every field getBlockedFieldMap marks as
// never-updatable-via-API back to its value in current. It reverts them all, and
// returns only the subset whose value had actually changed (sorted, so the
// response is stable across map iteration order), so an empty result means the
// client changed nothing blocked rather than that nothing was written.
//
// This is what ENFORCES the map on the PATCH path. PATCH merges the incoming
// JSON straight into the section struct (handleGenericSection ->
// mergeJSONIntoStruct), so by the time control reaches here a blocked field can
// already hold a client-supplied value; restoring from the pre-update snapshot
// is what undoes that. It is deliberately a sibling of restoreRedactedSecrets,
// which sits on this same path and also restores from the snapshot after a
// merge.
//
// Both write paths call this. UpdateSettings (PUT) and UpdateSectionSettings
// (PATCH) each merge the request into the clone and then call restoreBlockedFields
// to revert the blocked leaves, so enforcement and reporting are identical on both
// verbs: the returned list names only the blocked paths whose value the request
// actually changed, and is [] when none did. TestPutCannotChangeBlockedFields and
// TestPatchCannotChangeBlockedFields pin the two entry points;
// TestRestoreBlockedFieldsCoversEveryLeaf pins the leaves.
func restoreBlockedFields(current, updated *conf.Settings) []string {
	// Non-nil so the response carries [] rather than null when nothing was
	// rejected, matching what the endpoint documentation promises.
	restored := []string{}
	restoreBlockedFieldsRecursively(
		reflect.ValueOf(current).Elem(),
		reflect.ValueOf(updated).Elem(),
		getBlockedFieldMap(),
		&restored,
		"",
	)
	slices.Sort(restored)
	return restored
}

// restoreBlockedFieldsRecursively walks the blocked map rather than the struct,
// so its cost is the size of the map and not the size of conf.Settings.
func restoreBlockedFieldsRecursively(
	currentValue, updatedValue reflect.Value,
	blockedFields map[string]any,
	restored *[]string,
	prefix string,
) {
	if currentValue.Kind() != reflect.Struct || updatedValue.Kind() != reflect.Struct {
		return
	}

	for fieldName, rule := range blockedFields {
		currentField := currentValue.FieldByName(fieldName)
		updatedField := updatedValue.FieldByName(fieldName)
		// A map entry naming a field that no longer exists (renamed or removed)
		// must not panic a live request. TestBlockedFieldMapNamesRealFields is
		// what turns such an entry into a test failure instead.
		if !currentField.IsValid() || !updatedField.IsValid() || !currentField.CanInterface() {
			continue
		}

		fieldPath := fieldName
		if prefix != "" {
			fieldPath = prefix + "." + fieldName
		}

		switch rule := rule.(type) {
		case bool:
			if rule {
				restoreBlockedLeaf(currentField, updatedField, fieldPath, restored)
			}
		case map[string]any:
			restoreBlockedSubtree(currentField, updatedField, rule, fieldPath, restored)
		}
	}
}

// restoreBlockedLeaf reverts one blocked leaf and records the path when the
// value actually differed.
//
// The restore is UNCONDITIONAL; only the reporting consults blockedValuesEqual.
// That split is deliberate: enforcement must not depend on a comparison being
// right. An earlier revision restored only when the values compared unequal, so
// every gap in blockedValuesEqual was a bypass, and it had one: time.Time.Equal
// ignores Location, so a client could resend BirdNET.RangeFilter.LastUpdated as
// the same instant in a different offset, have it compare "unchanged", and shift
// the calendar day conf.LocalNoon derives from it. Writing current's value back
// every time makes a comparison bug cost an inaccurate skippedFields list rather
// than a leaked write.
func restoreBlockedLeaf(currentField, updatedField reflect.Value, fieldPath string, restored *[]string) {
	if !updatedField.CanSet() {
		return
	}
	if !blockedValuesEqual(currentField, updatedField) {
		*restored = append(*restored, fieldPath)
	}
	updatedField.Set(defensiveCopy(currentField))
}

// blockedValuesEqual reports whether a blocked leaf came through the merge
// unchanged. It decides only whether the path is REPORTED in skippedFields (and
// logged); restoreBlockedLeaf reverts the value either way.
//
// Two kinds need more than reflect.DeepEqual, both because the PATCH merge
// round-trips the section through JSON and JSON cannot carry everything a Go
// value holds:
//
//   - time.Time is compared by instant, with Equal. DeepEqual sees the monotonic
//     clock reading that time.Now() attaches, and the Location, neither of which
//     survives the round trip intact. Comparing the Location is specifically
//     wrong: at UTC offset 0 the value marshals as "...Z" and parses back to
//     time.UTC, which is a different *Location from the time.Local that
//     time.Now() attached even when the local zone IS UTC, so a Location-
//     sensitive comparison reports a phantom rejection on every request touching
//     the section. That fires wherever the local zone sits at offset 0: a bare
//     container or TZ=UTC, Reykjavik and Abidjan year round, London, Dublin and
//     Lisbon every winter. BirdNET.RangeFilter.LastUpdated is the live case
//     (conf.UpdateIncludedSpecies sets it from time.Now()).
//
//     Comparing by instant costs no enforcement: a client resending the same
//     instant in a different offset still has the whole value, Location
//     included, overwritten from the snapshot, because restoreBlockedLeaf
//     restores unconditionally. It is only left out of skippedFields, which is
//     correct, since the instant it asked for is the instant it already had.
//     The one thing it does cost is a log line: a client deliberately probing
//     with a shifted offset no longer trips the blocked-field warning, because
//     that fires on the reported list. The write is still prevented.
//
//   - A nil slice and an empty non-nil slice are the same JSON (`[]`, or absent
//     under omitempty) but differ under DeepEqual. BirdNET.RangeFilter.Species is
//     the live case: a range filter admitting zero species leaves a non-nil empty
//     slice (conf.UpdateIncludedSpecies allocates with make), which the merge
//     turns back into nil. A nil-vs-empty MAP is the same asymmetry and is NOT
//     covered here, because no blocked leaf is a map today; add an arm before
//     adding one (conf.RangeFilterSettings.IncludedScientificNames is the
//     candidate, kept out of reach today only by json:"-").
func blockedValuesEqual(currentField, updatedField reflect.Value) bool {
	if cur, ok := reflect.TypeAssert[time.Time](currentField); ok {
		if upd, isTime := reflect.TypeAssert[time.Time](updatedField); isTime {
			return cur.Equal(upd)
		}
	}
	if currentField.Kind() == reflect.Slice && updatedField.Kind() == reflect.Slice &&
		currentField.Len() == 0 && updatedField.Len() == 0 {
		return true
	}
	return reflect.DeepEqual(currentField.Interface(), updatedField.Interface())
}

// defensiveCopy returns a value safe to store in the snapshot that is about to
// be published. A slice or map at the TOP level of a blocked leaf is copied
// rather than shared: conf.CloneSettings deliberately gives the clone its own
// backing storage, and restoring a blocked field must not quietly re-alias it to
// the outgoing snapshot. The copy is shallow, matching the slices.Clone /
// maps.Clone that CloneSettings uses.
//
// Scope worth knowing before adding a leaf: a struct-typed leaf falls through to
// the default arm and is copied by assignment, so any slice or map INSIDE it
// stays shared with the outgoing snapshot. That is safe for the only such leaf
// today (Input, three scalars) and would need revisiting for a struct leaf
// holding reference types.
func defensiveCopy(v reflect.Value) reflect.Value {
	switch v.Kind() {
	case reflect.Slice:
		if v.IsNil() {
			return v
		}
		dst := reflect.MakeSlice(v.Type(), v.Len(), v.Len())
		reflect.Copy(dst, v)
		return dst
	case reflect.Map:
		if v.IsNil() {
			return v
		}
		dst := reflect.MakeMapWithSize(v.Type(), v.Len())
		for iter := v.MapRange(); iter.Next(); {
			dst.SetMapIndex(iter.Key(), iter.Value())
		}
		return dst
	default:
		return v
	}
}

// restoreBlockedSubtree descends into a struct that carries blocked leaves.
//
// No blocked path crosses a pointer today: every node the walk descends through
// is a value struct. The pointer arms exist so that turning any one of them into
// a pointer later cannot silently disable enforcement for everything underneath
// it. They are unreachable through restoreBlockedFields, so
// TestRestoreBlockedFieldsHandlesPointerSubtrees drives them directly.
func restoreBlockedSubtree(
	currentField, updatedField reflect.Value,
	blockedFields map[string]any,
	fieldPath string,
	restored *[]string,
) {
	if currentField.Kind() != reflect.Pointer {
		restoreBlockedFieldsRecursively(currentField, updatedField, blockedFields, restored, fieldPath)
		return
	}

	switch {
	case !updatedField.IsNil():
		// A nil current means every blocked leaf under it is the zero value, and
		// zeroing them in updated is the correct restore. Read from a throwaway
		// zero struct so current is never allocated into.
		source := currentField
		if source.IsNil() {
			source = reflect.New(currentField.Type().Elem())
		}
		restoreBlockedFieldsRecursively(source.Elem(), updatedField.Elem(), blockedFields, restored, fieldPath)

	case !currentField.IsNil() && updatedField.CanSet():
		// The client nulled a whole struct holding blocked leaves. Rebuild just
		// those leaves, and publish the rebuild only when it carries something, so
		// a no-op restore cannot turn a nil pointer into a non-nil one.
		//
		// The predicate is whether the rebuild came out non-zero, NOT whether
		// anything was reported. Those differ now that restoreBlockedLeaf restores
		// unconditionally and reports only on difference: a blocked leaf that
		// compares equal is still restored, and gating the write on the report
		// count would let a client delete a struct holding blocked fields by
		// nulling it whenever every leaf under it happened to compare equal.
		rebuilt := reflect.New(updatedField.Type().Elem())
		restoreBlockedFieldsRecursively(currentField.Elem(), rebuilt.Elem(), blockedFields, restored, fieldPath)
		if !rebuilt.Elem().IsZero() {
			updatedField.Set(rebuilt)
		}
	}
}

// settingsChangeCheck defines a settings change detector with its associated action and notification.
type settingsChangeCheck struct {
	name     string                                 // Human-readable name for logging
	action   string                                 // Control action to trigger (empty = notify only)
	changed  func(old, current *conf.Settings) bool // Function to detect if settings changed
	toast    string                                 // Toast message to display (English fallback)
	toastKey string                                 // i18n translation key for the toast message
	toastTyp string                                 // Toast type: "info" or "warning"
	duration int                                    // Toast duration in milliseconds
}

// Restart-reason i18n keys recorded via restart.MarkRestartRequired when a
// restart-requiring setting changes. The frontend RestartBanner resolves these
// via t(). Named constants to avoid magic strings.
const (
	reasonWebserverRestart = "restart.reasons.webserver"
	reasonDatabaseRestart  = "restart.reasons.database"
	reasonLoggingRestart   = "restart.reasons.logging"
	reasonOAuthRestart     = "restart.reasons.oauth"
)

// settingsChangeChecks defines the settings change detectors that dispatch an
// action or a toast, in order of execution. Each check has a detection
// function, action to trigger, and toast notification.
//
// It is not quite every detector: profilingRatesChanged is applied directly by
// handleSettingsChanges instead, because the runtime setters it drives are
// atomic stores that cannot fail and so need neither the controlChan queue nor
// a toast. Anyone adding a diagnostics setting will look here first, hence this
// pointer; the reasoning is at that call site.
var settingsChangeChecks = []settingsChangeCheck{
	{"BirdNET", "reload_birdnet", birdnetSettingsChanged, "Reloading BirdNET model with new settings...", notification.MsgSettingsReloadingBirdnet, ToastTypeInfo, toastDurationLong},
	{"Analysis overlap", actionRestartAudioCapture, analysisOverlapChanged, "Restarting audio capture to apply new overlap...", "", ToastTypeInfo, toastDurationMedium},
	{"Range filter", "rebuild_range_filter", rangeFilterSettingsChanged, "Rebuilding species range filter...", notification.MsgSettingsRebuildingRangeFilter, ToastTypeInfo, toastDurationMedium},
	{"Species interval", "update_detection_intervals", intervalSettingsChanged, "Updating detection intervals...", notification.MsgSettingsUpdatingIntervals, ToastTypeInfo, toastDurationShort},
	{"Dynamic thresholds", "reconfigure_dynamic_thresholds", dynamicThresholdEnabledChanged, "Reconfiguring dynamic thresholds...", notification.MsgSettingsReconfiguringDynamicThresholds, ToastTypeInfo, toastDurationMedium},
	{"MQTT", "reconfigure_mqtt", mqttSettingsChanged, "Reconfiguring MQTT connection...", notification.MsgSettingsReconfiguringMqtt, ToastTypeInfo, toastDurationMedium},
	{"BirdWeather", "reconfigure_birdweather", birdWeatherSettingsChanged, "Reconfiguring BirdWeather integration...", notification.MsgSettingsReconfiguringBirdweather, ToastTypeInfo, toastDurationMedium},
	{"eBird", "reconfigure_ebird", ebirdSettingsChanged, "Reconfiguring eBird integration...", notification.MsgSettingsReconfiguringEbird, ToastTypeInfo, toastDurationMedium},
	{"Streams", "reconfigure_rtsp_sources", streamsSettingsChanged, "Reconfiguring audio streams...", notification.MsgSettingsReconfiguringStreams, ToastTypeInfo, toastDurationMedium},
	{"Telemetry", "reconfigure_telemetry", telemetrySettingsChanged, "Reconfiguring telemetry settings...", notification.MsgSettingsReconfiguringTelemetry, ToastTypeInfo, toastDurationShort},
	{"Species tracking", "reconfigure_species_tracking", speciesTrackingSettingsChanged, "Reconfiguring species tracking...", notification.MsgSettingsReconfiguringSpeciesTracking, ToastTypeInfo, toastDurationShort},
	{"Push notifications", "reconfigure_push_notifications", pushNotificationSettingsChanged, "Reconfiguring push notification providers...", notification.MsgSettingsReconfiguringPushNotifications, ToastTypeInfo, toastDurationMedium},
	{"Quiet hours", schedule.SignalReconfigureQuietHours, quietHoursSettingsChanged, "Updating quiet hours schedule...", "", ToastTypeInfo, toastDurationShort},
	{"Web server", "", webserverSettingsChanged, "Web server settings changed. Restart required to apply.", notification.MsgSettingsWebserverRestart, ToastTypeWarning, toastDurationExtended},
	{"OAuth providers", "", oauthProvidersChanged, "Authentication provider settings changed. Restart required to apply.", notification.MsgSettingsOauthRestart, ToastTypeWarning, toastDurationExtended},
	{"Database", "", outputSettingsChanged, "Database settings changed. Restart required to apply.", notification.MsgSettingsDatabaseRestart, ToastTypeWarning, toastDurationExtended},
	{"Logging", "", loggingSettingsChanged, "Logging settings changed. Restart required to apply.", notification.MsgSettingsLoggingRestart, ToastTypeWarning, toastDurationExtended},
	{"Log deduplication", "reconfigure_log_deduplication", logDeduplicationSettingsChanged, "Reconfiguring log deduplication...", "", ToastTypeInfo, toastDurationShort},
	{"RTSP health", "reconfigure_rtsp_health", rtspHealthSettingsChanged, "Reconfiguring RTSP health monitoring...", "", ToastTypeInfo, toastDurationShort},
	{"Monitoring", "reconfigure_monitoring", monitoringSettingsChanged, "Reconfiguring system monitoring...", "", ToastTypeInfo, toastDurationShort},
	{"Live stream", "reconfigure_livestream", liveStreamSettingsChanged, "Reconfiguring live stream settings...", "", ToastTypeInfo, toastDurationShort},
}

// restartRequiringChecks maps a settingsChangeChecks entry (by name) to the i18n
// reason key recorded via restart.MarkRestartRequired when that check fires.
// These settings configure resources bound once at startup (HTTP listener, DB
// connection, log sinks) and cannot hot-reload. Names MUST match table entries
// above; settings_restart_test.go cross-validates these keys against the table
// names and against the hot-reload registry's `restart` category.
var restartRequiringChecks = map[string]string{
	"Web server":      reasonWebserverRestart,
	"OAuth providers": reasonOAuthRestart,
	"Database":        reasonDatabaseRestart,
	"Logging":         reasonLoggingRestart,
}

// handleSettingsChanges checks if important settings have changed and triggers appropriate actions
func (c *Controller) handleSettingsChanges(oldSettings, currentSettings *conf.Settings) error {
	var reconfigActions []string
	var restartReasons []string

	// Process all settings change checks using table-driven approach
	for _, check := range settingsChangeChecks {
		if check.changed(oldSettings, currentSettings) {
			c.Debug("%s settings changed, triggering %s", check.name, check.action)
			if check.action != "" {
				reconfigActions = append(reconfigActions, check.action)
			}
			if check.toast != "" || check.toastKey != "" {
				_ = c.SendToastWithKey(check.toast, check.toastTyp, check.duration, check.toastKey, nil)
			}
			// Defer the actual restart-required mark until all fallible work below
			// has succeeded (see below); only collect the reason here.
			if reasonKey, ok := restartRequiringChecks[check.name]; ok {
				restartReasons = append(restartReasons, reasonKey)
			}
		}
	}

	// Handle audio settings changes (separate due to error return)
	audioActions, err := c.handleAudioSettingsChanges(oldSettings, currentSettings)
	if err != nil {
		// Audio reconfig failed; the caller rolls back the settings snapshot, so
		// the change is not applied. Do NOT mark restart-required, otherwise the
		// banner would nag for a change that never persisted.
		return err
	}
	reconfigActions = append(reconfigActions, audioActions...)

	// Mark restart-required now that the fallible detection above has succeeded.
	// The restart flag is process-global and sticky, so marking it before a
	// possible error/rollback would leave a stuck banner.
	for _, reason := range restartReasons {
		restart.MarkRestartRequired(reason)
	}

	// Apply the runtime block and mutex sampling rates.
	//
	// Deliberately NOT a settingsChangeChecks entry routed through controlChan
	// like the reconfigure_* actions above. The property that separates these
	// from those is not "process-global" (reconfigure_push_notifications and
	// reconfigure_log_deduplication are process-global too): it is that both
	// runtime setters are single atomic stores that cannot fail and cost
	// microseconds. There is nothing to serialize, nothing that can report an
	// error, and no success worth a toast, so the queue that sendReconfigActions
	// maintains, with its delay between sends, buys nothing here.
	//
	// Placed after the fallible audio reconfiguration for the same reason the
	// restart marks are: a change that is about to be rolled back should not
	// have left the process sampling.
	//
	// Both handlers that can carry a diagnostics change route through
	// handleSettingsChanges, and neither routes through publishAndSaveSettings,
	// whose callers are the TLS, MQTT-TLS and detections writers. That is what
	// makes this the one seam covering this section rather than something to be
	// remembered at each write path; getting it backwards is how the token mint
	// first shipped dead, see ensureProfilingTokenForSave. handleSettingsChanges
	// does have those three writers as callers too, which is harmless: each
	// clones the current snapshot and touches only its own section, so the gate
	// below is false on those paths.
	if profilingRatesChanged(oldSettings, currentSettings) {
		profiling.ApplyRates(&currentSettings.Diagnostics.Profiling)
	}

	// Recommend-only region staleness check (rule D2): a station location change
	// may make an installed regional model variant stale. Notify the user; never
	// switch, install, or unload a model. Runs synchronously, like the toast sends
	// above: the work is a cached region-table load, an in-memory snapshot of the
	// installed models, a bounded geometry resolve, and a rate-limited in-memory
	// notification. Placed after the fallible audio reconfig so an audio-reconfig
	// failure rolls back before notifying; a later disk-save failure in the caller
	// leaves a dismissible advisory, the same rare window the restart-required
	// marking above already has.
	if coordinatesChanged(oldSettings, currentSettings) {
		c.notifyRegionStaleness(oldSettings, currentSettings)
	}

	// Trigger reconfigurations asynchronously.
	// Capture debug flag from the settings snapshot so the goroutine never
	// reloads settings (which may be republished by a concurrent update).
	if len(reconfigActions) > 0 {
		debugEnabled := currentSettings.WebServer.Debug
		c.Go(func() {
			c.sendReconfigActions(reconfigActions, debugEnabled)
		})

	}

	return nil
}

// sendReconfigActions sends reconfig actions to controlChan with a delay between
// each to avoid overwhelming the control monitor with rapid-fire reconfiguration.
// Unlike quiet_hours.go:trySendSoundCardSignal (which drops signals via a default
// branch), this blocks until either the send succeeds or shutdown cancels the
// context, because settings reconfig actions are order-dependent and must not be
// lost. The recover guard is defense-in-depth against a TOCTOU race where
// api_service.go closes controlChan before the context cancellation propagates.
func (c *Controller) sendReconfigActions(actions []string, debugEnabled bool) {
	defer func() {
		if r := recover(); r != nil {
			c.LogWarnIfEnabled("Recovered from send on closed controlChan during shutdown",
				logger.Any("panic", r))
		}
	}()

	for _, action := range actions {
		if debugEnabled {
			c.LogDebugIfEnabled("Asynchronously executing action", logger.String("action", action))
		}
		select {
		case <-c.Context().Done():
			return
		case c.controlChan <- action:
		}

		select {
		case <-c.Context().Done():
			return
		case <-time.After(actionDelay):
		}
	}
}

// intervalSettingsChanged checks if species interval or global interval settings have changed.
func intervalSettingsChanged(old, current *conf.Settings) bool {
	return speciesIntervalSettingsChanged(old, current) || old.Realtime.Interval != current.Realtime.Interval
}

// birdnetSettingsChanged checks if BirdNET settings have changed
func birdnetSettingsChanged(oldSettings, currentSettings *conf.Settings) bool {
	// Check for changes in BirdNET locale
	if oldSettings.BirdNET.Locale != currentSettings.BirdNET.Locale {
		return true
	}

	// Check for changes in BirdNET threads
	if oldSettings.BirdNET.Threads != currentSettings.BirdNET.Threads {
		return true
	}

	// Check for changes in BirdNET model path
	if oldSettings.BirdNET.ModelPath != currentSettings.BirdNET.ModelPath {
		return true
	}

	// Check for changes in BirdNET label path
	if oldSettings.BirdNET.LabelPath != currentSettings.BirdNET.LabelPath {
		return true
	}

	// Check for changes in BirdNET XNNPACK acceleration
	if oldSettings.BirdNET.UseXNNPACK != currentSettings.BirdNET.UseXNNPACK {
		return true
	}

	// Check for changes in BirdNET inference backend preference. OpenVINOPath is
	// intentionally NOT checked here: it is restart-required (the OpenVINO core
	// loads the library once via InitOpenVINO and libopenvino_c cannot be safely
	// unloaded), so a runtime path change is declared hotReloadRestart, matching
	// ONNXRuntimePath.
	if oldSettings.BirdNET.Backend != currentSettings.BirdNET.Backend {
		return true
	}

	// Check for changes in the OpenVINO device preference. Switching CPU<->GPU
	// recompiles the model on the new device, so a reload is needed; the OpenVINO
	// core itself stays loaded (only the compiled model and infer request are
	// rebuilt), so this is hot-reloadable, not restart-required. The reload path
	// (handleReloadBirdnet) rebuilds the primary BirdNET classifier and then
	// reloads the OV-capable secondary models (e.g. Perch) via
	// Orchestrator.ReloadSecondaryModels, so a device/backend change applies to
	// both without a restart.
	if oldSettings.BirdNET.OpenVINODevice != currentSettings.BirdNET.OpenVINODevice {
		return true
	}

	return false
}

// analysisOverlapChanged reports whether birdnet.overlap changed. Overlap drives
// the realtime analysis-buffer cadence (read/overlap size), so a change requires
// reallocating the analysis buffers. Overlap is not a per-source audio property,
// so the diff-based reconfigure_audio_sources cannot see it; instead this triggers
// restart_audio_capture, a full teardown and rebuild that re-applies the primary
// model dimensions and reallocates every source's analysis buffers with the new
// overlap. This is separate from reload_birdnet (which rebuilds the model
// instance, not the source buffers).
func analysisOverlapChanged(oldSettings, currentSettings *conf.Settings) bool {
	return oldSettings.BirdNET.Overlap != currentSettings.BirdNET.Overlap
}

// dynamicThresholdEnabledChanged checks if the DynamicThreshold.Enabled flag was toggled.
// When this changes, the persistence and cleanup goroutines must be started or stopped
// to match the new state.
func dynamicThresholdEnabledChanged(oldSettings, currentSettings *conf.Settings) bool {
	return oldSettings.Realtime.DynamicThreshold.Enabled != currentSettings.Realtime.DynamicThreshold.Enabled
}

// rangeFilterSettingsChanged checks if range filter settings have changed
func rangeFilterSettingsChanged(oldSettings, currentSettings *conf.Settings) bool {
	// Check for changes in species include/exclude lists
	if !reflect.DeepEqual(oldSettings.Realtime.Species.Include, currentSettings.Realtime.Species.Include) {
		return true
	}
	if !reflect.DeepEqual(oldSettings.Realtime.Species.Exclude, currentSettings.Realtime.Species.Exclude) {
		return true
	}

	// Check for changes in BirdNET range filter settings
	if !reflect.DeepEqual(oldSettings.BirdNET.RangeFilter, currentSettings.BirdNET.RangeFilter) {
		return true
	}

	// Check for changes in BirdNET location (coordinates or the location-configured flag)
	if coordinatesChanged(oldSettings, currentSettings) {
		return true
	}

	return false
}

// coordinatesChanged reports whether the station location differs between two
// settings snapshots: the latitude, the longitude, or the LocationConfigured
// flag. The flag is included so that configuring a location for the first time
// (LocationConfigured flips false->true while the coordinates may be unchanged)
// still counts as a change, both for range-filter reload and for region
// staleness detection.
func coordinatesChanged(oldSettings, currentSettings *conf.Settings) bool {
	return oldSettings.BirdNET.LocationConfigured != currentSettings.BirdNET.LocationConfigured ||
		oldSettings.BirdNET.Latitude != currentSettings.BirdNET.Latitude ||
		oldSettings.BirdNET.Longitude != currentSettings.BirdNET.Longitude
}

// notifyRegionStaleness runs the recommend-only region staleness detector after
// a station location change and emits a bell notification for each installed
// regional model variant that no longer matches the newly resolved region. Every
// failure degrades to "no notification": it never blocks or fails the settings
// save, and it never switches, installs, or unloads a model (rule D2).
func (c *Controller) notifyRegionStaleness(oldSettings, currentSettings *conf.Settings) {
	if c.ModelManager == nil {
		return
	}
	tables, err := region.Tables()
	if err != nil {
		// Degrade to no notification. The embedded tables are immutable, so this
		// only fails on a corrupt build (the golden region test guards that); log
		// at debug so a real regression stays diagnosable.
		c.LogDebugIfEnabled("region staleness check skipped: region tables unavailable", logger.Error(err))
		return
	}
	installed := c.ModelManager.InstalledRegionalModels()
	if len(installed) == 0 {
		return
	}
	changes := classifier.DetectRegionStaleness(
		tables,
		installed,
		currentSettings.BirdNET.ModelRegion,
		classifier.RegionCoords{
			Lat:        oldSettings.BirdNET.Latitude,
			Lon:        oldSettings.BirdNET.Longitude,
			Configured: oldSettings.BirdNET.LocationConfigured,
		},
		classifier.RegionCoords{
			Lat:        currentSettings.BirdNET.Latitude,
			Lon:        currentSettings.BirdNET.Longitude,
			Configured: currentSettings.BirdNET.LocationConfigured,
		},
	)
	classifier.NotifyRegionStaleness(changes)
}

// mqttSettingsChanged checks if MQTT settings have changed
func mqttSettingsChanged(oldSettings, currentSettings *conf.Settings) bool {
	oldMQTT := oldSettings.Realtime.MQTT
	newMQTT := currentSettings.Realtime.MQTT

	// Check for changes in MQTT settings
	if oldMQTT.Enabled != newMQTT.Enabled ||
		oldMQTT.Broker != newMQTT.Broker ||
		oldMQTT.Topic != newMQTT.Topic ||
		oldMQTT.Username != newMQTT.Username ||
		oldMQTT.Password != newMQTT.Password ||
		oldMQTT.Retain != newMQTT.Retain ||
		oldMQTT.TLS.InsecureSkipVerify != newMQTT.TLS.InsecureSkipVerify ||
		oldMQTT.TLS.CACert != newMQTT.TLS.CACert ||
		oldMQTT.TLS.ClientCert != newMQTT.TLS.ClientCert ||
		oldMQTT.TLS.ClientKey != newMQTT.TLS.ClientKey {
		return true
	}

	if !reflect.DeepEqual(oldMQTT.HomeAssistant, newMQTT.HomeAssistant) {
		return true
	}

	return false
}

// streamsSettingsChanged checks if stream settings have changed in a way that
// requires the audio engine to reconfigure RTSP sources.
//
// Per-stream Models is included: when a user adds or removes a classifier on
// a stream (e.g., enables Perch v2 alongside BirdNET) the orchestrator must
// rebind the stream's analysis pipeline. Without this check, the save
// persists to disk but the running pipeline keeps using the previous model
// set until a restart — silently breaking the hot-reload contract.
func streamsSettingsChanged(oldSettings, currentSettings *conf.Settings) bool {
	oldRTSP := oldSettings.Realtime.RTSP
	newRTSP := currentSettings.Realtime.RTSP

	// Check for changes in stream count
	if len(oldRTSP.Streams) != len(newRTSP.Streams) {
		return true
	}

	// Check for changes in individual streams (name, URL, type, transport, channel
	// mode, media mode, or models)
	for i := range oldRTSP.Streams {
		if i >= len(newRTSP.Streams) {
			return true
		}
		oldStream := &oldRTSP.Streams[i]
		newStream := &newRTSP.Streams[i]
		if oldStream.Name != newStream.Name ||
			oldStream.URL != newStream.URL ||
			oldStream.IsEnabled() != newStream.IsEnabled() ||
			oldStream.Type != newStream.Type ||
			oldStream.Transport != newStream.Transport ||
			oldStream.ChannelMode.Canonical() != newStream.ChannelMode.Canonical() ||
			oldStream.MediaMode.Canonical() != newStream.MediaMode.Canonical() ||
			!slices.Equal(oldStream.Models, newStream.Models) {
			return true
		}
	}

	return false
}

// speciesIntervalSettingsChanged checks if any species-specific interval settings have changed
func speciesIntervalSettingsChanged(oldSettings, currentSettings *conf.Settings) bool {
	// Get the old and new species configs
	oldSpeciesConfigs := oldSettings.Realtime.Species.Config
	newSpeciesConfigs := currentSettings.Realtime.Species.Config

	// Create a set of all species keys from both old and new configs for efficient iteration
	allSpecies := make(map[string]bool)
	for species := range oldSpeciesConfigs {
		allSpecies[species] = true
	}
	for species := range newSpeciesConfigs {
		allSpecies[species] = true
	}

	// Single loop to check all species in both old and new configs
	for species := range allSpecies {
		oldConfig, existedInOld := oldSpeciesConfigs[species]
		newConfig, existsInNew := newSpeciesConfigs[species]

		// Case 1: Species exists in both configs but interval changed
		if existedInOld && existsInNew && oldConfig.Interval != newConfig.Interval {
			return true
		}

		// Case 2: Species was removed and had a custom interval
		if existedInOld && !existsInNew && oldConfig.Interval > 0 {
			return true
		}

		// Case 3: New species was added with a custom interval
		if !existedInOld && existsInNew && newConfig.Interval > 0 {
			return true
		}
	}

	// No relevant changes detected
	return false
}

// birdWeatherSettingsChanged checks if BirdWeather integration settings have changed
func birdWeatherSettingsChanged(oldSettings, currentSettings *conf.Settings) bool {
	// Check for changes in BirdWeather enabled state
	if oldSettings.Realtime.Birdweather.Enabled != currentSettings.Realtime.Birdweather.Enabled {
		return true
	}

	// Check for changes in BirdWeather credentials and configuration
	if oldSettings.Realtime.Birdweather.ID != currentSettings.Realtime.Birdweather.ID ||
		oldSettings.Realtime.Birdweather.Threshold != currentSettings.Realtime.Birdweather.Threshold ||
		oldSettings.Realtime.Birdweather.LocationAccuracy != currentSettings.Realtime.Birdweather.LocationAccuracy {
		return true
	}

	// Check for debug mode changes
	if oldSettings.Realtime.Birdweather.Debug != currentSettings.Realtime.Birdweather.Debug {
		return true
	}

	return false
}

// ebirdSettingsChanged reports whether any eBird setting changed. The eBird API
// client is rebuilt live from these settings (apicore.Core.ReconfigureEBird via
// the reconfigure_ebird control action), so a change triggers a live reconfigure
// rather than a restart prompt.
func ebirdSettingsChanged(oldSettings, currentSettings *conf.Settings) bool {
	return !reflect.DeepEqual(oldSettings.Realtime.EBird, currentSettings.Realtime.EBird)
}

// pushNotificationSettingsChanged checks if push notification settings have changed.
func pushNotificationSettingsChanged(oldSettings, currentSettings *conf.Settings) bool {
	return !reflect.DeepEqual(oldSettings.Notification.Push, currentSettings.Notification.Push)
}

// restoreProfilingRates puts the runtime sampling rates back to what the
// restored snapshot says, after a failed save has rolled the settings back.
//
// Without this the divergence is permanent, not merely temporary, which is what
// separates it from the other side effects handleSettingsChanges triggers. The
// reconfigure_* actions re-read the live snapshot when the control monitor
// processes them (conf.Setting()), so a rollback makes them converge on the
// persisted config by themselves. ApplyRates reads the snapshot at call time
// and is never invoked again on its own, and the change gate then seals it: the
// rolled-back config and the next save both say 0, the comparison finds no
// change, and the process keeps sampling forever at a rate nothing records.
// That is exactly the cost this feature exists to remove, made invisible.
//
// Cheap and idempotent, so it runs unconditionally rather than re-deriving
// whether this particular save touched the rates.
func restoreProfilingRates(current *conf.Settings) {
	if current == nil {
		return
	}
	profiling.ApplyRates(&current.Diagnostics.Profiling)
}

// profilingRatesChanged reports whether either runtime sampling rate differs
// between the two snapshots.
//
// Only the rates. diagnostics.profiling.enabled and .token need no action at
// all: the pprof routes are registered unconditionally and their middleware
// reads the live snapshot per request, so those two are observed on the next
// request without anything being applied here.
func profilingRatesChanged(oldSettings, currentSettings *conf.Settings) bool {
	oldProfiling := &oldSettings.Diagnostics.Profiling
	newProfiling := &currentSettings.Diagnostics.Profiling
	// Resolved rather than raw, so two values that mean the same thing to the
	// runtime do not count as a change. Editing -1 to -5 leaves both profilers
	// off either way, and firing on it would re-log the applied rates for a
	// save that altered nothing.
	return oldProfiling.ResolvedBlockRate() != newProfiling.ResolvedBlockRate() ||
		oldProfiling.ResolvedMutexFraction() != newProfiling.ResolvedMutexFraction()
}

// telemetrySettingsChanged checks if telemetry/observability settings have changed
func telemetrySettingsChanged(oldSettings, currentSettings *conf.Settings) bool {
	// Check for changes in enabled state
	if oldSettings.Realtime.Telemetry.Enabled != currentSettings.Realtime.Telemetry.Enabled {
		return true
	}

	// Check for changes in listen address (only if enabled)
	if currentSettings.Realtime.Telemetry.Enabled &&
		oldSettings.Realtime.Telemetry.Listen != currentSettings.Realtime.Telemetry.Listen {
		return true
	}

	return false
}

// yearlyTrackingChanged checks if yearly tracking settings have changed.
func yearlyTrackingChanged(old, current conf.YearlyTrackingSettings) bool {
	return old.Enabled != current.Enabled ||
		old.WindowDays != current.WindowDays ||
		old.ResetMonth != current.ResetMonth ||
		old.ResetDay != current.ResetDay
}

// seasonalTrackingChanged checks if seasonal tracking settings have changed.
func seasonalTrackingChanged(old, current conf.SeasonalTrackingSettings) bool {
	if old.Enabled != current.Enabled || old.WindowDays != current.WindowDays {
		return true
	}
	if len(old.Seasons) != len(current.Seasons) {
		return true
	}
	for name, oldSeason := range old.Seasons {
		currentSeason, exists := current.Seasons[name]
		if !exists || oldSeason.StartMonth != currentSeason.StartMonth || oldSeason.StartDay != currentSeason.StartDay {
			return true
		}
	}
	return false
}

// speciesTrackingSettingsChanged checks if species tracking settings have changed
func speciesTrackingSettingsChanged(oldSettings, currentSettings *conf.Settings) bool {
	oldTracking := oldSettings.Realtime.SpeciesTracking
	newTracking := currentSettings.Realtime.SpeciesTracking

	// Check for changes in enabled state
	if oldTracking.Enabled != newTracking.Enabled {
		return true
	}

	// If disabled, no need to check other settings
	if !newTracking.Enabled {
		return false
	}

	// Check core settings
	if oldTracking.NewSpeciesWindowDays != newTracking.NewSpeciesWindowDays ||
		oldTracking.SyncIntervalMinutes != newTracking.SyncIntervalMinutes ||
		oldTracking.NotificationSuppressionHours != newTracking.NotificationSuppressionHours {
		return true
	}

	return yearlyTrackingChanged(oldTracking.YearlyTracking, newTracking.YearlyTracking) ||
		seasonalTrackingChanged(oldTracking.SeasonalTracking, newTracking.SeasonalTracking)
}

// webserverSettingsChanged checks if web server settings have changed that require a restart
func webserverSettingsChanged(oldSettings, currentSettings *conf.Settings) bool {
	oldWS := oldSettings.WebServer
	newWS := currentSettings.WebServer

	// Check web server core settings that require a restart to apply. Debug is
	// intentionally excluded: it hot-reloads (registry category `fresh`), so it
	// must not trigger the restart-required toast/banner.
	if oldWS.Port != newWS.Port ||
		oldWS.Enabled != newWS.Enabled ||
		oldWS.BasePath != newWS.BasePath ||
		oldWS.EnableTerminal != newWS.EnableTerminal {
		return true
	}

	// Check security/TLS settings that affect the server. BaseURL is included
	// because it feeds the HTTP->HTTPS redirect authority (api.Config.RedirectAuthority)
	// and the session-cookie Secure decision (SessionCookiesSecure), both captured
	// once at server start; it also seeds OAuth callback URLs at startup. Its only
	// live consumer is notification link generation, so prompting restart matches the
	// existing Host handling (Host is also read live by notifications yet restart-gated).
	oldSec := oldSettings.Security
	newSec := currentSettings.Security

	if oldSec.Host != newSec.Host ||
		oldSec.BaseURL != newSec.BaseURL ||
		oldSec.TLSMode != newSec.TLSMode ||
		oldSec.AutoTLS != newSec.AutoTLS || //nolint:staticcheck // Intentional: backward-compatible migration
		oldSec.RedirectToHTTPS != newSec.RedirectToHTTPS {
		return true
	}

	return false
}

// oauthProvidersChanged reports whether OAuth provider REGISTRATION config changed.
// Provider registration (the goth providers built from client id/secret and the
// callback URL derived from the base URL) binds once at startup via
// security.InitializeGoth and does not hot-reload, so a change needs a restart to
// take effect. Comparison is keyed by provider ID so reordering the list alone is
// not treated as a change. UserID is intentionally excluded: the per-provider
// allowlist is read live by the auth check (via currentSettings), so allowlist
// edits already hot-reload without a restart.
func oauthProvidersChanged(oldSettings, currentSettings *conf.Settings) bool {
	oldProviders := oldSettings.Security.OAuthProviders
	newProviders := currentSettings.Security.OAuthProviders
	if len(oldProviders) != len(newProviders) {
		return true
	}
	// Keyed by provider ID; duplicate IDs (a malformed config) collapse last-wins,
	// which is acceptable and fail-safe: a missed change only delays a not-yet-active
	// provider from registering until the next restart, never grants access.
	newByID := make(map[string]*conf.OAuthProviderConfig, len(newProviders))
	for i := range newProviders {
		newByID[newProviders[i].Provider] = &newProviders[i]
	}
	for i := range oldProviders {
		oldProvider := &oldProviders[i]
		newProvider, ok := newByID[oldProvider.Provider]
		if !ok {
			return true
		}
		if oldProvider.Enabled != newProvider.Enabled ||
			oldProvider.ClientID != newProvider.ClientID ||
			oldProvider.ClientSecret != newProvider.ClientSecret ||
			oldProvider.RedirectURI != newProvider.RedirectURI ||
			oldProvider.IssuerURL != newProvider.IssuerURL ||
			!slices.Equal(oldProvider.Scopes, newProvider.Scopes) {
			return true
		}
	}
	return false
}

// outputSettingsChanged reports whether database output settings changed in a
// way that requires a restart. The database connection pool is opened once at
// startup, so switching backend, path, or connection parameters needs a restart.
func outputSettingsChanged(oldSettings, currentSettings *conf.Settings) bool {
	oldOut := oldSettings.Output
	newOut := currentSettings.Output

	if oldOut.SQLite.Enabled != newOut.SQLite.Enabled ||
		oldOut.SQLite.Path != newOut.SQLite.Path {
		return true
	}

	if oldOut.MySQL.Enabled != newOut.MySQL.Enabled ||
		oldOut.MySQL.Username != newOut.MySQL.Username ||
		oldOut.MySQL.Password != newOut.MySQL.Password ||
		oldOut.MySQL.Database != newOut.MySQL.Database ||
		oldOut.MySQL.Host != newOut.MySQL.Host ||
		oldOut.MySQL.Port != newOut.MySQL.Port {
		return true
	}

	return false
}

// loggingSettingsChanged reports whether the logging configuration changed. Log
// sinks and rotation are configured once at startup, so any change needs a
// restart to take effect.
func loggingSettingsChanged(oldSettings, currentSettings *conf.Settings) bool {
	return !reflect.DeepEqual(oldSettings.Logging, currentSettings.Logging)
}

// logDeduplicationSettingsChanged checks if log deduplication settings have changed.
func logDeduplicationSettingsChanged(old, current *conf.Settings) bool {
	return old.Realtime.LogDeduplication != current.Realtime.LogDeduplication
}

// rtspHealthSettingsChanged checks if RTSP health monitoring settings have changed.
func rtspHealthSettingsChanged(old, current *conf.Settings) bool {
	return old.Realtime.RTSP.Health != current.Realtime.RTSP.Health
}

// monitoringSettingsChanged checks if system monitoring settings have changed.
func monitoringSettingsChanged(old, current *conf.Settings) bool {
	return !reflect.DeepEqual(old.Realtime.Monitoring, current.Realtime.Monitoring)
}

// liveStreamSettingsChanged checks if live stream settings have changed.
func liveStreamSettingsChanged(old, current *conf.Settings) bool {
	return old.WebServer.LiveStream != current.WebServer.LiveStream
}

// LocaleData represents a locale with its code and full name
type LocaleData struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

// ImageProviderOption represents an image provider option
type ImageProviderOption struct {
	Value   string `json:"value"`
	Display string `json:"display"`
}

// GetLocales handles GET /api/v2/settings/locales
func (c *Controller) GetLocales(ctx echo.Context) error {
	c.LogAPIRequest(ctx, logger.LogLevelInfo, "Getting available locales")

	// Return locales in the same format as v1 for compatibility
	// This matches the client-side expectation of key-value pairs
	locales := make(map[string]string)
	maps.Copy(locales, conf.LocaleCodes)

	c.LogAPIRequest(ctx, logger.LogLevelInfo, "Retrieved locales successfully", logger.Int("count", len(locales)))

	return ctx.JSON(http.StatusOK, locales)
}

// capitalizeProviderName returns a display name for the provider with first letter capitalized.
func capitalizeProviderName(name string) string {
	if name == "" {
		return "(unknown)"
	}
	r, size := utf8.DecodeRuneInString(name)
	return strings.ToUpper(string(r)) + name[size:]
}

// collectImageProviders collects and sorts image providers from the registry.
func (c *Controller) collectImageProviders(ctx echo.Context) (providers []ImageProviderOption, count int) {
	providers = []ImageProviderOption{{Value: "auto", Display: "Auto (Default)"}}

	cache := c.BirdImageCache
	if cache == nil {
		c.LogAPIRequest(ctx, logger.LogLevelWarn, "BirdImageCache is nil, cannot get provider names")
		return providers, count
	}

	registry := cache.GetRegistry()
	if registry == nil {
		c.LogAPIRequest(ctx, logger.LogLevelWarn, "ImageProviderRegistry is nil, cannot get provider names")
		return providers, count
	}

	registry.RangeProviders(func(name string, _ *imageprovider.BirdImageCache) bool {
		providers = append(providers, ImageProviderOption{Value: name, Display: capitalizeProviderName(name)})
		count++
		return true
	})

	// Sort providers alphabetically by display name (excluding 'auto')
	if len(providers) > minSortableElements {
		sub := providers[1:]
		sort.Slice(sub, func(i, j int) bool { return sub[i].Display < sub[j].Display })
	}

	return providers, count
}

// GetImageProviders handles GET /api/v2/settings/imageproviders
func (c *Controller) GetImageProviders(ctx echo.Context) error {
	c.LogAPIRequest(ctx, logger.LogLevelInfo, "Getting available image providers")

	providers, providerCount := c.collectImageProviders(ctx)

	c.LogAPIRequest(ctx, logger.LogLevelInfo, "Retrieved image providers successfully", logger.Int("count", len(providers)), logger.Int("provider_count", providerCount))

	return ctx.JSON(http.StatusOK, map[string]any{"providers": providers})
}

// GetSystemID handles GET /api/v2/settings/systemid
func (c *Controller) GetSystemID(ctx echo.Context) error {
	c.LogAPIRequest(ctx, logger.LogLevelInfo, "Getting system ID")

	// Read the controller's lock-free settings snapshot.
	settings := c.ControllerSettings()
	if settings == nil {
		// Fallback to global settings if controller settings not set
		settings = conf.Setting()
		if settings == nil {
			c.LogAPIRequest(ctx, logger.LogLevelError, "Settings not initialized when trying to get system ID", logger.String("endpoint", "GetSystemID"))
			return c.HandleError(ctx, fmt.Errorf("settings not initialized"), "Failed to get settings", http.StatusInternalServerError)
		}
	}

	c.LogAPIRequest(ctx, logger.LogLevelInfo, "Retrieved system ID successfully", logger.String("system_id", settings.SystemID))

	// Return system ID in the format expected by the frontend
	response := map[string]string{
		"systemID": settings.SystemID,
	}

	return ctx.JSON(http.StatusOK, response)
}

// diffSettings computes a key-level diff between two settings snapshots.
// Both snapshots are YAML-marshalled, flattened to dot-separated paths, and
// compared. Sensitive values are scrubbed. Returns nil if nothing changed.
func diffSettings(current, updated *conf.Settings) map[string]map[string]any {
	currentMap := settingsToFlatMap(current)
	updatedMap := settingsToFlatMap(updated)

	sensitiveKeys := support.DefaultSensitiveKeys()
	changes := make(map[string]map[string]any)

	allKeys := make(map[string]struct{}, len(currentMap)+len(updatedMap))
	for k := range currentMap {
		allKeys[k] = struct{}{}
	}
	for k := range updatedMap {
		allKeys[k] = struct{}{}
	}

	for key := range allKeys {
		oldVal := currentMap[key]
		newVal := updatedMap[key]
		if reflect.DeepEqual(oldVal, newVal) {
			continue
		}
		if support.MatchesSensitiveKey(key, sensitiveKeys) {
			changes[key] = map[string]any{"old": "[redacted]", "new": "[redacted]"}
		} else {
			changes[key] = map[string]any{"old": oldVal, "new": newVal}
		}
	}

	if len(changes) == 0 {
		return nil
	}
	return changes
}

// settingsToFlatMap marshals settings to YAML, unmarshals to a generic map,
// and flattens to dot-separated keys.
func settingsToFlatMap(s *conf.Settings) map[string]any {
	data, err := yaml.Marshal(s)
	if err != nil {
		GetLogger().Warn("failed to marshal settings for diff", logger.Error(err))
		return nil
	}
	var m map[string]any
	if err := yaml.Unmarshal(data, &m); err != nil {
		GetLogger().Warn("failed to unmarshal settings for diff", logger.Error(err))
		return nil
	}
	return flattenMap("", m)
}

// flattenMap recursively flattens a nested map into dot-separated keys.
func flattenMap(prefix string, m map[string]any) map[string]any {
	result := make(map[string]any)
	flattenInto(result, prefix, m)
	return result
}

// flattenInto recursively populates result with dot-separated keys from a nested map,
// avoiding intermediate map allocations that the previous recursive-merge approach created.
func flattenInto(result map[string]any, prefix string, m map[string]any) {
	for k, v := range m {
		key := k
		if prefix != "" {
			key = prefix + "." + k
		}
		switch val := v.(type) {
		case map[string]any:
			flattenInto(result, key, val)
		case []any:
			for i, item := range val {
				sliceKey := fmt.Sprintf("%s.%d", key, i)
				if child, ok := item.(map[string]any); ok {
					flattenInto(result, sliceKey, child)
				} else {
					result[sliceKey] = item
				}
			}
		default:
			result[key] = v
		}
	}
}
