// internal/api/v2/settings.go
package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"

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
	settingsGroup.GET("", c.GetAllSettings)
	settingsGroup.GET("/:section", c.GetSectionSettings)
	settingsGroup.PUT("", c.UpdateSettings)
	settingsGroup.PATCH("/:section", c.UpdateSectionSettings)
}

// GetAllSettings handles GET /api/v2/settings
func (c *Controller) GetAllSettings(ctx echo.Context) error {
	settings := conf.Setting()
	if settings == nil {
		return c.HandleError(ctx, fmt.Errorf("settings not initialized"), "Failed to get settings", http.StatusInternalServerError)
	}

	// Return a copy of the settings
	return ctx.JSON(http.StatusOK, settings)
}

// GetSectionSettings handles GET /api/v2/settings/:section
func (c *Controller) GetSectionSettings(ctx echo.Context) error {
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
	// TODO: Implement a more comprehensive update mechanism
	// For now, we'll do a simplified update of main sections

	// Update BirdNET settings
	current.BirdNET.Locale = updated.BirdNET.Locale
	current.BirdNET.Threads = updated.BirdNET.Threads
	current.BirdNET.ModelPath = updated.BirdNET.ModelPath
	current.BirdNET.LabelPath = updated.BirdNET.LabelPath
	current.BirdNET.UseXNNPACK = updated.BirdNET.UseXNNPACK
	current.BirdNET.Latitude = updated.BirdNET.Latitude
	current.BirdNET.Longitude = updated.BirdNET.Longitude

	// Update WebServer settings
	current.WebServer.Port = updated.WebServer.Port
	current.WebServer.Debug = updated.WebServer.Debug

	// Update Realtime settings (selectively)
	current.Realtime.Interval = updated.Realtime.Interval
	current.Realtime.ProcessingTime = updated.Realtime.ProcessingTime

	// Update Audio settings (selectively)
	current.Realtime.Audio.Source = updated.Realtime.Audio.Source
	current.Realtime.Audio.Export.Enabled = updated.Realtime.Audio.Export.Enabled
	current.Realtime.Audio.Export.Path = updated.Realtime.Audio.Export.Path
	current.Realtime.Audio.Export.Type = updated.Realtime.Audio.Export.Type
	current.Realtime.Audio.Export.Bitrate = updated.Realtime.Audio.Export.Bitrate

	// Update EQ settings
	current.Realtime.Audio.Equalizer = updated.Realtime.Audio.Equalizer

	// Update MQTT settings
	current.Realtime.MQTT = updated.Realtime.MQTT

	// Update RTSP settings
	current.Realtime.RTSP = updated.Realtime.RTSP

	// Update Species settings
	current.Realtime.Species.Include = updated.Realtime.Species.Include
	current.Realtime.Species.Exclude = updated.Realtime.Species.Exclude
	current.Realtime.Species.Config = updated.Realtime.Species.Config

	return nil
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
	// Check BirdNET settings
	if birdnetSettingsChanged(oldSettings, currentSettings) {
		c.Debug("BirdNET settings changed, triggering reload")
		c.controlChan <- "reload_birdnet"
	}

	// Check range filter settings
	if rangeFilterSettingsChanged(oldSettings, currentSettings) {
		c.Debug("Range filter settings changed, triggering rebuild")
		c.controlChan <- "rebuild_range_filter"
	}

	// Check MQTT settings
	if mqttSettingsChanged(oldSettings, currentSettings) {
		c.Debug("MQTT settings changed, triggering reconfiguration")
		c.controlChan <- "reconfigure_mqtt"
	}

	// Check RTSP settings
	if rtspSettingsChanged(oldSettings, currentSettings) {
		c.Debug("RTSP settings changed, triggering reconfiguration")
		c.controlChan <- "reconfigure_rtsp_sources"
	}

	// Check audio device settings
	if audioDeviceSettingChanged(oldSettings, currentSettings) {
		c.Debug("Audio device changed. A restart will be required.")
		// No action here as restart is manual
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
