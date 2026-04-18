// internal/api/v2/settings.go
package api

import (
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"
	"text/template"
	"time"
	"unicode/utf8"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/audiocore/schedule"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/imageprovider"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/notification"
	"github.com/tphakala/birdnet-go/internal/telemetry"
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
	c.logInfoIfEnabled("Initializing settings routes")

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
	settingsGroup := c.Group.Group("/settings", c.authMiddleware)

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

	c.logInfoIfEnabled("Settings routes initialized successfully")
}

// GetAllSettings handles GET /api/v2/settings
func (c *Controller) GetAllSettings(ctx echo.Context) error {
	c.logInfoIfEnabled("Getting all settings",
		logger.String("path", ctx.Request().URL.Path),
		logger.String("ip", ctx.RealIP()),
	)

	// Acquire read lock to ensure settings aren't being modified during read
	c.settingsMutex.RLock()
	defer c.settingsMutex.RUnlock()

	settings := c.Settings
	if settings == nil {
		// Fallback to global settings if controller settings not set
		settings = conf.Setting()
		if settings == nil {
			c.logErrorIfEnabled("Settings not initialized when trying to get all settings",
				logger.String("path", ctx.Request().URL.Path),
				logger.String("ip", ctx.RealIP()),
			)
			return c.HandleError(ctx, fmt.Errorf("settings not initialized"), "Failed to get settings", http.StatusInternalServerError)
		}
	}

	c.logInfoIfEnabled("Retrieved all settings successfully",
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
	c.logAPIRequest(ctx, logger.LogLevelInfo, "Getting public dashboard settings")

	// Acquire read lock to ensure settings aren't being modified during read.
	c.settingsMutex.RLock()
	defer c.settingsMutex.RUnlock()

	settings := c.Settings
	if settings == nil {
		// Fallback to global settings if controller settings not set.
		settings = conf.Setting()
		if settings == nil {
			c.logAPIRequest(ctx, logger.LogLevelError,
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
		c.logAPIRequest(ctx, logger.LogLevelError,
			"Failed to get dashboard settings section", logger.Error(err))
		return c.HandleError(ctx, err, "Failed to get settings section",
			http.StatusInternalServerError)
	}

	return ctx.JSON(http.StatusOK, sectionValue)
}

// GetSectionSettings handles GET /api/v2/settings/:section
func (c *Controller) GetSectionSettings(ctx echo.Context) error {
	section := ctx.Param("section")
	c.logAPIRequest(ctx, logger.LogLevelInfo, "Getting settings for section", logger.String("section", section))

	// Acquire read lock to ensure settings aren't being modified during read
	c.settingsMutex.RLock()
	defer c.settingsMutex.RUnlock()

	if section == "" {
		c.logAPIRequest(ctx, logger.LogLevelError, "Missing section parameter")
		return c.HandleError(ctx, fmt.Errorf("section not specified"), "Section parameter is required", http.StatusBadRequest)
	}

	settings := c.Settings
	if settings == nil {
		// Fallback to global settings if controller settings not set
		settings = conf.Setting()
		if settings == nil {
			c.logAPIRequest(ctx, logger.LogLevelError, "Settings not initialized when trying to get section settings", logger.String("section", section))
			return c.HandleError(ctx, fmt.Errorf("settings not initialized"), "Failed to get settings", http.StatusInternalServerError)
		}
	}

	// Sanitize first, then extract the section from the sanitized copy
	sanitized := sanitizeSettingsForAPI(settings)
	sectionValue, err := getSettingsSection(sanitized, section)
	if err != nil {
		c.logAPIRequest(ctx, logger.LogLevelError, "Failed to get settings section", logger.String("section", section), logger.Error(err))
		return c.HandleError(ctx, err, "Failed to get settings section", http.StatusNotFound)
	}

	c.logAPIRequest(ctx, logger.LogLevelInfo, "Retrieved settings section successfully", logger.String("section", section))

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
	c.logAPIRequest(ctx, logger.LogLevelInfo, "Attempting to update settings")
	// Serialise concurrent PUT /api/v2/settings calls; each must see the
	// latest published snapshot as its baseline.
	c.settingsMutex.Lock()
	defer c.settingsMutex.Unlock()

	// Read the controller-cached snapshot when set (tests inject this
	// directly); fall back to the global publisher. In production these are
	// the same pointer at boot and stay in sync because every successful
	// publish below updates both c.Settings and conf.settingsInstance.
	current := c.getSettingsOrFallback()
	if current == nil {
		c.logAPIRequest(ctx, logger.LogLevelError, "Settings not initialized during update attempt")
		return c.HandleError(ctx, fmt.Errorf("settings not initialized"), "Failed to get settings", http.StatusInternalServerError)
	}

	// Build a mutable clone; never mutate current in place. Readers holding
	// current through conf.GetSettings continue to see a consistent snapshot
	// until StoreSettings publishes the new one.
	updated := conf.CloneSettings(current)

	// Only publish to the global atomic pointer when this controller is the
	// one that owns it (production path). In tests that inject a controller-
	// local *conf.Settings without touching the global, skip the publish so
	// we don't leak state into other tests.
	publishGlobal := current == conf.GetSettings()

	// Parse the request body
	var updatedSettings conf.Settings
	if err := ctx.Bind(&updatedSettings); err != nil {
		c.logAPIRequest(ctx, logger.LogLevelError, "Failed to bind request body for settings update", logger.Error(err))
		return c.HandleError(ctx, err, "Failed to parse request body", http.StatusBadRequest)
	}

	// Restore redacted secret fields to their current values so the update
	// logic does not overwrite real secrets with the placeholder. Operate on
	// updated (clone) as the canonical destination, not on current.
	if err := restoreRedactedSecrets(updated, &updatedSettings); err != nil {
		c.logAPIRequest(ctx, logger.LogLevelWarn, "Redacted sentinel validation failed", logger.Error(err))
		return c.HandleError(ctx, err, "Cannot save: some secret fields contain the redacted placeholder because their identifying key was changed while the secret was hidden. Re-enter the secret values.", http.StatusBadRequest)
	}

	// Apply allowed field updates to the clone.
	skippedFields, err := updateAllowedSettingsWithTracking(updated, &updatedSettings)
	if err != nil {
		c.logAPIRequest(ctx, logger.LogLevelError, "Error updating allowed settings fields", logger.Error(err), logger.Any("skipped_fields", skippedFields))
		return c.HandleError(ctx, err, "Failed to update settings", http.StatusInternalServerError)
	}
	if len(skippedFields) > 0 {
		c.logAPIRequest(ctx, logger.LogLevelDebug, "Skipped protected fields during settings update", logger.Any("skipped_fields", skippedFields))
	}

	// Normalize species config keys to lowercase for case-insensitive matching.
	if updated.Realtime.Species.Config != nil {
		updated.Realtime.Species.Config = conf.NormalizeSpeciesConfigKeys(updated.Realtime.Species.Config)
	}

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
	// existing pointer holders stay on the old). c.Settings keeps the
	// controller-cached pointer in sync so read handlers that still
	// dereference c.Settings return the freshly published snapshot. The
	// write is safe under c.settingsMutex which all c.Settings readers
	// also acquire, except for c.Debug which deliberately reads via
	// conf.GetSettings() to stay race-free without grabbing the lock.
	if publishGlobal {
		conf.StoreSettings(updated)
	}
	c.Settings = updated

	// Run cross-field side-effects (interval tracking, telemetry toggles, etc.)
	// against the published pair. handleSettingsChanges is read-only on both.
	if err := c.handleSettingsChanges(current, updated); err != nil {
		// Rollback: republish the previous snapshot so in-memory state matches
		// what is on disk (which was never overwritten).
		if publishGlobal {
			conf.StoreSettings(current)
		}
		c.Settings = current
		c.logAPIRequest(ctx, logger.LogLevelError, "Failed to apply settings changes, rolling back", logger.Error(err))
		return c.HandleError(ctx, err, "Failed to apply settings changes, rolled back to previous settings", http.StatusInternalServerError)
	}

	// Persist to disk only when this controller owns the global snapshot
	// (production path) AND DisableSaveSettings is not set. conf.SaveSettings
	// reads conf.GetSettings internally; persisting from a test that injected
	// a standalone c.Settings would save an unrelated snapshot.
	if publishGlobal && !c.DisableSaveSettings {
		if err := conf.SaveSettings(); err != nil {
			// Rollback in-memory; disk write never happened successfully.
			conf.StoreSettings(current)
			c.Settings = current
			c.logAPIRequest(ctx, logger.LogLevelError, "Failed to save settings to disk, rolling back", logger.Error(err))
			return c.HandleError(ctx, err, "Failed to save settings, rolled back to previous settings", http.StatusInternalServerError)
		}
	}

	telemetry.UpdateTelemetryEnabled()
	imageprovider.SetCustomSynonyms(updated.TaxonomySynonyms, updated.BirdNET.Labels)

	c.logAPIRequest(ctx, logger.LogLevelInfo, "Settings updated and saved successfully", logger.Int("skipped_fields_count", len(skippedFields)))
	return ctx.JSON(http.StatusOK, map[string]any{
		"message":       "Settings updated successfully",
		"skippedFields": skippedFields,
	})
}

// updateAllowedSettingsWithTracking updates only the allowed fields and returns a list of skipped fields
func updateAllowedSettingsWithTracking(current, updated *conf.Settings) ([]string, error) {
	var skippedFields []string
	err := updateAllowedFieldsRecursivelyWithTracking(
		reflect.ValueOf(current).Elem(),
		reflect.ValueOf(updated).Elem(),
		getBlockedFieldMap(), // Using blacklist instead of whitelist
		&skippedFields,
		"",
	)
	return skippedFields, err
}

// updateAllowedFieldsRecursivelyWithTracking handles recursive field updates and tracks skipped fields
// Using BLACKLIST approach - fields are allowed by default unless blocked or marked with yaml:"-"
func updateAllowedFieldsRecursivelyWithTracking(
	currentValue, updatedValue reflect.Value,
	blockedFields map[string]any,
	skippedFields *[]string,
	prefix string,
) error {
	if currentValue.Kind() != reflect.Struct || updatedValue.Kind() != reflect.Struct {
		return fmt.Errorf("both values must be structs")
	}

	//nolint:gocritic // Need index i for getFieldInfo() and Field(i) calls
	for i := range currentValue.NumField() {
		fieldInfo := currentValue.Type().Field(i)
		fieldName := fieldInfo.Name
		currentField := currentValue.Field(i)

		// Skip fields marked with yaml:"-" (runtime-only fields)
		yamlTag := fieldInfo.Tag.Get("yaml")
		if yamlTag == "-" {
			fieldPath := prefix
			if fieldPath != "" {
				fieldPath += "."
			}
			fieldPath += fieldName
			*skippedFields = append(*skippedFields, fieldPath+" (runtime-only)")
			continue
		}

		// Get updated field and skip if not valid
		updatedField := updatedValue.FieldByName(fieldName)
		if !updatedField.IsValid() {
			continue
		}

		// Get field info (path and json tag)
		fieldPath, jsonTag := getFieldInfo(currentValue, i, fieldName, prefix)

		// Process the field based on permissions and type
		if err := processField(currentField, updatedField, fieldName, fieldPath, jsonTag,
			blockedFields, skippedFields); err != nil {
			return err
		}
	}

	return nil
}

// getFieldInfo extracts path and JSON tag information for a field
func getFieldInfo(valueType reflect.Value, fieldIndex int, fieldName, prefix string) (fieldPath, jsonTag string) {
	// Get JSON tag name for more readable logging
	jsonTag = valueType.Type().Field(fieldIndex).Tag.Get("json")
	if jsonTag == "" {
		jsonTag = fieldName
	} else {
		// Extract the name part before any comma in the json tag
		if commaIdx := strings.Index(jsonTag, ","); commaIdx > 0 {
			jsonTag = jsonTag[:commaIdx]
		}
	}

	// Build the full path to this field
	fieldPath = fieldName
	if prefix != "" {
		fieldPath = prefix + "." + fieldName
	}

	return fieldPath, jsonTag
}

// processField handles a single field based on its permissions and type
func processField(
	currentField, updatedField reflect.Value,
	fieldName, fieldPath, jsonTag string,
	blockedFields map[string]any,
	skippedFields *[]string,
) error {
	// Check field permissions using blacklist approach
	blockedSubfields, isBlockedAsMap := blockedFields[fieldName].(map[string]any)

	if !isBlockedAsMap {
		// Handle field based on permission (if it's a simple boolean permission or not in blocklist)
		return handleFieldPermission(currentField, updatedField, fieldName, fieldPath, jsonTag,
			blockedFields, skippedFields)
	}

	// Handle field based on its type (struct, pointer, or primitive)
	return handleFieldByType(currentField, updatedField, fieldName, fieldPath, jsonTag,
		blockedSubfields, skippedFields)
}

// handleFieldPermission processes a field based on its permission settings
func handleFieldPermission(
	currentField, updatedField reflect.Value,
	fieldName, fieldPath, jsonTag string,
	blockedFields map[string]any,
	skippedFields *[]string,
) error {
	// INVERTED LOGIC: Default is to ALLOW unless explicitly blocked
	// Check if field is in the blocklist
	if blocked, exists := blockedFields[fieldName]; exists {
		if blockedBool, isBool := blocked.(bool); isBool && blockedBool {
			// Field is explicitly blocked
			*skippedFields = append(*skippedFields, fieldPath)
			return nil // Skip this field
		}
	}

	// By default, the field is allowed to be updated
	if currentField.CanSet() {
		// Check if we need to validate this field
		validationErr := validateField(fieldName, updatedField.Interface())
		if validationErr != nil {
			return fmt.Errorf("validation failed for field %s: %w", jsonTag, validationErr)
		}
		currentField.Set(updatedField)
	}

	return nil
}

// handleFieldByType processes a field based on its type (struct, pointer, or primitive)
func handleFieldByType(
	currentField, updatedField reflect.Value,
	fieldName, fieldPath, jsonTag string,
	blockedSubfields map[string]any,
	skippedFields *[]string,
) error {
	// For struct fields
	if currentField.Kind() == reflect.Struct && updatedField.Kind() == reflect.Struct {
		return handleStructField(currentField, updatedField, fieldPath, blockedSubfields, skippedFields)
	}

	// For fields that are pointers to structs
	if currentField.Kind() == reflect.Pointer && updatedField.Kind() == reflect.Pointer {
		return handlePointerField(currentField, updatedField, fieldPath, blockedSubfields, skippedFields)
	}

	// For primitive fields or other types
	return handlePrimitiveField(currentField, updatedField, fieldName, jsonTag)
}

// handleStructField handles struct fields recursively
func handleStructField(
	currentField, updatedField reflect.Value,
	fieldPath string,
	blockedSubfields map[string]any,
	skippedFields *[]string,
) error {
	return updateAllowedFieldsRecursivelyWithTracking(
		currentField,
		updatedField,
		blockedSubfields,
		skippedFields,
		fieldPath,
	)
}

// handlePointerField handles pointer fields, including nil pointer cases
func handlePointerField(
	currentField, updatedField reflect.Value,
	fieldPath string,
	blockedSubfields map[string]any,
	skippedFields *[]string,
) error {
	// Create a new struct if current is nil but updated is not
	if currentField.IsNil() && !updatedField.IsNil() {
		newStruct := reflect.New(currentField.Type().Elem())
		currentField.Set(newStruct)
	}

	// If both pointers are non-nil and point to structs, update recursively
	if !currentField.IsNil() && !updatedField.IsNil() {
		if currentField.Elem().Kind() == reflect.Struct && updatedField.Elem().Kind() == reflect.Struct {
			return updateAllowedFieldsRecursivelyWithTracking(
				currentField.Elem(),
				updatedField.Elem(),
				blockedSubfields,
				skippedFields,
				fieldPath,
			)
		}
	}

	return nil
}

// handlePrimitiveField handles primitive fields (int, string, etc.)
func handlePrimitiveField(
	currentField, updatedField reflect.Value,
	fieldName, jsonTag string,
) error {
	if currentField.CanSet() {
		// Check if we need to validate this field
		validationErr := validateField(fieldName, updatedField.Interface())
		if validationErr != nil {
			return fmt.Errorf("validation failed for field %s: %w", jsonTag, validationErr)
		}
		currentField.Set(updatedField)
	}

	return nil
}

// getSettingsOrFallback returns controller settings or falls back to global settings.
func (c *Controller) getSettingsOrFallback() *conf.Settings {
	if c.Settings != nil {
		return c.Settings
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

	// Only publish globally when the controller owns the global snapshot
	// (production path); skip when tests inject a standalone c.Settings.
	publishGlobal := current == conf.GetSettings()

	requestBody, err := parseAndValidateJSON(ctx)
	if err != nil {
		return c.HandleError(ctx, err, "Failed to parse request body", http.StatusBadRequest)
	}

	var skippedFields []string
	if err := updateSettingsSectionWithTracking(updated, section, requestBody, &skippedFields); err != nil {
		if len(skippedFields) > 0 {
			c.Debug("Protected fields that were skipped in update of section %s: %s", section, strings.Join(skippedFields, ", "))
		}
		return c.HandleError(ctx, err, fmt.Sprintf("Failed to update %s settings", section), http.StatusBadRequest)
	}

	// Restore redacted secret fields to their current values so the merge
	// does not overwrite real secrets with the placeholder. current is the
	// source of truth for the pre-update values.
	if err := restoreRedactedSecrets(current, updated); err != nil {
		c.logAPIRequest(ctx, logger.LogLevelWarn, "Redacted sentinel validation failed", logger.Error(err))
		return c.HandleError(ctx, err, "Cannot save: some secret fields contain the redacted placeholder because their identifying key was changed while the secret was hidden. Re-enter the secret values.", http.StatusBadRequest)
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

	// Validate the clone before publishing. No rollback needed on validation
	// failure: we simply never publish.
	if err := conf.ValidateSettings(updated); err != nil {
		return c.HandleError(ctx, err,
			fmt.Sprintf("Invalid %s settings", section), http.StatusBadRequest)
	}

	// Publish the new snapshot atomically when we own the global; keep
	// c.Settings in sync. See the matching comment in UpdateSettings for
	// why c.Debug reads via conf.GetSettings() rather than c.Settings.
	if publishGlobal {
		conf.StoreSettings(updated)
	}
	c.Settings = updated

	if err := c.handleSettingsChanges(current, updated); err != nil {
		if publishGlobal {
			conf.StoreSettings(current)
		}
		c.Settings = current
		return c.HandleError(ctx, err, "Failed to apply settings changes, rolled back to previous settings", http.StatusInternalServerError)
	}

	// Persist to disk only when this controller owns the global snapshot
	// AND the test did not disable save. conf.SaveSettings persists the
	// conf.GetSettings value, which would be wrong under a standalone
	// c.Settings injected by a test that bypassed the global publish.
	if publishGlobal && !c.DisableSaveSettings {
		if err := conf.SaveSettings(); err != nil {
			conf.StoreSettings(current)
			c.Settings = current
			return c.HandleError(ctx, err, "Failed to save settings, rolled back to previous settings", http.StatusInternalServerError)
		}
	}

	telemetry.UpdateTelemetryEnabled()

	// Rebuild taxonomy synonym lookup cache if overrides changed
	imageprovider.SetCustomSynonyms(updated.TaxonomySynonyms, updated.BirdNET.Labels)

	return ctx.JSON(http.StatusOK, map[string]any{
		"message":       fmt.Sprintf("%s settings updated successfully", section),
		"skippedFields": skippedFields,
	})
}

// updateSettingsSectionWithTracking updates a specific section of the settings and tracks skipped fields
func updateSettingsSectionWithTracking(settings *conf.Settings, section string, data json.RawMessage, skippedFields *[]string) error {
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
	return handleGenericSection(sectionValue, data, section, skippedFields)
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
	case "notification":
		return &settings.Notification, nil
	default:
		return nil, fmt.Errorf("unknown settings section: %s", section)
	}
}

// handleGenericSection handles updates to any settings section using merging
func handleGenericSection(sectionPtr any, data json.RawMessage, sectionName string, skippedFields *[]string) error {
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

	// Apply field-level permissions if needed
	// Note: getBlockedFieldMap uses capitalized section names (e.g., "BirdNET", "Realtime")
	// We need to map our lowercase section names to the expected capitalized format
	capitalizedSectionName := ""
	switch sectionName {
	case SettingsSectionBirdnet:
		capitalizedSectionName = "BirdNET"
	case SettingsSectionRealtime:
		capitalizedSectionName = "Realtime"
	case SettingsSectionWebserver:
		capitalizedSectionName = "WebServer"
	default:
		// For other sections, capitalize first letter
		if sectionName != "" {
			capitalizedSectionName = strings.ToUpper(sectionName[:1]) + sectionName[1:]
		}
	}

	blockedFieldsMap := getBlockedFieldMap()
	if blockedFields, exists := blockedFieldsMap[capitalizedSectionName]; exists {
		if _, ok := blockedFields.(map[string]any); ok {
			// For now, just note that we have blocked fields
			// The actual blocking happens in updateAllowedFieldsRecursivelyWithTracking
			*skippedFields = append(*skippedFields, fmt.Sprintf("Section %s has field-level restrictions", sectionName))
		}
	}

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
	return validateFloatInRange(updateMap, "longitude", minLongitude, maxLongitude, "longitude")
}

// validateWebServerSection validates WebServer settings
func validateWebServerSection(data json.RawMessage) error {
	var updateMap map[string]any
	if err := json.Unmarshal(data, &updateMap); err != nil {
		return err
	}

	return validatePortField(updateMap, "port")
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

// Field validation constants
const (
	minPort        = 1
	maxPort        = 65535
	minLatitude    = -90
	maxLatitude    = 90
	minLongitude   = -180
	maxLongitude   = 180
	minPasswordLen = 8
)

// fieldValidators maps field names to their validation functions
var fieldValidators = map[string]func(value any) error{
	"port":      validatePort,
	"latitude":  validateLatitude,
	"longitude": validateLongitude,
	"password":  validatePassword,
}

// validateField performs validation on specific fields that require extra checks
// Returns nil if validation passes, error otherwise
func validateField(fieldName string, value any) error {
	if validator, ok := fieldValidators[fieldName]; ok {
		return validator(value)
	}
	return nil
}

// validatePort validates port numbers in valid range (1-65535)
func validatePort(value any) error {
	switch port := value.(type) {
	case int:
		if port < minPort || port > maxPort {
			return fmt.Errorf("port must be between %d and %d", minPort, maxPort)
		}
	case string:
		portInt, err := strconv.Atoi(port)
		if err != nil {
			return fmt.Errorf("port must be a valid number")
		}
		if portInt < minPort || portInt > maxPort {
			return fmt.Errorf("port must be between %d and %d", minPort, maxPort)
		}
	}
	return nil
}

// validateLatitude validates latitude range (-90 to 90)
func validateLatitude(value any) error {
	if lat, ok := value.(float64); ok {
		if lat < minLatitude || lat > maxLatitude {
			return fmt.Errorf("latitude must be between %d and %d", minLatitude, maxLatitude)
		}
	}
	return nil
}

// validateLongitude validates longitude range (-180 to 180)
func validateLongitude(value any) error {
	if lng, ok := value.(float64); ok {
		if lng < minLongitude || lng > maxLongitude {
			return fmt.Errorf("longitude must be between %d and %d", minLongitude, maxLongitude)
		}
	}
	return nil
}

// validatePassword validates password minimum length
func validatePassword(value any) error {
	if pass, ok := value.(string); ok {
		if pass != "" && len(pass) < minPasswordLen {
			return fmt.Errorf("password must be at least %d characters long", minPasswordLen)
		}
	}
	return nil
}

// redactedValue is the placeholder used for secret fields in API responses.
// The frontend can check for this value to show a "secret is set" indicator.
const redactedValue = "**********"

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

	// --- Backup secrets ---
	sanitized.Backup.EncryptionKey = redact(s.Backup.EncryptionKey)

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
		if *inc == redactedValue {
			*inc = *cur
		}
	}

	// Security — defense-in-depth: restore even though SessionSecret is
	// also in the blocked field map (protects against future unblocking).
	restore(&current.Security.SessionSecret, &incoming.Security.SessionSecret)
	restore(&current.Security.BasicAuth.Password, &incoming.Security.BasicAuth.Password)
	restore(&current.Security.GoogleAuth.ClientSecret, &incoming.Security.GoogleAuth.ClientSecret)
	restore(&current.Security.GithubAuth.ClientSecret, &incoming.Security.GithubAuth.ClientSecret)
	restore(&current.Security.MicrosoftAuth.ClientSecret, &incoming.Security.MicrosoftAuth.ClientSecret)

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

	// Backup
	restore(&current.Backup.EncryptionKey, &incoming.Backup.EncryptionKey)

	// Backup target secrets — match by Type to handle reordering
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
	check(s.Realtime.MQTT.Password, "realtime.mqtt.password")
	check(s.Output.MySQL.Password, "output.mysql.password")
	check(s.Realtime.Weather.OpenWeather.APIKey, "realtime.weather.openWeather.apiKey")
	check(s.Realtime.Weather.Wunderground.APIKey, "realtime.weather.wunderground.apiKey")
	check(s.Realtime.EBird.APIKey, "realtime.ebird.apiKey")
	check(s.Backup.EncryptionKey, "backup.encryptionKey")

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
	clearField(&s.Realtime.MQTT.Password)
	clearField(&s.Output.MySQL.Password)
	clearField(&s.Realtime.Weather.OpenWeather.APIKey)
	clearField(&s.Realtime.Weather.Wunderground.APIKey)
	clearField(&s.Realtime.EBird.APIKey)
	clearField(&s.Backup.EncryptionKey)

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
// Using BLACKLIST approach - all fields are allowed by default except:
// 1. Fields marked with yaml:"-" tag (automatically skipped)
// 2. Fields explicitly listed here for security reasons
//
// IMPORTANT: Only add fields here if they pose a security risk
// Most runtime-only fields should use yaml:"-" tag instead
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

		// Realtime section - block runtime fields
		"Realtime": map[string]any{
			"Audio": getAudioBlockedFields(),
		},

		// All other fields are allowed by default
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

// settingsChangeChecks defines all settings change detectors in order of execution.
// Each check has a detection function, action to trigger, and toast notification.
var settingsChangeChecks = []settingsChangeCheck{
	{"BirdNET", "reload_birdnet", birdnetSettingsChanged, "Reloading BirdNET model with new settings...", notification.MsgSettingsReloadingBirdnet, "info", toastDurationLong},
	{"Range filter", "rebuild_range_filter", rangeFilterSettingsChanged, "Rebuilding species range filter...", notification.MsgSettingsRebuildingRangeFilter, "info", toastDurationMedium},
	{"Species interval", "update_detection_intervals", intervalSettingsChanged, "Updating detection intervals...", notification.MsgSettingsUpdatingIntervals, "info", toastDurationShort},
	{"Base threshold", "recalculate_dynamic_thresholds", baseThresholdChanged, "Recalculating dynamic thresholds...", notification.MsgSettingsRecalculatingThresholds, "info", toastDurationShort},
	{"Dynamic thresholds", "reconfigure_dynamic_thresholds", dynamicThresholdEnabledChanged, "Reconfiguring dynamic thresholds...", notification.MsgSettingsReconfiguringDynamicThresholds, "info", toastDurationMedium},
	{"MQTT", "reconfigure_mqtt", mqttSettingsChanged, "Reconfiguring MQTT connection...", notification.MsgSettingsReconfiguringMqtt, "info", toastDurationMedium},
	{"BirdWeather", "reconfigure_birdweather", birdWeatherSettingsChanged, "Reconfiguring BirdWeather integration...", notification.MsgSettingsReconfiguringBirdweather, "info", toastDurationMedium},
	{"Streams", "reconfigure_rtsp_sources", streamsSettingsChanged, "Reconfiguring audio streams...", notification.MsgSettingsReconfiguringStreams, "info", toastDurationMedium},
	{"Telemetry", "reconfigure_telemetry", telemetrySettingsChanged, "Reconfiguring telemetry settings...", notification.MsgSettingsReconfiguringTelemetry, "info", toastDurationShort},
	{"Species tracking", "reconfigure_species_tracking", speciesTrackingSettingsChanged, "Reconfiguring species tracking...", notification.MsgSettingsReconfiguringSpeciesTracking, "info", toastDurationShort},
	{"Push notifications", "reconfigure_push_notifications", pushNotificationSettingsChanged, "Reconfiguring push notification providers...", notification.MsgSettingsReconfiguringPushNotifications, "info", toastDurationMedium},
	{"Quiet hours", schedule.SignalReconfigureQuietHours, quietHoursSettingsChanged, "Updating quiet hours schedule...", "", "info", toastDurationShort},
	{"Web server", "", webserverSettingsChanged, "Web server settings changed. Restart required to apply.", notification.MsgSettingsWebserverRestart, "warning", toastDurationExtended},
}

// handleSettingsChanges checks if important settings have changed and triggers appropriate actions
func (c *Controller) handleSettingsChanges(oldSettings, currentSettings *conf.Settings) error {
	var reconfigActions []string

	// Process all settings change checks using table-driven approach
	for _, check := range settingsChangeChecks {
		if check.changed(oldSettings, currentSettings) {
			c.Debug("%s settings changed, triggering %s", check.name, check.action)
			if check.action != "" {
				reconfigActions = append(reconfigActions, check.action)
			}
			_ = c.SendToastWithKey(check.toast, check.toastTyp, check.duration, check.toastKey, nil)
		}
	}

	// Handle audio settings changes (separate due to error return)
	audioActions, err := c.handleAudioSettingsChanges(oldSettings, currentSettings)
	if err != nil {
		return err
	}
	reconfigActions = append(reconfigActions, audioActions...)

	// Trigger reconfigurations asynchronously
	if len(reconfigActions) > 0 {
		go func(actions []string) {
			for _, action := range actions {
				c.Debug("Asynchronously executing action: %s", action)
				c.controlChan <- action
				time.Sleep(actionDelay)
			}
		}(reconfigActions)
	}

	return nil
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

	return false
}

// baseThresholdChanged checks if the global BirdNET confidence threshold has changed.
// When this changes, dynamic threshold CurrentValue entries must be recalculated
// since they store absolute values derived from the base threshold.
func baseThresholdChanged(oldSettings, currentSettings *conf.Settings) bool {
	return oldSettings.BirdNET.Threshold != currentSettings.BirdNET.Threshold
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

	// Check for changes in BirdNET latitude and longitude
	if oldSettings.BirdNET.Latitude != currentSettings.BirdNET.Latitude || oldSettings.BirdNET.Longitude != currentSettings.BirdNET.Longitude {
		return true
	}

	return false
}

// mqttSettingsChanged checks if MQTT settings have changed
func mqttSettingsChanged(oldSettings, currentSettings *conf.Settings) bool {
	oldMQTT := oldSettings.Realtime.MQTT
	newMQTT := currentSettings.Realtime.MQTT

	// Check for changes in MQTT settings
	return oldMQTT.Enabled != newMQTT.Enabled ||
		oldMQTT.Broker != newMQTT.Broker ||
		oldMQTT.Topic != newMQTT.Topic ||
		oldMQTT.Username != newMQTT.Username ||
		oldMQTT.Password != newMQTT.Password ||
		oldMQTT.Retain != newMQTT.Retain ||
		oldMQTT.TLS.InsecureSkipVerify != newMQTT.TLS.InsecureSkipVerify ||
		oldMQTT.TLS.CACert != newMQTT.TLS.CACert ||
		oldMQTT.TLS.ClientCert != newMQTT.TLS.ClientCert ||
		oldMQTT.TLS.ClientKey != newMQTT.TLS.ClientKey
}

// streamsSettingsChanged checks if stream settings have changed
func streamsSettingsChanged(oldSettings, currentSettings *conf.Settings) bool {
	oldRTSP := oldSettings.Realtime.RTSP
	newRTSP := currentSettings.Realtime.RTSP

	// Check for changes in stream count
	if len(oldRTSP.Streams) != len(newRTSP.Streams) {
		return true
	}

	// Check for changes in individual streams (name, URL, type, or transport)
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
			oldStream.Transport != newStream.Transport {
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

// pushNotificationSettingsChanged checks if push notification settings have changed.
func pushNotificationSettingsChanged(oldSettings, currentSettings *conf.Settings) bool {
	return !reflect.DeepEqual(oldSettings.Notification.Push, currentSettings.Notification.Push)
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

	// Check web server core settings
	if oldWS.Port != newWS.Port ||
		oldWS.Enabled != newWS.Enabled ||
		oldWS.Debug != newWS.Debug {
		return true
	}

	// Check security/TLS settings that affect the server
	oldSec := oldSettings.Security
	newSec := currentSettings.Security

	if oldSec.Host != newSec.Host ||
		oldSec.AutoTLS != newSec.AutoTLS || //nolint:staticcheck // Intentional: backward-compatible migration
		oldSec.RedirectToHTTPS != newSec.RedirectToHTTPS {
		return true
	}

	return false
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
	c.logAPIRequest(ctx, logger.LogLevelInfo, "Getting available locales")

	// Return locales in the same format as v1 for compatibility
	// This matches the client-side expectation of key-value pairs
	locales := make(map[string]string)
	maps.Copy(locales, conf.LocaleCodes)

	c.logAPIRequest(ctx, logger.LogLevelInfo, "Retrieved locales successfully", logger.Int("count", len(locales)))

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

	if c.BirdImageCache == nil {
		c.logAPIRequest(ctx, logger.LogLevelWarn, "BirdImageCache is nil, cannot get provider names")
		return providers, count
	}

	registry := c.BirdImageCache.GetRegistry()
	if registry == nil {
		c.logAPIRequest(ctx, logger.LogLevelWarn, "ImageProviderRegistry is nil, cannot get provider names")
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
	c.logAPIRequest(ctx, logger.LogLevelInfo, "Getting available image providers")

	providers, providerCount := c.collectImageProviders(ctx)

	c.logAPIRequest(ctx, logger.LogLevelInfo, "Retrieved image providers successfully", logger.Int("count", len(providers)), logger.Int("provider_count", providerCount))

	return ctx.JSON(http.StatusOK, map[string]any{"providers": providers})
}

// GetSystemID handles GET /api/v2/settings/systemid
func (c *Controller) GetSystemID(ctx echo.Context) error {
	c.logAPIRequest(ctx, logger.LogLevelInfo, "Getting system ID")

	// Acquire read lock to ensure settings aren't being modified during read
	c.settingsMutex.RLock()
	defer c.settingsMutex.RUnlock()

	settings := c.Settings
	if settings == nil {
		// Fallback to global settings if controller settings not set
		settings = conf.Setting()
		if settings == nil {
			c.logAPIRequest(ctx, logger.LogLevelError, "Settings not initialized when trying to get system ID", logger.String("endpoint", "GetSystemID"))
			return c.HandleError(ctx, fmt.Errorf("settings not initialized"), "Failed to get settings", http.StatusInternalServerError)
		}
	}

	c.logAPIRequest(ctx, logger.LogLevelInfo, "Retrieved system ID successfully", logger.String("system_id", settings.SystemID))

	// Return system ID in the format expected by the frontend
	response := map[string]string{
		"systemID": settings.SystemID,
	}

	return ctx.JSON(http.StatusOK, response)
}
