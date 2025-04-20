package handlers

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/myaudio"
)

// fieldsToSkip is a map of fields that should not be updated from the form
// most of these are runtime settings that are dynamically generated
// some are file analysis settings which do not apply to realtime analysis
var fieldsToSkip = map[string]bool{
	"birdnet.rangefilter.species":     true,
	"birdnet.rangefilter.lastupdated": true,
	"audio.soxaudiotypes":             true,
	"input.path":                      true,
	"input.recursive":                 true,
	"output.file.enabled":             true,
	"output.file.path":                true,
	"output.file.type":                true,
}

// GetAudioDevices handles the request to list available audio devices
// API: GET /api/v1/settings/audio/get
func (h *Handlers) GetAudioDevices(c echo.Context) error {
	devices, err := myaudio.ListAudioSources()

	fmt.Println("Devices:", devices)

	if err != nil {
		log.Println("Error listing audio devices:", err)
		return h.NewHandlerError(err, "Failed to list audio devices", http.StatusInternalServerError)
	}

	return c.JSON(http.StatusOK, devices)
}

// SaveSettings handles the request to save settings
// API: POST /api/v1/settings/save
func (h *Handlers) SaveSettings(c echo.Context) error {
	settings := conf.Setting()
	if settings == nil {
		return h.NewHandlerError(fmt.Errorf("settings is nil"), "Settings not initialized", http.StatusInternalServerError)
	}

	// Store old settings for comparison
	oldSettings := *settings

	formParams, err := c.FormParams()
	if err != nil {
		return h.NewHandlerError(err, "Failed to parse form", http.StatusBadRequest)
	}

	// Update settings from form parameters
	if err := updateSettingsFromForm(settings, formParams); err != nil {
		//log.Printf("Debug: Form parameters for species config: %+v", formParams["realtime.species.config"])
		return h.NewHandlerError(err, "Error updating settings", http.StatusInternalServerError)
	}

	// Check if BirdNET settings have changed
	if birdnetSettingsChanged(&oldSettings, settings) {
		h.SSE.SendNotification(Notification{
			Message: "Reloading BirdNET model...",
			Type:    "info",
		})

		h.controlChan <- "reload_birdnet"
	}

	// Check if range filter related settings have changed
	if rangeFilterSettingsChanged(&oldSettings, settings) {
		h.SSE.SendNotification(Notification{
			Message: "Rebuilding range filter...",
			Type:    "info",
		})
		h.controlChan <- "rebuild_range_filter"
	}

	// Check if MQTT settings have changed
	if mqttSettingsChanged(&oldSettings, settings) {
		h.SSE.SendNotification(Notification{
			Message: "Reconfiguring MQTT connection...",
			Type:    "info",
		})
		h.controlChan <- "reconfigure_mqtt"
	}

	// Check if BirdWeather settings have changed
	if birdWeatherSettingsChanged(&oldSettings, settings) {
		h.SSE.SendNotification(Notification{
			Message: "Reconfiguring BirdWeather integration...",
			Type:    "info",
		})
		h.controlChan <- "reconfigure_birdweather"
	}

	// Check if RTSP settings were included in the form and have changed
	if hasRTSPSettings(formParams) && rtspSettingsChanged(&oldSettings, settings) {
		h.SSE.SendNotification(Notification{
			Message: "Reconfiguring RTSP sources...",
			Type:    "info",
		})
		h.controlChan <- "reconfigure_rtsp_sources"
	}

	// Check if audio device settings have changed
	if audioDeviceSettingChanged(&oldSettings, settings) {
		h.SSE.SendNotification(Notification{
			Message: "Audio device changed. Please restart the application for the changes to take effect.",
			Type:    "warning",
		})
	}

	// Check the authentication settings and update if needed
	h.updateAuthenticationSettings(settings)

	// Check if audio equalizer settings have changed
	if equalizerSettingsChanged(oldSettings.Realtime.Audio.Equalizer, settings.Realtime.Audio.Equalizer) {
		if err := myaudio.UpdateFilterChain(settings); err != nil {
			h.SSE.SendNotification(Notification{
				Message: fmt.Sprintf("Error updating audio EQ filters: %v", err),
				Type:    "error",
			})
			return h.NewHandlerError(err, "Failed to update audio EQ filters", http.StatusInternalServerError)
		}
	}

	// Save settings to YAML file
	if err := conf.SaveSettings(); err != nil {
		h.SSE.SendNotification(Notification{
			Message: fmt.Sprintf("Error saving settings: %v", err),
			Type:    "error",
		})
		return h.NewHandlerError(err, "Failed to save settings", http.StatusInternalServerError)
	}

	// Send success notification for applying and saving settings
	h.SSE.SendNotification(Notification{
		Message: "Settings saved and applied",
		Type:    "success",
	})

	return c.NoContent(http.StatusOK)
}

