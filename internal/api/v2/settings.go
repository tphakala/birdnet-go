// internal/api/v2/settings.go
package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// UpdateRequest represents a request to update settings
type UpdateRequest struct {
	Path  string      `json:"path"`
	Value interface{} `json:"value"`
}

// initSettingsRoutes registers all settings-related API endpoints
func (c *Controller) initSettingsRoutes() {
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
}

// GetAllSettings handles GET /api/v2/settings
func (c *Controller) GetAllSettings(ctx echo.Context) error {
	// Acquire read lock to ensure settings aren't being modified during read
	c.settingsMutex.RLock()
	defer c.settingsMutex.RUnlock()

	settings := conf.Setting()
	if settings == nil {
		return c.HandleError(ctx, fmt.Errorf("settings not initialized"), "Failed to get settings", http.StatusInternalServerError)
	}

	// Return a copy of the settings
	return ctx.JSON(http.StatusOK, settings)
}

// GetSectionSettings handles GET /api/v2/settings/:section
func (c *Controller) GetSectionSettings(ctx echo.Context) error {
	// Acquire read lock to ensure settings aren't being modified during read
	c.settingsMutex.RLock()
	defer c.settingsMutex.RUnlock()

	section := ctx.Param("section")
	if section == "" {
		return c.HandleError(ctx, fmt.Errorf("section not specified"), "Section parameter is required", http.StatusBadRequest)
	}

	settings := conf.Setting()
	if settings == nil {
		return c.HandleError(ctx, fmt.Errorf("settings not initialized"), "Failed to get settings", http.StatusInternalServerError)
	}

	// Get the settings section
	sectionValue, err := getSettingsSection(settings, section)
	if err != nil {
		return c.HandleError(ctx, err, "Failed to get settings section", http.StatusNotFound)
	}

	return ctx.JSON(http.StatusOK, sectionValue)
}

