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
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/imageprovider"
	"github.com/tphakala/birdnet-go/internal/logger"
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

	// Create settings API group
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
	settingsGroup.GET("/:section", c.GetSectionSettings)
	// PUT /api/v2/settings - Updates multiple settings sections with complete replacement
	settingsGroup.PUT("", c.UpdateSettings)
	// PATCH /api/v2/settings/:section - Updates a specific settings section with partial replacement
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

	// Return a copy of the settings
	return ctx.JSON(http.StatusOK, settings)
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

	// Get the settings section
	sectionValue, err := getSettingsSection(settings, section)
	if err != nil {
		c.logAPIRequest(ctx, logger.LogLevelError, "Failed to get settings section", logger.String("section", section), logger.Error(err))
		return c.HandleError(ctx, err, "Failed to get settings section", http.StatusNotFound)
	}

	c.logAPIRequest(ctx, logger.LogLevelInfo, "Retrieved settings section successfully", logger.String("section", section))

	return ctx.JSON(http.StatusOK, sectionValue)
}

// UpdateSettings handles PUT /api/v2/settings
func (c *Controller) UpdateSettings(ctx echo.Context) error {
	c.logAPIRequest(ctx, logger.LogLevelInfo, "Attempting to update settings")
	// Acquire write lock to prevent concurrent settings updates
	c.settingsMutex.Lock()
	defer c.settingsMutex.Unlock()

	settings := c.Settings
	if settings == nil {
		// Fallback to global settings if controller settings not set
		settings = conf.Setting()
		if settings == nil {
			c.logAPIRequest(ctx, logger.LogLevelError, "Settings not initialized during update attempt")
			return c.HandleError(ctx, fmt.Errorf("settings not initialized"), "Failed to get settings", http.StatusInternalServerError)
		}
	}

	// Create a backup of current settings for rollback if needed
	oldSettings := *settings

	// Parse the request body
	var updatedSettings conf.Settings
	if err := ctx.Bind(&updatedSettings); err != nil {
		// Log binding error
		c.logAPIRequest(ctx, logger.LogLevelError, "Failed to bind request body for settings update", logger.Error(err))
		return c.HandleError(ctx, err, "Failed to parse request body", http.StatusBadRequest)
	}

	// Verify the request body contains valid data
	if err := validateSettingsData(&updatedSettings); err != nil {
		c.logAPIRequest(ctx, logger.LogLevelError, "Invalid settings data received", logger.Error(err))
		return c.HandleError(ctx, err, "Invalid settings data", http.StatusBadRequest)
	}

	// Update only the fields that are allowed to be changed
	skippedFields, err := updateAllowedSettingsWithTracking(settings, &updatedSettings)
	if err != nil {
		// Log error during field update attempt
		c.logAPIRequest(ctx, logger.LogLevelError, "Error updating allowed settings fields", logger.Error(err), logger.Any("skipped_fields", skippedFields))
		return c.HandleError(ctx, err, "Failed to update settings", http.StatusInternalServerError)
	}
	if len(skippedFields) > 0 {
		// Log skipped fields at Debug level
		c.logAPIRequest(ctx, logger.LogLevelDebug, "Skipped protected fields during settings update", logger.Any("skipped_fields", skippedFields))
	}

	// Normalize species config keys to lowercase for case-insensitive matching
	if settings.Realtime.Species.Config != nil {
		settings.Realtime.Species.Config = conf.NormalizeSpeciesConfigKeys(settings.Realtime.Species.Config)
	}

	// Check if any important settings have changed and trigger actions as needed
	if err := c.handleSettingsChanges(&oldSettings, settings); err != nil {
		// Attempt to rollback changes if applying them failed
		*settings = oldSettings
		c.logAPIRequest(ctx, logger.LogLevelError, "Failed to apply settings changes, rolling back", logger.Error(err))
		return c.HandleError(ctx, err, "Failed to apply settings changes, rolled back to previous settings", http.StatusInternalServerError)
	}

	// Save settings to disk
	if err := conf.SaveSettings(); err != nil {
		// Attempt to rollback changes if saving failed
		*settings = oldSettings
		c.logAPIRequest(ctx, logger.LogLevelError, "Failed to save settings to disk, rolling back", logger.Error(err))
		return c.HandleError(ctx, err, "Failed to save settings, rolled back to previous settings", http.StatusInternalServerError)
	}

	// Update the cached telemetry state after settings change
	telemetry.UpdateTelemetryEnabled()

	c.logAPIRequest(ctx, logger.LogLevelInfo, "Settings updated and saved successfully", logger.Int("skipped_fields_count", len(skippedFields)))
	return ctx.JSON(http.StatusOK, map[string]any{
		"message":       "Settings updated successfully",
		"skippedFields": skippedFields,
	})
}