func formatAndValidateHost(host string, useHTTPS bool) (string, error) {
	protocol := "http"
	if useHTTPS {
		protocol = "https"
	}

	host = strings.TrimRight(host, "/")
	if !strings.HasPrefix(host, "http") {
		host = fmt.Sprintf("%s://%s", protocol, host)
	}

	parsedHost, err := url.Parse(host)
	if err != nil || parsedHost.Host == "" {
		return "", fmt.Errorf("Invalid host address")
	}

	return host, nil
}

func (h *Handlers) updateAuthenticationSettings(settings *conf.Settings) {
	basicAuth := &settings.Security.BasicAuth

	// Check if any authentication settings are enabled
	if !settings.Security.GoogleAuth.Enabled && !settings.Security.GithubAuth.Enabled && !basicAuth.Enabled {
		return
	}

	// Format and validate the host address
	host, err := formatAndValidateHost(settings.Security.Host, settings.Security.RedirectToHTTPS)
	if err != nil {
		h.SSE.SendNotification(Notification{
			Message: err.Error(),
			Type:    "error",
		})
		return
	}

	settings.Security.BasicAuth.RedirectURI = host
	settings.Security.GoogleAuth.RedirectURI = fmt.Sprintf("%s/auth/google/callback", host)
	settings.Security.GithubAuth.RedirectURI = fmt.Sprintf("%s/auth/github/callback", host)

	// Generate secrets if they are empty
	if basicAuth.Enabled {
		if basicAuth.ClientID == "" {
			basicAuth.ClientID = conf.GenerateRandomSecret()
		}
		if basicAuth.ClientSecret == "" {
			basicAuth.ClientSecret = conf.GenerateRandomSecret()
		}
		if basicAuth.AuthCodeExp == 0 {
			basicAuth.AuthCodeExp = 10 * time.Minute
		}
		if basicAuth.AccessTokenExp == 0 {
			basicAuth.AccessTokenExp = 1 * time.Hour
		}
	}

	// Generate a random session secret for Gothic
	if settings.Security.SessionSecret == "" {
		settings.Security.SessionSecret = conf.GenerateRandomSecret()
	}

	h.OAuth2Server.UpdateProviders()
}

// updateSettingsFromForm updates the settings based on form values
func updateSettingsFromForm(settings *conf.Settings, formValues map[string][]string) error {
	// Delegate the update process to updateStructFromForm
	return updateStructFromForm(reflect.ValueOf(settings).Elem(), formValues, "")
}

