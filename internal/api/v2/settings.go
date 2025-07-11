// internal/api/v2/settings.go
package api

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/telemetry"
)

// UpdateRequest represents a request to update settings
type UpdateRequest struct {
	Path  string      `json:"path"`
	Value interface{} `json:"value"`
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

	settings := conf.Setting()
	if settings == nil {
		if c.apiLogger != nil {
			c.apiLogger.Error("Settings not initialized when trying to get all settings",
				"path", ctx.Request().URL.Path,
				"ip", ctx.RealIP(),
			)
		}
		return c.HandleError(ctx, fmt.Errorf("settings not initialized"), "Failed to get settings", http.StatusInternalServerError)
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

	settings := conf.Setting()
	if settings == nil {
		c.logAPIRequest(ctx, slog.LevelError, "Settings not initialized when trying to get section settings", "section", section)
		return c.HandleError(ctx, fmt.Errorf("settings not initialized"), "Failed to get settings", http.StatusInternalServerError)
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

	settings := conf.Setting()
	if settings == nil {
		c.logAPIRequest(ctx, slog.LevelError, "Settings not initialized during update attempt")
		return c.HandleError(ctx, fmt.Errorf("settings not initialized"), "Failed to get settings", http.StatusInternalServerError)
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
	return ctx.JSON(http.StatusOK, map[string]interface{}{
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
	switch v := interface{}(settings.WebServer.Port).(type) {
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
		getAllowedFieldMap(),
		&skippedFields,
		"",
	)
	return skippedFields, err
}

// updateAllowedFieldsRecursivelyWithTracking handles recursive field updates and tracks skipped fields
func updateAllowedFieldsRecursivelyWithTracking(
	currentValue, updatedValue reflect.Value,
	allowedFields map[string]interface{},
	skippedFields *[]string,
	prefix string,
) error {
	if currentValue.Kind() != reflect.Struct || updatedValue.Kind() != reflect.Struct {
		return fmt.Errorf("both values must be structs")
	}

	for i := 0; i < currentValue.NumField(); i++ {
		fieldName := currentValue.Type().Field(i).Name
		currentField := currentValue.Field(i)

		// Get updated field and skip if not valid
		updatedField := updatedValue.FieldByName(fieldName)
		if !updatedField.IsValid() {
			continue
		}

		// Get field info (path and json tag)
		fieldPath, jsonTag := getFieldInfo(currentValue, i, fieldName, prefix)

		// Process the field based on permissions and type
		if err := processField(currentField, updatedField, fieldName, fieldPath, jsonTag,
			allowedFields, skippedFields); err != nil {
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
	allowedFields map[string]interface{},
	skippedFields *[]string,
) error {
	// Check field permissions
	allowedSubfields, isAllowedAsMap := allowedFields[fieldName].(map[string]interface{})

	if !isAllowedAsMap {
		// Handle field based on permission (if it's a simple boolean permission)
		return handleFieldPermission(currentField, updatedField, fieldName, fieldPath, jsonTag,
			allowedFields, skippedFields)
	}

	// Handle field based on its type (struct, pointer, or primitive)
	return handleFieldByType(currentField, updatedField, fieldName, fieldPath, jsonTag,
		allowedSubfields, skippedFields)
}

// handleFieldPermission processes a field based on its permission settings
func handleFieldPermission(
	currentField, updatedField reflect.Value,
	fieldName, fieldPath, jsonTag string,
	allowedFields map[string]interface{},
	skippedFields *[]string,
) error {
	// If it's a bool in the map, it means the whole field is allowed (if true)
	isAllowedBool, isBool := allowedFields[fieldName].(bool)
	if !isBool || !isAllowedBool {
		// Field is explicitly not allowed to be updated
		*skippedFields = append(*skippedFields, fieldPath)
		return nil // Skip this field
	}

	// The entire field is allowed to be updated
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
	allowedSubfields map[string]interface{},
	skippedFields *[]string,
) error {
	// For struct fields
	if currentField.Kind() == reflect.Struct && updatedField.Kind() == reflect.Struct {
		return handleStructField(currentField, updatedField, fieldPath, allowedSubfields, skippedFields)
	}

	// For fields that are pointers to structs
	if currentField.Kind() == reflect.Ptr && updatedField.Kind() == reflect.Ptr {
		return handlePointerField(currentField, updatedField, fieldPath, allowedSubfields, skippedFields)
	}

	// For primitive fields or other types
	return handlePrimitiveField(currentField, updatedField, fieldName, jsonTag)
}

// handleStructField handles struct fields recursively
func handleStructField(
	currentField, updatedField reflect.Value,
	fieldPath string,
	allowedSubfields map[string]interface{},
	skippedFields *[]string,
) error {
	return updateAllowedFieldsRecursivelyWithTracking(
		currentField,
		updatedField,
		allowedSubfields,
		skippedFields,
		fieldPath,
	)
}

// handlePointerField handles pointer fields, including nil pointer cases
func handlePointerField(
	currentField, updatedField reflect.Value,
	fieldPath string,
	allowedSubfields map[string]interface{},
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
				allowedSubfields,
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

	settings := conf.Setting()
	if settings == nil {
		return c.HandleError(ctx, fmt.Errorf("settings not initialized"), "Failed to get settings", http.StatusInternalServerError)
	}

	// Create a backup of current settings for rollback if needed
	oldSettings := *settings

	// Parse the request body
	var requestBody json.RawMessage
	if err := ctx.Bind(&requestBody); err != nil {
		return c.HandleError(ctx, err, "Failed to parse request body", http.StatusBadRequest)
	}

	// Validate that the request body contains valid JSON
	var tempValue interface{}
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

	// Save settings to disk
	if err := conf.SaveSettings(); err != nil {
		// Attempt to rollback changes if saving failed
		*settings = oldSettings
		return c.HandleError(ctx, err, "Failed to save settings, rolled back to previous settings", http.StatusInternalServerError)
	}

	// Update the cached telemetry state after settings change
	telemetry.UpdateTelemetryEnabled()

	return ctx.JSON(http.StatusOK, map[string]interface{}{
		"message":       fmt.Sprintf("%s settings updated successfully", section),
		"skippedFields": skippedFields,
	})
}

// sectionHandler defines a function that handles updates for a specific settings section
type sectionHandler func(settings *conf.Settings, data json.RawMessage, skippedFields *[]string) error

// getSectionHandlers returns a map of section names to their handler functions
func getSectionHandlers() map[string]sectionHandler {
	return map[string]sectionHandler{
		"birdnet":     handleBirdnetSection,
		"webserver":   handleWebServerSection,
		"security":    handleSecuritySection,
		"main":        handleMainSection,
		"audio":       handleAudioSection,
		"mqtt":        handleMQTTSection,
		"rtsp":        handleRTSPSection,
		"species":     handleSpeciesSection,
		"dashboard":   handleDashboardSection,
		"weather":     handleWeatherSection,
		"birdweather": handleBirdweatherSection,
	}
}

// updateSettingsSectionWithTracking updates a specific section of the settings and tracks skipped fields
func updateSettingsSectionWithTracking(settings *conf.Settings, section string, data json.RawMessage, skippedFields *[]string) error {
	section = strings.ToLower(section)

	var tempValue interface{}
	if err := json.Unmarshal(data, &tempValue); err != nil {
		return fmt.Errorf("invalid JSON for section %s: %w", section, err)
	}

	// Get the handler for this section
	handlers := getSectionHandlers()
	handler, exists := handlers[section]
	if !exists {
		return fmt.Errorf("unknown settings section: %s", section)
	}

	// Execute the handler
	return handler(settings, data, skippedFields)
}

// handleBirdnetSection handles updates to the BirdNET section
func handleBirdnetSection(settings *conf.Settings, data json.RawMessage, skippedFields *[]string) error {
	// Create a temporary copy for filtering
	tempSettings := settings.BirdNET

	// Apply the allowed fields filter using reflection
	if err := json.Unmarshal(data, &tempSettings); err != nil {
		return err
	}

	// Get the allowed fields for this section
	allowedFieldsMap := getAllowedFieldMap()
	birdnetAllowedFields, _ := allowedFieldsMap["BirdNET"].(map[string]interface{})

	// Apply the allowed fields filter using reflection
	return updateAllowedFieldsRecursivelyWithTracking(
		reflect.ValueOf(&settings.BirdNET).Elem(),
		reflect.ValueOf(&tempSettings).Elem(),
		birdnetAllowedFields,
		skippedFields,
		"BirdNET",
	)
}

// handleWebServerSection handles updates to the WebServer section
func handleWebServerSection(settings *conf.Settings, data json.RawMessage, skippedFields *[]string) error {
	// Create a temporary copy for filtering
	webServerSettings := settings.WebServer

	// Unmarshal data into the temporary copy
	if err := json.Unmarshal(data, &webServerSettings); err != nil {
		return err
	}

	allowedFieldsMap := getAllowedFieldMap()
	webserverAllowedFields, _ := allowedFieldsMap["WebServer"].(map[string]interface{})

	return updateAllowedFieldsRecursivelyWithTracking(
		reflect.ValueOf(&settings.WebServer).Elem(),
		reflect.ValueOf(&webServerSettings).Elem(),
		webserverAllowedFields,
		skippedFields,
		"WebServer",
	)
}

// handleSecuritySection handles updates to the Security section
func handleSecuritySection(settings *conf.Settings, data json.RawMessage, skippedFields *[]string) error {
	// Security settings are sensitive and should have very limited updateable fields
	// For now, we're not allowing direct updates to security settings via the API
	return fmt.Errorf("direct updates to security section are not supported for security reasons")
}

// handleMainSection handles updates to the Main section
func handleMainSection(settings *conf.Settings, data json.RawMessage, skippedFields *[]string) error {
	// Create a temporary copy for filtering
	mainSettings := settings.Main

	// Unmarshal data into the temporary copy
	if err := json.Unmarshal(data, &mainSettings); err != nil {
		return err
	}

	// Here you would define which Main fields can be updated
	// For now, we'll use an empty map to prevent any updates
	mainFields := []string{"Main settings cannot be updated via API"}
	*skippedFields = append(*skippedFields, mainFields...)
	return fmt.Errorf("main settings cannot be updated via API")
}

// handleAudioSection handles updates to the Audio section
func handleAudioSection(settings *conf.Settings, data json.RawMessage, skippedFields *[]string) error {
	// Create a temporary copy for filtering
	audioSettings := settings.Realtime.Audio

	// Unmarshal data into the temporary copy
	if err := json.Unmarshal(data, &audioSettings); err != nil {
		return err
	}

	allowedFieldsMap := getAllowedFieldMap()
	realtimeAllowedFields, _ := allowedFieldsMap["Realtime"].(map[string]interface{})
	audioAllowedFields, _ := realtimeAllowedFields["Audio"].(map[string]interface{})

	return updateAllowedFieldsRecursivelyWithTracking(
		reflect.ValueOf(&settings.Realtime.Audio).Elem(),
		reflect.ValueOf(&audioSettings).Elem(),
		audioAllowedFields,
		skippedFields,
		"Realtime.Audio",
	)
}

// handleMQTTSection handles updates to the MQTT section
func handleMQTTSection(settings *conf.Settings, data json.RawMessage, skippedFields *[]string) error {
	// Validate MQTT settings before applying
	mqttSettings := settings.Realtime.MQTT

	// Unmarshal data into the temporary copy
	if err := json.Unmarshal(data, &mqttSettings); err != nil {
		return err
	}

	// Perform any additional validation on MQTT settings
	// For example, checking broker URL format, etc.
	if mqttSettings.Enabled && mqttSettings.Broker == "" {
		return fmt.Errorf("broker is required when MQTT is enabled")
	}

	// MQTT is allowed to be fully replaced according to getAllowedFieldMap
	settings.Realtime.MQTT = mqttSettings
	return nil
}

// handleRTSPSection handles updates to the RTSP section
func handleRTSPSection(settings *conf.Settings, data json.RawMessage, skippedFields *[]string) error {
	// Validate RTSP settings before applying
	rtspSettings := settings.Realtime.RTSP

	// Unmarshal data into the temporary copy
	if err := json.Unmarshal(data, &rtspSettings); err != nil {
		return err
	}

	// Validate RTSP URLs
	if err := validateRTSPURLs(rtspSettings.URLs); err != nil {
		return err
	}

	// RTSP is allowed to be fully replaced according to getAllowedFieldMap
	settings.Realtime.RTSP = rtspSettings
	return nil
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

// handleSpeciesSection handles updates to the Species section
func handleSpeciesSection(settings *conf.Settings, data json.RawMessage, skippedFields *[]string) error {
	// Create a temporary copy
	speciesSettings := settings.Realtime.Species

	// Unmarshal data into the temporary copy
	if err := json.Unmarshal(data, &speciesSettings); err != nil {
		return err
	}

	allowedFieldsMap := getAllowedFieldMap()
	realtimeAllowedFields, _ := allowedFieldsMap["Realtime"].(map[string]interface{})
	speciesAllowedFields, _ := realtimeAllowedFields["Species"].(map[string]interface{})

	return updateAllowedFieldsRecursivelyWithTracking(
		reflect.ValueOf(&settings.Realtime.Species).Elem(),
		reflect.ValueOf(&speciesSettings).Elem(),
		speciesAllowedFields,
		skippedFields,
		"Realtime.Species",
	)
}

// handleDashboardSection handles updates to the Dashboard section
func handleDashboardSection(settings *conf.Settings, data json.RawMessage, skippedFields *[]string) error {
	// Dashboard settings are considered safe for full updates as they contain only display preferences
	// No sensitive configuration like authentication or system paths are involved
	tempDashboardSettings := settings.Realtime.Dashboard
	if err := json.Unmarshal(data, &tempDashboardSettings); err != nil {
		return err
	}
	settings.Realtime.Dashboard = tempDashboardSettings
	return nil
}

// handleWeatherSection handles updates to the Weather section
func handleWeatherSection(settings *conf.Settings, data json.RawMessage, skippedFields *[]string) error {
	// Weather settings are considered safe for full updates as they contain only API configuration
	// No sensitive configuration like authentication keys or system paths are involved
	tempWeatherSettings := settings.Realtime.Weather
	if err := json.Unmarshal(data, &tempWeatherSettings); err != nil {
		return err
	}
	settings.Realtime.Weather = tempWeatherSettings
	return nil
}

// handleBirdweatherSection handles updates to the Birdweather section
func handleBirdweatherSection(settings *conf.Settings, data json.RawMessage, skippedFields *[]string) error {
	// Birdweather settings are considered safe for full updates as they contain only service configuration
	// No sensitive configuration like authentication keys or system paths are involved
	tempBirdweatherSettings := settings.Realtime.Birdweather
	if err := json.Unmarshal(data, &tempBirdweatherSettings); err != nil {
		return err
	}
	settings.Realtime.Birdweather = tempBirdweatherSettings
	return nil
}

// Helper functions

// getSettingsSection returns the requested section of settings
func getSettingsSection(settings *conf.Settings, section string) (interface{}, error) {
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
		return settings.Realtime.Audio, nil
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
func validateField(fieldName string, value interface{}) error {
	switch fieldName {
	case "Port":
		// Validate port is in valid range
		if port, ok := value.(int); ok {
			if port < 1 || port > 65535 {
				return fmt.Errorf("port must be between 1 and 65535")
			}
		}
	case "Latitude":
		// Validate latitude range
		if lat, ok := value.(float64); ok {
			if lat < -90 || lat > 90 {
				return fmt.Errorf("latitude must be between -90 and 90")
			}
		}
	case "Longitude":
		// Validate longitude range
		if lng, ok := value.(float64); ok {
			if lng < -180 || lng > 180 {
				return fmt.Errorf("longitude must be between -180 and 180")
			}
		}
	case "Password":
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

// getAllowedFieldMap returns a map of fields that are allowed to be updated
// The structure uses nested maps to represent the structure of the settings
// true means the whole field is allowed, a nested map means only specific subfields are allowed
//
// IMPORTANT: This is a critical security mechanism for preventing sensitive or runtime-only
// fields from being modified via the API. When adding new fields to the Settings struct:
//  1. Fields NOT in this map will be automatically protected (default deny)
//  2. Add new user-configurable fields explicitly to this map
//  3. NEVER add sensitive data fields (credentials, tokens, etc.) or runtime-state fields here
//     unless they are explicitly designed to be configured via the API
//  4. For nested structures, use nested maps to allow only specific subfields
func getAllowedFieldMap() map[string]interface{} {
	return map[string]interface{}{
		"BirdNET": map[string]interface{}{
			"Locale":     true,
			"Threads":    true,
			"ModelPath":  true,
			"LabelPath":  true,
			"UseXNNPACK": true,
			"Latitude":   true,
			"Longitude":  true,
		},
		"WebServer": map[string]interface{}{
			"Port":  true,
			"Debug": true,
		},
		"Realtime": map[string]interface{}{
			"Interval":       true,
			"ProcessingTime": true,
			"Audio": map[string]interface{}{
				"Source": true,
				"Export": map[string]interface{}{
					"Enabled": true,
					"Path":    true,
					"Type":    true,
					"Bitrate": true,
				},
				"Equalizer": true,
			},
			"MQTT": true, // Allow complete update of MQTT settings
			"RTSP": true, // Allow complete update of RTSP settings
			"Species": map[string]interface{}{
				"Include": true,
				"Exclude": true,
				"Config":  true,
			},
		},
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
	}

	// Check range filter settings
	if rangeFilterSettingsChanged(oldSettings, currentSettings) {
		c.Debug("Range filter settings changed, triggering rebuild")
		reconfigActions = append(reconfigActions, "rebuild_range_filter")
	}

	// Check MQTT settings
	if mqttSettingsChanged(oldSettings, currentSettings) {
		c.Debug("MQTT settings changed, triggering reconfiguration")
		reconfigActions = append(reconfigActions, "reconfigure_mqtt")
	}

	// Check RTSP settings
	if rtspSettingsChanged(oldSettings, currentSettings) {
		c.Debug("RTSP settings changed, triggering reconfiguration")
		reconfigActions = append(reconfigActions, "reconfigure_rtsp_sources")
	}

	// Check audio device settings
	if audioDeviceSettingChanged(oldSettings, currentSettings) {
		c.Debug("Audio device changed. A restart will be required.")
		// No action here as restart is manual
	}

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
	// Check for changes in BirdNET latitude
	if oldSettings.BirdNET.Latitude != currentSettings.BirdNET.Latitude {
		return true
	}

	// Check for changes in BirdNET longitude
	if oldSettings.BirdNET.Longitude != currentSettings.BirdNET.Longitude {
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
		oldMQTT.Password != newMQTT.Password
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

// audioDeviceSettingChanged checks if audio device settings have changed
func audioDeviceSettingChanged(oldSettings, currentSettings *conf.Settings) bool {
	return oldSettings.Realtime.Audio.Source != currentSettings.Realtime.Audio.Source
}
