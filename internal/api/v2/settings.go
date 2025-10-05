// internal/api/v2/settings.go
package api

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/imageprovider"
	"github.com/tphakala/birdnet-go/internal/telemetry"
)

// UpdateRequest represents a request to update settings
type UpdateRequest struct {
	Path  string `json:"path"`
	Value any    `json:"value"`
}

// initSettingsRoutes registers all settings-related API endpoints
func (c *Controller) initSettingsRoutes() {
	if c.apiLogger != nil {
		c.apiLogger.Info("Initializing settings routes")
	}

	// Create settings API group
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
	settingsGroup.GET("/:section", c.GetSectionSettings)
	// PUT /api/v2/settings - Updates multiple settings sections with complete replacement
	settingsGroup.PUT("", c.UpdateSettings)
	// PATCH /api/v2/settings/:section - Updates a specific settings section with partial replacement
	settingsGroup.PATCH("/:section", c.UpdateSectionSettings)

	if c.apiLogger != nil {
		c.apiLogger.Info("Settings routes initialized successfully")
	}
}

// GetAllSettings handles GET /api/v2/settings
func (c *Controller) GetAllSettings(ctx echo.Context) error {
	if c.apiLogger != nil {
		c.apiLogger.Info("Getting all settings",
			"path", ctx.Request().URL.Path,
			"ip", ctx.RealIP(),
		)
	}

	// Acquire read lock to ensure settings aren't being modified during read
	c.settingsMutex.RLock()
	defer c.settingsMutex.RUnlock()

	settings := c.Settings
	if settings == nil {
		// Fallback to global settings if controller settings not set
		settings = conf.Setting()
		if settings == nil {
			if c.apiLogger != nil {
				c.apiLogger.Error("Settings not initialized when trying to get all settings",
					"path", ctx.Request().URL.Path,
					"ip", ctx.RealIP(),
				)
			}
			return c.HandleError(ctx, fmt.Errorf("settings not initialized"), "Failed to get settings", http.StatusInternalServerError)
		}
	}

	if c.apiLogger != nil {
		c.apiLogger.Info("Retrieved all settings successfully",
			"path", ctx.Request().URL.Path,
			"ip", ctx.RealIP(),
		)
	}

	// Return a copy of the settings
	return ctx.JSON(http.StatusOK, settings)
}

// GetSectionSettings handles GET /api/v2/settings/:section
func (c *Controller) GetSectionSettings(ctx echo.Context) error {
	section := ctx.Param("section")
	c.logAPIRequest(ctx, slog.LevelInfo, "Getting settings for section", "section", section)

	// Acquire read lock to ensure settings aren't being modified during read
	c.settingsMutex.RLock()
	defer c.settingsMutex.RUnlock()

	if section == "" {
		c.logAPIRequest(ctx, slog.LevelError, "Missing section parameter")
		return c.HandleError(ctx, fmt.Errorf("section not specified"), "Section parameter is required", http.StatusBadRequest)
	}

	settings := c.Settings
	if settings == nil {
		// Fallback to global settings if controller settings not set
		settings = conf.Setting()
		if settings == nil {
			c.logAPIRequest(ctx, slog.LevelError, "Settings not initialized when trying to get section settings", "section", section)
			return c.HandleError(ctx, fmt.Errorf("settings not initialized"), "Failed to get settings", http.StatusInternalServerError)
		}
	}

	// Get the settings section
	sectionValue, err := getSettingsSection(settings, section)
	if err != nil {
		c.logAPIRequest(ctx, slog.LevelError, "Failed to get settings section", "section", section, "error", err.Error())
		return c.HandleError(ctx, err, "Failed to get settings section", http.StatusNotFound)
	}

	c.logAPIRequest(ctx, slog.LevelInfo, "Retrieved settings section successfully", "section", section)

	return ctx.JSON(http.StatusOK, sectionValue)
}