// updateStructFromForm recursively updates a struct's fields from form values
func updateStructFromForm(v reflect.Value, formValues map[string][]string, prefix string) error { //nolint:gocognit // ignore gocognit warning for this function, maybe refactor later
	t := v.Type()

	// Iterate through all fields of the struct
	for i := 0; i < v.NumField(); i++ {
		field := v.Field(i)
		fieldType := t.Field(i)
		fieldName := fieldType.Name

		// Skip fields that cannot be set
		if !field.CanSet() {
			continue
		}

		// Construct the full name of the field
		fullName := strings.ToLower(prefix + fieldName)

		// Skip fields that should not be updated from the form, eg. fields containing runtime values
		if fieldsToSkip[fullName] {
			continue
		}

		// Handle struct fields
		if field.Kind() == reflect.Struct {
			// Special handling for Audio Equalizer field
			if fieldType.Type == reflect.TypeOf(conf.AudioSettings{}.Equalizer) { //nolint:gocritic // ignore gocritic warning for this if statement, maybe refactor later
				// Only update equalizer if related form values exist
				if hasEqualizerFormValues(formValues, fullName) {
					if err := updateEqualizerFromForm(field, formValues, fullName); err != nil {
						return err
					}
				}
			} else if fieldType.Type == reflect.TypeOf(conf.SpeciesConfig{}) {
				// Special handling for SpeciesConfig
				if configJSON, exists := formValues[fullName]; exists && len(configJSON) > 0 {
					var config conf.SpeciesConfig
					if err := json.Unmarshal([]byte(configJSON[0]), &config); err != nil {
						return fmt.Errorf("error unmarshaling species config for %s: %w", fullName, err)
					}
					field.Set(reflect.ValueOf(config))
				}
			} else {
				//log.Println("Debug (updateStructFromForm): Updating struct field:", fullName)
				// For other structs, recursively update their fields
				if err := updateStructFromForm(field, formValues, fullName+"."); err != nil {
					//log.Println("Debug (updateStructFromForm): Error updating struct field:", err)
					return err
				}
			}
			continue
		}

		// Get the form value for this field
		formValue, exists := formValues[fullName]

		// DEBUG: Log the field name and form value
		//log.Printf("%s: %v", fullName, formValue)

		// If the form value doesn't exist for this field
		if !exists {
			continue
		}

		// Update the field based on its type
		switch field.Kind() {
		case reflect.String:
			// If the form value is not empty, set the string field
			if len(formValue) > 0 {
				field.SetString(formValue[0])
			}
		case reflect.Bool:
			// Set boolean field based on form value
			boolValue := false
			if len(formValue) > 0 {
				boolValue = formValue[0] == "on" || formValue[0] == "true"
			}
			field.SetBool(boolValue)
		case reflect.Int, reflect.Int64:
			// Parse and set integer field if form value exists
			if len(formValue) > 0 {
				intValue, err := strconv.ParseInt(formValue[0], 10, 64)
				if err != nil {
					return fmt.Errorf("invalid integer value for %s: %w", fullName, err)
				}
				field.SetInt(intValue)
			}
		case reflect.Float32, reflect.Float64:
			// Parse and set float field if form value exists
			if len(formValue) > 0 {
				floatValue, err := strconv.ParseFloat(formValue[0], 64)
				if err != nil {
					return fmt.Errorf("invalid float value for %s: %w", fullName, err)
				}
				if field.Kind() == reflect.Float32 {
					field.SetFloat(float64(float32(floatValue)))
				} else {
					field.SetFloat(floatValue)
				}
			}
		case reflect.Slice:
			// Handle slice fields
			if fieldType.Type.Elem().Kind() == reflect.String {
				// Handle string slice (e.g., species lists)
				if len(formValue) > 0 {
					var stringSlice []string
					err := json.Unmarshal([]byte(formValue[0]), &stringSlice)
					if err != nil {
						return fmt.Errorf("error unmarshaling JSON for %s: %w", fullName, err)
					}
					field.Set(reflect.ValueOf(stringSlice))
				}
			} else {
				// Handle other slice types
				if err := updateSliceFromForm(field, formValue); err != nil {
					return fmt.Errorf("error updating slice for %s: %w", fullName, err)
				}
			}
		case reflect.Struct:
			// Handle struct fields
			if fieldType.Type == reflect.TypeOf(conf.AudioSettings{}.Equalizer) {
				// Special handling for Audio Equalizer field
				if err := updateEqualizerFromForm(field, formValues, fullName); err != nil {
					return err
				}
			} else {
				// Recursively update nested structs
				if err := updateStructFromForm(field, formValues, fullName+"."); err != nil {
					return err
				}
			}
		case reflect.Map:
			// Handle map fields
			if fieldType.Type == reflect.TypeOf(map[string]conf.SpeciesConfig{}) {
				// Special handling for species config map
				if configJSON, exists := formValues[fullName]; exists && len(configJSON) > 0 {
					var configs map[string]conf.SpeciesConfig
					if err := json.Unmarshal([]byte(configJSON[0]), &configs); err != nil {
						// Add more detailed error logging
						//log.Printf("Debug: Failed to unmarshal species config JSON: %s", configJSON[0])
						return fmt.Errorf("error unmarshaling species configs for %s: %w", fullName, err)
					}

					// Clean up the Actions data before setting
					for species, config := range configs {
						for i := range config.Actions {
							// Ensure Parameters is properly initialized as a string slice
							if config.Actions[i].Parameters == nil {
								config.Actions[i].Parameters = []string{}
							}
						}
						configs[species] = config
					}

					field.Set(reflect.ValueOf(configs))
				}
			} else {
				return fmt.Errorf("unsupported map type for %s", fullName)
			}
		default:
			// Return error for unsupported field types
			return fmt.Errorf("unsupported field type for %s", fullName)
		}
	}

	return nil
}

func hasEqualizerFormValues(formValues map[string][]string, prefix string) bool {
	// Check for the main Equalizer enabled field
	if _, exists := formValues[prefix+".enabled"]; exists {
		return true
	}

	// Check for any filter-related fields
	filterPrefix := prefix + ".filters["
	for key := range formValues {
		if strings.HasPrefix(key, filterPrefix) {
			// Extract the filter index and field name
			parts := strings.SplitN(strings.TrimPrefix(key, filterPrefix), "].", 2)
			if len(parts) == 2 {
				fieldName := parts[1]
				// Check if the field name is one of the EqualizerFilter fields
				switch fieldName {
				case "type", "frequency", "q", "gain", "width", "passes":
					return true
				}
			}
		}
	}

	return false
}