// UpdateSettings handles PUT /api/v2/settings
func (c *Controller) UpdateSettings(ctx echo.Context) error {
	// Acquire write lock to prevent concurrent settings updates
	c.settingsMutex.Lock()
	defer c.settingsMutex.Unlock()

	settings := conf.Setting()
	if settings == nil {
		return c.HandleError(ctx, fmt.Errorf("settings not initialized"), "Failed to get settings", http.StatusInternalServerError)
	}

	// Store old settings for comparison
	oldSettings := *settings

	// Parse the request body
	var updatedSettings conf.Settings
	if err := ctx.Bind(&updatedSettings); err != nil {
		return c.HandleError(ctx, err, "Failed to parse request body", http.StatusBadRequest)
	}

	// Update only the fields that are allowed to be changed
	// This ensures that runtime-only fields are not overwritten
	if err := updateAllowedSettings(settings, &updatedSettings); err != nil {
		return c.HandleError(ctx, err, "Failed to update settings", http.StatusInternalServerError)
	}

	// Check if any important settings have changed and trigger actions as needed
	if err := c.handleSettingsChanges(&oldSettings, settings); err != nil {
		return c.HandleError(ctx, err, "Failed to apply settings changes", http.StatusInternalServerError)
	}

	// Save settings to disk
	if err := conf.SaveSettings(); err != nil {
		return c.HandleError(ctx, err, "Failed to save settings", http.StatusInternalServerError)
	}

	return ctx.JSON(http.StatusOK, map[string]string{
		"message": "Settings updated successfully",
	})
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

	// Store old settings for comparison
	oldSettings := *settings

	// Parse the request body
	var requestBody json.RawMessage
	if err := ctx.Bind(&requestBody); err != nil {
		return c.HandleError(ctx, err, "Failed to parse request body", http.StatusBadRequest)
	}

	// Update the specific section
	if err := updateSettingsSection(settings, section, requestBody); err != nil {
		return c.HandleError(ctx, err, fmt.Sprintf("Failed to update %s settings", section), http.StatusBadRequest)
	}

	// Check if any important settings have changed and trigger actions as needed
	if err := c.handleSettingsChanges(&oldSettings, settings); err != nil {
		return c.HandleError(ctx, err, "Failed to apply settings changes", http.StatusInternalServerError)
	}

	// Save settings to disk
	if err := conf.SaveSettings(); err != nil {
		return c.HandleError(ctx, err, "Failed to save settings", http.StatusInternalServerError)
	}

	return ctx.JSON(http.StatusOK, map[string]string{
		"message": fmt.Sprintf("%s settings updated successfully", section),
	})
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

// updateAllowedSettings updates only the fields that are allowed to be changed
func updateAllowedSettings(current, updated *conf.Settings) error {
	// Use reflection to dynamically update fields
	return updateAllowedFieldsRecursively(reflect.ValueOf(current).Elem(), reflect.ValueOf(updated).Elem(), getAllowedFieldMap())
}

// updateAllowedFieldsRecursively handles recursive field updates using reflection
func updateAllowedFieldsRecursively(currentValue, updatedValue reflect.Value, allowedFields map[string]interface{}) error {
	if currentValue.Kind() != reflect.Struct || updatedValue.Kind() != reflect.Struct {
		return fmt.Errorf("both values must be structs")
	}

	for i := 0; i < currentValue.NumField(); i++ {
		fieldName := currentValue.Type().Field(i).Name
		currentField := currentValue.Field(i)

		// Check if this field exists in the updated struct
		updatedField := updatedValue.FieldByName(fieldName)
		if !updatedField.IsValid() {
			continue
		}

		// Check if this field is in the allowed fields map
		allowedSubfields, isAllowed := allowedFields[fieldName].(map[string]interface{})

		if !isAllowed {
			// If it's a bool in the map, it means the whole field is allowed (if true)
			isAllowedBool, isBool := allowedFields[fieldName].(bool)
			if !isBool || !isAllowedBool {
				continue // Skip this field
			}

			// The entire field is allowed to be updated
			if currentField.CanSet() {
				currentField.Set(updatedField)
			}
			continue
		}

		// For struct fields, recursively update allowed subfields
		if currentField.Kind() == reflect.Struct && updatedField.Kind() == reflect.Struct {
			if err := updateAllowedFieldsRecursively(currentField, updatedField, allowedSubfields); err != nil {
				return err
			}
			continue
		}

		// For fields that are pointers to structs
		if currentField.Kind() == reflect.Ptr && updatedField.Kind() == reflect.Ptr {
			if currentField.IsNil() && !updatedField.IsNil() {
				// Create a new struct of the appropriate type
				newStruct := reflect.New(currentField.Type().Elem())
				currentField.Set(newStruct)
			}

			if !currentField.IsNil() && !updatedField.IsNil() {
				if currentField.Elem().Kind() == reflect.Struct && updatedField.Elem().Kind() == reflect.Struct {
					if err := updateAllowedFieldsRecursively(currentField.Elem(), updatedField.Elem(), allowedSubfields); err != nil {
						return err
					}
				}
			}
			continue
		}

		// Update primitive fields or slices that are in the allowed list
		if currentField.CanSet() {
			currentField.Set(updatedField)
		}
	}

	return nil
}

// getAllowedFieldMap returns a map of fields that are allowed to be updated
// The structure uses nested maps to represent the structure of the settings
// true means the whole field is allowed, a nested map means only specific subfields are allowed
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

// updateSettingsSection updates a specific section of the settings
func updateSettingsSection(settings *conf.Settings, section string, data json.RawMessage) error {
	section = strings.ToLower(section)

	switch section {
	case "birdnet":
		return json.Unmarshal(data, &settings.BirdNET)
	case "webserver":
		return json.Unmarshal(data, &settings.WebServer)
	case "security":
		return json.Unmarshal(data, &settings.Security)
	case "main":
		return json.Unmarshal(data, &settings.Main)
	case "audio":
		return json.Unmarshal(data, &settings.Realtime.Audio)
	case "dashboard":
		return json.Unmarshal(data, &settings.Realtime.Dashboard)
	case "weather":
		return json.Unmarshal(data, &settings.Realtime.Weather)
	case "mqtt":
		return json.Unmarshal(data, &settings.Realtime.MQTT)
	case "birdweather":
		return json.Unmarshal(data, &settings.Realtime.Birdweather)
	case "species":
		return json.Unmarshal(data, &settings.Realtime.Species)
	default:
		return fmt.Errorf("unknown settings section: %s", section)
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
				c.Debug("Asynchronously executing action: " + action)
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