// UpdateSettings handles PUT /api/v2/settings
func (c *Controller) UpdateSettings(ctx echo.Context) error {
	c.logAPIRequest(ctx, slog.LevelInfo, "Attempting to update settings")
	// Acquire write lock to prevent concurrent settings updates
	c.settingsMutex.Lock()
	defer c.settingsMutex.Unlock()

	settings := c.Settings
	if settings == nil {
		// Fallback to global settings if controller settings not set
		settings = conf.Setting()
		if settings == nil {
			c.logAPIRequest(ctx, slog.LevelError, "Settings not initialized during update attempt")
			return c.HandleError(ctx, fmt.Errorf("settings not initialized"), "Failed to get settings", http.StatusInternalServerError)
		}
	}

	// Create a backup of current settings for rollback if needed
	oldSettings := *settings

	// Parse the request body
	var updatedSettings conf.Settings
	if err := ctx.Bind(&updatedSettings); err != nil {
		// Log binding error
		c.logAPIRequest(ctx, slog.LevelError, "Failed to bind request body for settings update", "error", err.Error())
		return c.HandleError(ctx, err, "Failed to parse request body", http.StatusBadRequest)
	}

	// Verify the request body contains valid data
	if err := validateSettingsData(&updatedSettings); err != nil {
		c.logAPIRequest(ctx, slog.LevelError, "Invalid settings data received", "error", err.Error())
		return c.HandleError(ctx, err, "Invalid settings data", http.StatusBadRequest)
	}

	// Update only the fields that are allowed to be changed
	skippedFields, err := updateAllowedSettingsWithTracking(settings, &updatedSettings)
	if err != nil {
		// Log error during field update attempt
		c.logAPIRequest(ctx, slog.LevelError, "Error updating allowed settings fields", "error", err.Error(), "skipped_fields", skippedFields)
		return c.HandleError(ctx, err, "Failed to update settings", http.StatusInternalServerError)
	}
	if len(skippedFields) > 0 {
		// Log skipped fields at Debug level
		c.logAPIRequest(ctx, slog.LevelDebug, "Skipped protected fields during settings update", "skipped_fields", skippedFields)
	}

	// Check if any important settings have changed and trigger actions as needed
	if err := c.handleSettingsChanges(&oldSettings, settings); err != nil {
		// Attempt to rollback changes if applying them failed
		*settings = oldSettings
		c.logAPIRequest(ctx, slog.LevelError, "Failed to apply settings changes, rolling back", "error", err.Error())
		return c.HandleError(ctx, err, "Failed to apply settings changes, rolled back to previous settings", http.StatusInternalServerError)
	}

	// Save settings to disk
	if err := conf.SaveSettings(); err != nil {
		// Attempt to rollback changes if saving failed
		*settings = oldSettings
		c.logAPIRequest(ctx, slog.LevelError, "Failed to save settings to disk, rolling back", "error", err.Error())
		return c.HandleError(ctx, err, "Failed to save settings, rolled back to previous settings", http.StatusInternalServerError)
	}

	// Update the cached telemetry state after settings change
	telemetry.UpdateTelemetryEnabled()

	c.logAPIRequest(ctx, slog.LevelInfo, "Settings updated and saved successfully", "skipped_fields_count", len(skippedFields))
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

	for i := 0; i < currentValue.NumField(); i++ {
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

// UpdateSectionSettings handles PATCH /api/v2/settings/:section
func (c *Controller) UpdateSectionSettings(ctx echo.Context) error {
	// Acquire write lock to prevent concurrent settings updates
	c.settingsMutex.Lock()
	defer c.settingsMutex.Unlock()

	section := ctx.Param("section")
	if section == "" {
		return c.HandleError(ctx, fmt.Errorf("section not specified"), "Section parameter is required", http.StatusBadRequest)
	}

	settings := c.Settings
	if settings == nil {
		// Fallback to global settings if controller settings not set
		settings = conf.Setting()
		if settings == nil {
			return c.HandleError(ctx, fmt.Errorf("settings not initialized"), "Failed to get settings", http.StatusInternalServerError)
		}
	}

	// Create a backup of current settings for rollback if needed
	oldSettings := *settings

	// Parse the request body
	var requestBody json.RawMessage
	if err := ctx.Bind(&requestBody); err != nil {
		return c.HandleError(ctx, err, "Failed to parse request body", http.StatusBadRequest)
	}

	// Validate that the request body contains valid JSON
	var tempValue any
	if err := json.Unmarshal(requestBody, &tempValue); err != nil {
		return c.HandleError(ctx, err, "Invalid JSON in request body", http.StatusBadRequest)
	}

	// Update the specific section
	var skippedFields []string
	if err := updateSettingsSectionWithTracking(settings, section, requestBody, &skippedFields); err != nil {
		// Log which fields were attempted to be updated but were protected
		if len(skippedFields) > 0 {
			c.Debug("Protected fields that were skipped in update of section %s: %s", section, strings.Join(skippedFields, ", "))
		}
		return c.HandleError(ctx, err, fmt.Sprintf("Failed to update %s settings", section), http.StatusBadRequest)
	}

	// Check if any important settings have changed and trigger actions as needed
	if err := c.handleSettingsChanges(&oldSettings, settings); err != nil {
		// Attempt to rollback changes if applying them failed
		*settings = oldSettings
		return c.HandleError(ctx, err, "Failed to apply settings changes, rolled back to previous settings", http.StatusInternalServerError)
	}

	// Save settings to disk (unless disabled for tests)
	if !c.DisableSaveSettings {
		if err := conf.SaveSettings(); err != nil {
			// Attempt to rollback changes if saving failed
			*settings = oldSettings
			return c.HandleError(ctx, err, "Failed to save settings, rolled back to previous settings", http.StatusInternalServerError)
		}
	}

	// Update the cached telemetry state after settings change
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

// validateRTSPURLs validates a slice of RTSP URLs
func validateRTSPURLs(urls []string) error {
	for i, urlStr := range urls {
		if urlStr == "" {
			return fmt.Errorf("RTSP URL at index %d cannot be empty", i)
		}

		// Parse the URL to validate its structure
		parsedURL, err := url.Parse(urlStr)
		if err != nil {
			return fmt.Errorf("RTSP URL at index %d is malformed: %w", i, err)
		}

		// Check if the scheme is RTSP
		if parsedURL.Scheme != "rtsp" {
			return fmt.Errorf("RTSP URL at index %d must use rtsp:// scheme, got %s://", i, parsedURL.Scheme)
		}

		// Check if host is present
		if parsedURL.Host == "" {
			return fmt.Errorf("RTSP URL at index %d is missing host", i)
		}

		// Validate that the host part is properly formatted
		// This includes checking for valid hostname or IP address
		if parsedURL.Hostname() == "" {
			return fmt.Errorf("RTSP URL at index %d has invalid hostname", i)
		}

		// If port is specified, validate it's a valid number
		if portStr := parsedURL.Port(); portStr != "" {
			port, err := strconv.Atoi(portStr)
			if err != nil {
				return fmt.Errorf("RTSP URL at index %d has invalid port: %w", i, err)
			}
			if port < 1 || port > 65535 {
				return fmt.Errorf("RTSP URL at index %d has port %d out of valid range (1-65535)", i, port)
			}
		}
	}
	return nil
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
	for k, v := range dst {
		result[k] = v
	}

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

// Helper functions

// getSettingsSectionValue returns a pointer to the requested section of settings for in-place updates
func getSettingsSectionValue(settings *conf.Settings, section string) (any, error) {
	section = strings.ToLower(section)

	// Map section names to their corresponding pointers
	switch section {
	case "birdnet":
		return &settings.BirdNET, nil
	case "webserver":
		return &settings.WebServer, nil
	case "security":
		return &settings.Security, nil
	case "main":
		return &settings.Main, nil
	case "realtime":
		return &settings.Realtime, nil
	case "audio":
		return getAudioSectionValue(settings), nil
	case "dashboard":
		return &settings.Realtime.Dashboard, nil
	case "weather":
		return &settings.Realtime.Weather, nil
	case "mqtt":
		return &settings.Realtime.MQTT, nil
	case "birdweather":
		return &settings.Realtime.Birdweather, nil
	case "species":
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
	// Use mergeJSONIntoStruct to preserve fields not included in the update
	if err := mergeJSONIntoStruct(data, sectionPtr); err != nil {
		return fmt.Errorf("failed to merge settings for section %s: %w", sectionName, err)
	}

	// Apply field-level permissions if needed
	// Note: getBlockedFieldMap uses capitalized section names (e.g., "BirdNET", "Realtime")
	// We need to map our lowercase section names to the expected capitalized format
	capitalizedSectionName := ""
	switch sectionName {
	case "birdnet":
		capitalizedSectionName = "BirdNET"
	case "realtime":
		capitalizedSectionName = "Realtime"
	case "webserver":
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
		"mqtt":      validateMQTTSection,
		"rtsp":      validateRTSPSection,
		"security":  validateSecuritySection,
		"main":      validateMainSection,
		"birdnet":   validateBirdNETSection,
		"webserver": validateWebServerSection,
		"species":   validateSpeciesSection,
		"realtime":  validateRealtimeSection,
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

// validateRTSPSection validates RTSP settings
func validateRTSPSection(data json.RawMessage) error {
	var rtspSettings conf.RTSPSettings
	if err := json.Unmarshal(data, &rtspSettings); err != nil {
		return err
	}

	// Validate RTSP URLs
	return validateRTSPURLs(rtspSettings.URLs)
}

// securitySectionAllowedFields defines which fields in the security section can be updated via API
var securitySectionAllowedFields = map[string]bool{
	"host":              true, // Server hostname for TLS
	"autoTls":           true, // AutoTLS setting
	"basicAuth":         true, // Basic authentication settings
	"googleAuth":        true, // Google OAuth settings
	"githubAuth":        true, // GitHub OAuth settings
	"allowSubnetBypass": true, // Subnet bypass settings
	"redirectToHttps":   true, // HTTPS redirect setting
	// sessionSecret is NOT allowed - it's generated internally
	// sessionDuration is NOT allowed - it's a runtime setting
}

// validateSecuritySection validates security settings
func validateSecuritySection(data json.RawMessage) error {
	// Security settings cannot be updated via API for security reasons
	return fmt.Errorf("security settings cannot be updated via API")
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

	// Validate OAuth settings
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
		// Check clientId
		if clientId, exists := providerMap["clientId"]; exists {
			if str, ok := clientId.(string); !ok || str == "" {
				return fmt.Errorf("%s.clientId is required when enabled", providerName)
			}
		} else if enabled {
			// clientId field missing when provider is enabled
			return fmt.Errorf("%s.clientId is required when enabled", providerName)
		}

		// Check clientSecret
		if clientSecret, exists := providerMap["clientSecret"]; exists {
			if str, ok := clientSecret.(string); !ok || str == "" {
				return fmt.Errorf("%s.clientSecret is required when enabled", providerName)
			}
		} else if enabled {
			// clientSecret field missing when provider is enabled
			return fmt.Errorf("%s.clientSecret is required when enabled", providerName)
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
	// Main settings cannot be updated via API for security reasons
	return fmt.Errorf("main settings cannot be updated via API")
}

// validateMainSectionValues validates the values of main section fields
func validateMainSectionValues(updateMap map[string]any) error {
	// Validate node name
	if name, exists := updateMap["name"]; exists {
		if str, ok := name.(string); ok {
			if str == "" {
				return fmt.Errorf("node name cannot be empty")
			}
			if len(str) > 100 {
				return fmt.Errorf("node name must not exceed 100 characters")
			}
		} else {
			return fmt.Errorf("node name must be a string")
		}
	}

	// Validate timeAs24h
	if timeAs24h, exists := updateMap["timeAs24h"]; exists {
		if _, ok := timeAs24h.(bool); !ok {
			return fmt.Errorf("timeAs24h must be a boolean value")
		}
	}

	return nil
}

// validateBirdNETSection validates BirdNET settings
func validateBirdNETSection(data json.RawMessage) error {
	var updateMap map[string]any
	if err := json.Unmarshal(data, &updateMap); err != nil {
		return err
	}

	// Validate latitude
	if lat, exists := updateMap["latitude"]; exists {
		if latFloat, ok := lat.(float64); ok {
			if latFloat < -90 || latFloat > 90 {
				return fmt.Errorf("latitude must be between -90 and 90")
			}
		}
	}

	// Validate longitude
	if lng, exists := updateMap["longitude"]; exists {
		if lngFloat, ok := lng.(float64); ok {
			if lngFloat < -180 || lngFloat > 180 {
				return fmt.Errorf("longitude must be between -180 and 180")
			}
		}
	}

	return nil
}

// validateWebServerSection validates WebServer settings
func validateWebServerSection(data json.RawMessage) error {
	var updateMap map[string]any
	if err := json.Unmarshal(data, &updateMap); err != nil {
		return err
	}

	// Validate port
	if portValue, exists := updateMap["port"]; exists {
		switch port := portValue.(type) {
		case string:
			portInt, err := strconv.Atoi(port)
			if err != nil {
				return fmt.Errorf("port must be a valid number")
			}
			if portInt < 1 || portInt > 65535 {
				return fmt.Errorf("port must be between 1 and 65535")
			}
		case int:
			if port < 1 || port > 65535 {
				return fmt.Errorf("port must be between 1 and 65535")
			}
		}
	}

	return nil
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

// getSettingsSection returns the requested section of settings
func getSettingsSection(settings *conf.Settings, section string) (any, error) {
	section = strings.ToLower(section)

	// Use reflection to get the field
	settingsValue := reflect.ValueOf(settings).Elem()
	settingsType := settingsValue.Type()

	// Check direct fields first
	for i := 0; i < settingsType.NumField(); i++ {
		field := settingsType.Field(i)
		if strings.EqualFold(field.Name, section) {
			return settingsValue.Field(i).Interface(), nil
		}
	}

	// Check nested fields
	switch section {
	case "birdnet":
		return settings.BirdNET, nil
	case "webserver":
		return settings.WebServer, nil
	case "security":
		return settings.Security, nil
	case "main":
		return settings.Main, nil
	case "realtime":
		return settings.Realtime, nil
	case "audio":
		return getAudioSection(settings), nil
	case "dashboard":
		return settings.Realtime.Dashboard, nil
	case "weather":
		return settings.Realtime.Weather, nil
	case "mqtt":
		return settings.Realtime.MQTT, nil
	case "birdweather":
		return settings.Realtime.Birdweather, nil
	case "species":
		return settings.Realtime.Species, nil
	default:
		return nil, fmt.Errorf("unknown settings section: %s", section)
	}
}

// validateField performs validation on specific fields that require extra checks
// Returns nil if validation passes, error otherwise
func validateField(fieldName string, value any) error {
	switch fieldName {
	case "port":
		// Validate port is in valid range
		switch port := value.(type) {
		case int:
			if port < 1 || port > 65535 {
				return fmt.Errorf("port must be between 1 and 65535")
			}
		case string:
			// Handle string ports (convert to int and validate)
			portInt, err := strconv.Atoi(port)
			if err != nil {
				return fmt.Errorf("port must be a valid number")
			}
			if portInt < 1 || portInt > 65535 {
				return fmt.Errorf("port must be between 1 and 65535")
			}
		}
	case "latitude":
		// Validate latitude range
		if lat, ok := value.(float64); ok {
			if lat < -90 || lat > 90 {
				return fmt.Errorf("latitude must be between -90 and 90")
			}
		}
	case "longitude":
		// Validate longitude range
		if lng, ok := value.(float64); ok {
			if lng < -180 || lng > 180 {
				return fmt.Errorf("longitude must be between -180 and 180")
			}
		}
	case "password":
		// For sensitive fields like passwords, perform additional validation
		// For example, you could check minimum length, complexity, etc.
		if pass, ok := value.(string); ok {
			if pass != "" && len(pass) < 8 {
				return fmt.Errorf("password must be at least 8 characters long")
			}
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

// handleSettingsChanges checks if important settings have changed and triggers appropriate actions
func (c *Controller) handleSettingsChanges(oldSettings, currentSettings *conf.Settings) error {
	// Create a slice to track which reconfigurations need to be performed
	var reconfigActions []string

	// Check BirdNET settings
	if birdnetSettingsChanged(oldSettings, currentSettings) {
		c.Debug("BirdNET settings changed, triggering reload")
		reconfigActions = append(reconfigActions, "reload_birdnet")
		// Send toast notification
		_ = c.SendToast("Reloading BirdNET model with new settings...", "info", 5000)
	}

	// Check range filter settings
	if rangeFilterSettingsChanged(oldSettings, currentSettings) {
		c.Debug("Range filter settings changed, triggering rebuild")
		reconfigActions = append(reconfigActions, "rebuild_range_filter")
		// Send toast notification
		_ = c.SendToast("Rebuilding species range filter...", "info", 4000)
	}

	// Check species interval settings
	if speciesIntervalSettingsChanged(oldSettings, currentSettings) || oldSettings.Realtime.Interval != currentSettings.Realtime.Interval {
		c.Debug("Species interval settings changed, triggering update")
		reconfigActions = append(reconfigActions, "update_detection_intervals")
		// Send toast notification
		_ = c.SendToast("Updating detection intervals...", "info", 3000)
	}

	// Check MQTT settings
	if mqttSettingsChanged(oldSettings, currentSettings) {
		c.Debug("MQTT settings changed, triggering reconfiguration")
		reconfigActions = append(reconfigActions, "reconfigure_mqtt")
		// Send toast notification
		_ = c.SendToast("Reconfiguring MQTT connection...", "info", 4000)
	}

	// Check BirdWeather settings
	if birdWeatherSettingsChanged(oldSettings, currentSettings) {
		c.Debug("BirdWeather settings changed, triggering reconfiguration")
		reconfigActions = append(reconfigActions, "reconfigure_birdweather")
		// Send toast notification
		_ = c.SendToast("Reconfiguring BirdWeather integration...", "info", 4000)
	}

	// Check RTSP settings
	if rtspSettingsChanged(oldSettings, currentSettings) {
		c.Debug("RTSP settings changed, triggering reconfiguration")
		reconfigActions = append(reconfigActions, "reconfigure_rtsp_sources")
		// Send toast notification
		_ = c.SendToast("Reconfiguring RTSP sources...", "info", 4000)
	}

	// Check telemetry settings
	if telemetrySettingsChanged(oldSettings, currentSettings) {
		c.Debug("Telemetry settings changed, triggering reconfiguration")
		reconfigActions = append(reconfigActions, "reconfigure_telemetry")
		// Send toast notification
		_ = c.SendToast("Reconfiguring telemetry settings...", "info", 3000)
	}

	// Handle audio settings changes
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
				// Add a small delay between actions to avoid overwhelming the system
				time.Sleep(100 * time.Millisecond)
			}
		}(reconfigActions)
	}

	return nil
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

// rtspSettingsChanged checks if RTSP settings have changed
func rtspSettingsChanged(oldSettings, currentSettings *conf.Settings) bool {
	oldRTSP := oldSettings.Realtime.RTSP
	newRTSP := currentSettings.Realtime.RTSP

	// Check for changes in RTSP transport protocol
	if oldRTSP.Transport != newRTSP.Transport {
		return true
	}

	// Check for changes in RTSP URLs
	if len(oldRTSP.URLs) != len(newRTSP.URLs) {
		return true
	}

	for i, url := range oldRTSP.URLs {
		if i >= len(newRTSP.URLs) || url != newRTSP.URLs[i] {
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
	c.logAPIRequest(ctx, slog.LevelInfo, "Getting available locales")

	// Return locales in the same format as v1 for compatibility
	// This matches the client-side expectation of key-value pairs
	locales := make(map[string]string)
	for code, name := range conf.LocaleCodes {
		locales[code] = name
	}

	c.logAPIRequest(ctx, slog.LevelInfo, "Retrieved locales successfully", "count", len(locales))

	return ctx.JSON(http.StatusOK, locales)
}

// GetImageProviders handles GET /api/v2/settings/imageproviders
func (c *Controller) GetImageProviders(ctx echo.Context) error {
	c.logAPIRequest(ctx, slog.LevelInfo, "Getting available image providers")

	// Prepare image provider options
	providerOptionList := []ImageProviderOption{
		{Value: "auto", Display: "Auto (Default)"}, // Always add auto first
	}

	providerCount := 0
	if c.BirdImageCache != nil {
		c.logAPIRequest(ctx, slog.LevelDebug, "BirdImageCache is available, checking for registry")
		if registry := c.BirdImageCache.GetRegistry(); registry != nil {
			c.logAPIRequest(ctx, slog.LevelDebug, "Registry found, ranging over providers")
			registry.RangeProviders(func(name string, cache *imageprovider.BirdImageCache) bool {
				c.logAPIRequest(ctx, slog.LevelDebug, "Found provider", "name", name)
				// Simple capitalization for display name (Rune-aware)
				var displayName string
				if name != "" {
					r, size := utf8.DecodeRuneInString(name)
					displayName = strings.ToUpper(string(r)) + name[size:]
				} else {
					displayName = "(unknown)"
				}
				providerOptionList = append(providerOptionList, ImageProviderOption{Value: name, Display: displayName})
				providerCount++
				return true // Continue ranging
			})

			// Sort the providers alphabetically by display name (excluding the first 'auto' entry)
			if len(providerOptionList) > 2 { // Need at least 3 elements to sort the part after 'auto'
				sub := providerOptionList[1:] // Create a sub-slice for sorting
				sort.Slice(sub, func(i, j int) bool {
					return sub[i].Display < sub[j].Display // Compare elements within the sub-slice
				})
			}
		} else {
			c.logAPIRequest(ctx, slog.LevelWarn, "ImageProviderRegistry is nil, cannot get provider names")
		}
	} else {
		c.logAPIRequest(ctx, slog.LevelWarn, "BirdImageCache is nil, cannot get provider names")
	}

	c.logAPIRequest(ctx, slog.LevelInfo, "Retrieved image providers successfully", "count", len(providerOptionList), "provider_count", providerCount)

	// Return in format expected by client: { providers: [...] }
	response := map[string]any{
		"providers": providerOptionList,
	}

	return ctx.JSON(http.StatusOK, response)
}

// GetSystemID handles GET /api/v2/settings/systemid
func (c *Controller) GetSystemID(ctx echo.Context) error {
	c.logAPIRequest(ctx, slog.LevelInfo, "Getting system ID")

	// Acquire read lock to ensure settings aren't being modified during read
	c.settingsMutex.RLock()
	defer c.settingsMutex.RUnlock()

	settings := c.Settings
	if settings == nil {
		// Fallback to global settings if controller settings not set
		settings = conf.Setting()
		if settings == nil {
			c.logAPIRequest(ctx, slog.LevelError, "Settings not initialized when trying to get system ID", "endpoint", "GetSystemID")
			return c.HandleError(ctx, fmt.Errorf("settings not initialized"), "Failed to get settings", http.StatusInternalServerError)
		}
	}

	c.logAPIRequest(ctx, slog.LevelInfo, "Retrieved system ID successfully", "system_id", settings.SystemID)

	// Return system ID in the format expected by the frontend
	response := map[string]string{
		"systemID": settings.SystemID,
	}

	return ctx.JSON(http.StatusOK, response)
}