// updateSliceFromForm updates a slice field from form values
func updateSliceFromForm(field reflect.Value, formValue []string) error {
	// Get the type of the slice elements
	sliceType := field.Type().Elem()
	// Create a new slice with initial capacity equal to the number of form values
	newSlice := reflect.MakeSlice(field.Type(), 0, len(formValue))

	// Iterate through each form value
	for _, val := range formValue {
		// Skip empty values
		if val == "" {
			continue
		}
		// Handle different types of slice elements
		switch sliceType.Kind() {
		case reflect.String:
			var urls []string
			// Try to unmarshal the value as a JSON array of strings
			err := json.Unmarshal([]byte(val), &urls)
			if err == nil {
				// If it's a valid JSON array, add each non-empty URL separately
				for _, url := range urls {
					if url != "" {
						newSlice = reflect.Append(newSlice, reflect.ValueOf(url))
					}
				}
			} else {
				// If it's not a JSON array, add it as a single string
				newSlice = reflect.Append(newSlice, reflect.ValueOf(val))
			}
		case reflect.Int, reflect.Int64:
			// Parse the value as an integer
			intVal, err := strconv.ParseInt(val, 10, 64)
			if err != nil {
				return fmt.Errorf("invalid integer value: %w", err)
			}
			// Add the parsed integer to the slice, converting to the correct type
			newSlice = reflect.Append(newSlice, reflect.ValueOf(intVal).Convert(sliceType))
		case reflect.Float32, reflect.Float64:
			// Parse the value as a float
			floatVal, err := strconv.ParseFloat(val, 64)
			if err != nil {
				return fmt.Errorf("invalid float value: %w", err)
			}
			// Add the parsed float to the slice, converting to the correct type
			newSlice = reflect.Append(newSlice, reflect.ValueOf(floatVal).Convert(sliceType))
		default:
			// Return an error for unsupported slice element types
			return fmt.Errorf("unsupported slice element type: %v", sliceType.Kind())
		}
	}

	// Set the updated slice back to the original field
	field.Set(newSlice)
	return nil
}

// updateEqualizerFromForm updates the equalizer settings from form values
func updateEqualizerFromForm(v reflect.Value, formValues map[string][]string, prefix string) error {
	// Check if the equalizer is enabled
	enabled, exists := formValues[prefix+".enabled"]
	if exists && len(enabled) > 0 {
		// Convert the "enabled" value to a boolean
		enabledValue := enabled[0] == "on" || enabled[0] == "true"
		// Set the "Enabled" field of the equalizer
		v.FieldByName("Enabled").SetBool(enabledValue)
		//log.Printf("Debug (updateEqualizerFromForm): Equalizer enabled: %v", enabledValue)
	}

	// Initialize a slice to store the equalizer filters
	var filters []conf.EqualizerFilter
	for i := 0; ; i++ {
		// Construct keys for each filter parameter
		typeKey := fmt.Sprintf("%s.filters[%d].type", prefix, i)
		frequencyKey := fmt.Sprintf("%s.filters[%d].frequency", prefix, i)
		qKey := fmt.Sprintf("%s.filters[%d].q", prefix, i)
		gainKey := fmt.Sprintf("%s.filters[%d].gain", prefix, i)
		passesKey := fmt.Sprintf("%s.filters[%d].passes", prefix, i)

		// Check if the current filter exists
		filterType, typeExists := formValues[typeKey]
		if !typeExists || len(filterType) == 0 {
			break // No more filters, exit the loop
		}

		// DEBUG: Log the processing of each filter
		//log.Printf("Debug (updateEqualizerFromForm): Processing filter %d", i)

		// Parse frequency value from form
		frequency, err := parseFloatFromForm(formValues, frequencyKey)
		if err != nil {
			return fmt.Errorf("invalid frequency value for filter %d: %w", i, err)
		}
		// Ensure frequency is within the valid range (0 to 20000)
		if frequency < 0 {
			frequency = 0
		} else if frequency > 20000 {
			frequency = 20000
		}

		// Parse the Q value from the form
		q, err := parseFloatFromForm(formValues, qKey)
		if err != nil {
			// If Q value is not found, set it to 0
			if err.Error() == fmt.Sprintf("value not found for key: %s", qKey) {
				q = 0
			} else {
				// If there's any other error, return it
				return fmt.Errorf("invalid Q value for filter %d: %w", i, err)
			}
		}
		// Ensure Q is within the valid range (0.0 to 1.0)
		if q < 0.0 {
			q = 0.0
		} else if q > 1.0 {
			q = 1.0
		}

		// Parse the gain value from the form
		gain, err := parseFloatFromForm(formValues, gainKey)
		if err != nil {
			// If gain is not provided, ignore the error and set gain to 0
			if err.Error() == fmt.Sprintf("value not found for key: %s", gainKey) {
				gain = 0
			} else {
				// If there's any other error, return it
				return fmt.Errorf("invalid gain value for filter %d: %w", i, err)
			}
		}

		// Parse the passes (Attenuation for low-pass and high-pass filters) value from the form
		passes, err := parseIntFromForm(formValues, passesKey)
		if err != nil {
			// If passes value is not found, set it to 0
			if err.Error() == fmt.Sprintf("value not found for key: %s", passesKey) {
				passes = 0
			} else {
				// If there's any other error, return it
				return fmt.Errorf("invalid passes value for filter %d: %w", i, err)
			}
		}
		// Ensure passes is within the valid range (0 to 4)
		if passes < 0 {
			passes = 0
		} else if passes > 4 {
			passes = 4
		}

		// Create a new filter with the parsed values
		filter := conf.EqualizerFilter{
			Type:      filterType[0],
			Frequency: frequency,
			Q:         q,
			Gain:      gain,
			Passes:    passes,
		}

		// Append the new filter to the filters slice
		filters = append(filters, filter)
		//log.Printf("Debug (updateEqualizerFromForm): Added filter: %+v", filter)
	}

	// Log the parsed filters for debugging
	//log.Printf("Debug (updateEqualizerFromForm): Total filters parsed: %d", len(filters))

	// Set the "Filters" field of the equalizer with the new filters
	v.FieldByName("Filters").Set(reflect.ValueOf(filters))

	return nil
}