// validateSettingsData performs basic validation on the settings data
func validateSettingsData(settings *conf.Settings) error {
	// Check for null settings
	if settings == nil {
		return fmt.Errorf("settings cannot be null")
	}

	// Validate BirdNET settings
	if settings.BirdNET.Latitude < -90 || settings.BirdNET.Latitude > 90 {
		return fmt.Errorf("latitude must be between -90 and 90")
	}

	if settings.BirdNET.Longitude < -180 || settings.BirdNET.Longitude > 180 {
		return fmt.Errorf("longitude must be between -180 and 180")
	}

	// Validate WebServer settings - fix for port type
	// Check if we can convert the port to an integer
	var (
		portInt int
		err     error
	)

	// If the port is a string (as indicated by the linter error), convert it to int
	switch v := any(settings.WebServer.Port).(type) {
	case int:
		portInt = v
	case string:
		portInt, err = strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("invalid port number: %v", v)
		}
	default:
		return fmt.Errorf("port has an unsupported type: %T", v)
	}

	if portInt < 1 || portInt > 65535 {
		return fmt.Errorf("port must be between 1 and 65535")
	}

	// Add additional validation for other fields as needed

	return nil
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
	if currentField.Kind() == reflect.Ptr && updatedField.Kind() == reflect.Ptr {
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

// UpdateSectionSettings handles PATCH /api/v2/settings/:section
func (c *Controller) UpdateSectionSettings(ctx echo.Context) error {
	c.settingsMutex.Lock()
	defer c.settingsMutex.Unlock()

	section := ctx.Param("section")
	if section == "" {
		return c.HandleError(ctx, fmt.Errorf("section not specified"), "Section parameter is required", http.StatusBadRequest)
	}

	settings := c.getSettingsOrFallback()
	if settings == nil {
		return c.HandleError(ctx, fmt.Errorf("settings not initialized"), "Failed to get settings", http.StatusInternalServerError)
	}

	oldSettings := *settings

	requestBody, err := parseAndValidateJSON(ctx)
	if err != nil {
		return c.HandleError(ctx, err, "Failed to parse request body", http.StatusBadRequest)
	}

	var skippedFields []string
	if err := updateSettingsSectionWithTracking(settings, section, requestBody, &skippedFields); err != nil {
		if len(skippedFields) > 0 {
			c.Debug("Protected fields that were skipped in update of section %s: %s", section, strings.Join(skippedFields, ", "))
		}
		return c.HandleError(ctx, err, fmt.Sprintf("Failed to update %s settings", section), http.StatusBadRequest)
	}

	if err := c.handleSettingsChanges(&oldSettings, settings); err != nil {
		*settings = oldSettings
		return c.HandleError(ctx, err, "Failed to apply settings changes, rolled back to previous settings", http.StatusInternalServerError)
	}

	if !c.DisableSaveSettings {
		if err := conf.SaveSettings(); err != nil {
			*settings = oldSettings
			return c.HandleError(ctx, err, "Failed to save settings, rolled back to previous settings", http.StatusInternalServerError)
		}
	}

	telemetry.UpdateTelemetryEnabled()

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

// mergeJSONIntoStruct merges JSON data into an existing struct without zeroing out missing fields
// This is crucial for preserving nested object values when partial updates are sent
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

	return json.Unmarshal(mergedJSON, target)
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
		if _, err := template.New("title").Parse(notificationConfig.Templates.NewSpecies.Title); err != nil {
			return fmt.Errorf("invalid template syntax in new species title: %w", err)
		}
	}

	if notificationConfig.Templates.NewSpecies.Message != "" {
		if _, err := template.New("message").Parse(notificationConfig.Templates.NewSpecies.Message); err != nil {
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
	toast    string                                 // Toast message to display
	toastTyp string                                 // Toast type: "info" or "warning"
	duration int                                    // Toast duration in milliseconds
}

// settingsChangeChecks defines all settings change detectors in order of execution.
// Each check has a detection function, action to trigger, and toast notification.
var settingsChangeChecks = []settingsChangeCheck{
	{"BirdNET", "reload_birdnet", birdnetSettingsChanged, "Reloading BirdNET model with new settings...", "info", toastDurationLong},
	{"Range filter", "rebuild_range_filter", rangeFilterSettingsChanged, "Rebuilding species range filter...", "info", toastDurationMedium},
	{"Species interval", "update_detection_intervals", intervalSettingsChanged, "Updating detection intervals...", "info", toastDurationShort},
	{"MQTT", "reconfigure_mqtt", mqttSettingsChanged, "Reconfiguring MQTT connection...", "info", toastDurationMedium},
	{"BirdWeather", "reconfigure_birdweather", birdWeatherSettingsChanged, "Reconfiguring BirdWeather integration...", "info", toastDurationMedium},
	{"Streams", "reconfigure_rtsp_sources", streamsSettingsChanged, "Reconfiguring audio streams...", "info", toastDurationMedium},
	{"Telemetry", "reconfigure_telemetry", telemetrySettingsChanged, "Reconfiguring telemetry settings...", "info", toastDurationShort},
	{"Species tracking", "reconfigure_species_tracking", speciesTrackingSettingsChanged, "Reconfiguring species tracking...", "info", toastDurationShort},
	{"Web server", "", webserverSettingsChanged, "Web server settings changed. Restart required to apply.", "warning", toastDurationExtended},
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
			_ = c.SendToast(check.toast, check.toastTyp, check.duration)
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
	for i, oldStream := range oldRTSP.Streams {
		if i >= len(newRTSP.Streams) {
			return true
		}
		newStream := newRTSP.Streams[i]
		if oldStream.Name != newStream.Name ||
			oldStream.URL != newStream.URL ||
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
		oldSec.AutoTLS != newSec.AutoTLS ||
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