// Helper function to parse float values from form
func parseFloatFromForm(formValues map[string][]string, key string) (float64, error) {
	values, exists := formValues[key]
	if !exists || len(values) == 0 {
		return 0, fmt.Errorf("value not found for key: %s", key)
	}
	return strconv.ParseFloat(values[0], 64)
}

// Helper function to parse integer values from form
func parseIntFromForm(formValues map[string][]string, key string) (int, error) {
	values, exists := formValues[key]
	if !exists || len(values) == 0 {
		return 0, fmt.Errorf("value not found for key: %s", key)
	}
	return strconv.Atoi(values[0])
}

// audioSettingsChanged checks if the audio settings have been modified
func equalizerSettingsChanged(oldSettings, newSettings conf.EqualizerSettings) bool {
	return !reflect.DeepEqual(oldSettings, newSettings)
}

// rangeFilterSettingsChanged checks if any settings that require a range filter reload have changed
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

// audioDeviceSettingChanged checks if audio device settings have been modified
func audioDeviceSettingChanged(oldSettings, currentSettings *conf.Settings) bool {
	return oldSettings.Realtime.Audio.Source != currentSettings.Realtime.Audio.Source
}

// rtspSettingsChanged checks if RTSP settings have been modified
func rtspSettingsChanged(oldSettings, currentSettings *conf.Settings) bool {
	// Check for changes in RTSP transport protocol
	if oldSettings.Realtime.RTSP.Transport != currentSettings.Realtime.RTSP.Transport {
		return true
	}

	// Check for changes in RTSP URLs
	if !reflect.DeepEqual(oldSettings.Realtime.RTSP.URLs, currentSettings.Realtime.RTSP.URLs) {
		return true
	}

	return false
}

// hasRTSPSettings checks if any RTSP-related settings were included in the form data
func hasRTSPSettings(formParams map[string][]string) bool {
	rtspPrefixes := []string{
		"realtime.rtsp.urls",
		"realtime.rtsp.transport",
	}

	for key := range formParams {
		for _, prefix := range rtspPrefixes {
			if strings.HasPrefix(strings.ToLower(key), prefix) {
				return true
			}
		}
	}
	return false
}

// Check if MQTT settings have changed
func mqttSettingsChanged(oldSettings, currentSettings *conf.Settings) bool {
	return oldSettings.Realtime.MQTT.Enabled != currentSettings.Realtime.MQTT.Enabled ||
		oldSettings.Realtime.MQTT.Broker != currentSettings.Realtime.MQTT.Broker ||
		oldSettings.Realtime.MQTT.Topic != currentSettings.Realtime.MQTT.Topic ||
		oldSettings.Realtime.MQTT.Username != currentSettings.Realtime.MQTT.Username ||
		oldSettings.Realtime.MQTT.Password != currentSettings.Realtime.MQTT.Password ||
		oldSettings.Realtime.MQTT.Retain != currentSettings.Realtime.MQTT.Retain
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
