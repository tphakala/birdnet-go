// updateconfig.go
package httpcontroller

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/conf"
	"gopkg.in/yaml.v3"
)

// updateSettingsHandler handles the settings update request.
func (s *Server) updateSettingsHandler(c echo.Context) error {
	// Extract form values from the request context.
	formValues := extractFormValues(c)

	// Attempt to locate the configuration file.
	configFilePath, err := findConfigFile()
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}

	// Read the YAML configuration file.
	data, err := os.ReadFile(configFilePath)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to read config file: "+err.Error())
	}

	// Parse the YAML data into a node structure.
	var config yaml.Node
	if err := yaml.Unmarshal(data, &config); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to unmarshal config file: "+err.Error())
	}

	// Update YAML structure based on form values.
	for key, value := range formValues {
		if node := findChildNodeByKey(key, config.Content[0]); node != nil {
			node.Value = fmt.Sprintf("%v", value)
		}
	}

	// Write the updated YAML configuration back to the file.
	if err := writeYAMLToFile(configFilePath, &config); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}

	// Update the server's settings struct with new values from the form.
	updateServerSettings(s, formValues)

	// Redirect to the settings page, indicating success.
	return c.Redirect(http.StatusFound, "/settings?update=success")
}

// writeYAMLToFile writes the modified YAML structure back to the file.
func writeYAMLToFile(filePath string, node *yaml.Node) error {
	modifiedData, err := yaml.Marshal(node)
	if err != nil {
		return fmt.Errorf("failed to marshal updated config: %w", err)
	}

	if err := os.WriteFile(filePath, modifiedData, 0644); err != nil {
		return fmt.Errorf("failed to write updated config: %w", err)
	}

	return nil
}

// findChildNodeByKey finds a node in the YAML structure based on the given key.
func findChildNodeByKey(key string, node *yaml.Node) *yaml.Node {
	components := strings.Split(key, ".")

	var find func(int, *yaml.Node) *yaml.Node
	find = func(index int, n *yaml.Node) *yaml.Node {
		if n.Kind == yaml.MappingNode {
			for i := 0; i < len(n.Content); i += 2 {
				if n.Content[i].Value == components[index] {
					if index == len(components)-1 {
						return n.Content[i+1]
					}
					return find(index+1, n.Content[i+1])
				}
			}
		}
		return nil
	}

	return find(0, node)
}

// extractFormValues extracts values from the form and converts them to appropriate types.
func extractFormValues(c echo.Context) map[string]interface{} {
	// Check if the checkbox is checked by looking for its presence in the form data.
	realtimeLogEnabled := c.FormValue("Realtime.Log.Enabled") != ""
	realtimeAudioExportEnabled := c.FormValue("Realtime.AudioExport.Enabled") != ""
	realtimeBirdweatherEnabled := c.FormValue("Realtime.Birdweather.Enabled") != ""

	values := map[string]interface{}{
		"main.name":                    c.FormValue("Main.Name"),
		"birdnet.locale":               c.FormValue("BirdNET.Locale"),
		"birdnet.sensitivity":          parseFloat(c.FormValue("BirdNET.Sensitivity")),
		"birdnet.threshold":            parseFloat(c.FormValue("BirdNET.Threshold")),
		"birdnet.latitude":             parseFloat(c.FormValue("BirdNET.Latitude")),
		"birdnet.longitude":            parseFloat(c.FormValue("BirdNET.Longitude")),
		"birdnet.threads":              parseInt(c.FormValue("BirdNET.Threads")),
		"realtime.audioexport.enabled": realtimeAudioExportEnabled,
		"realtime.audioexport.path":    c.FormValue("Realtime.AudioExport.Path"),
		"realtime.log.enabled":         realtimeLogEnabled,
		"realtime.log.path":            c.FormValue("Realtime.Log.Path"),
		"realtime.interval":            parseInt(c.FormValue("Realtime.Interval")),
		"realtime.birdweather.enabled": realtimeBirdweatherEnabled,
		"realtime.birdweather.id":      c.FormValue("Realtime.Birdweather.ID"),
		"output.sqlite.path":           c.FormValue("Settings.Output.SQLite.Path"),
	}

	return values
}

// updateServerSettings updates the server's Settings struct with values from the form.
func updateServerSettings(s *Server, formValues map[string]interface{}) {
	// Main settings
	if name, ok := formValues["main.name"].(string); ok {
		s.Settings.Main.Name = name
	}

	// BirdNET settings
	if locale, ok := formValues["birdnet.locale"].(string); ok {
		s.Settings.BirdNET.Locale = locale
	}
	s.Settings.BirdNET.Sensitivity = formValues["birdnet.sensitivity"].(float64)
	s.Settings.BirdNET.Threshold = formValues["birdnet.threshold"].(float64)
	s.Settings.BirdNET.Latitude = formValues["birdnet.latitude"].(float64)
	s.Settings.BirdNET.Longitude = formValues["birdnet.longitude"].(float64)
	s.Settings.BirdNET.Threads = formValues["birdnet.threads"].(int)

	// Realtime settings - Audio Export
	if val, ok := formValues["realtime.audioexport.enabled"].(bool); ok {
		s.Settings.Realtime.Audio.Export.Enabled = val
	}
	if audioExportPath, ok := formValues["realtime.audioexport.path"].(string); ok {
		s.Settings.Realtime.Audio.Export.Path = audioExportPath
	}

	// Realtime settings - Log
	if val, ok := formValues["realtime.log.enabled"].(bool); ok {
		s.Settings.Realtime.Log.Enabled = val
	}
	if logPath, ok := formValues["realtime.log.path"].(string); ok {
		s.Settings.Realtime.Log.Path = logPath
	}

	// Realtime settings - General and Birdweather
	s.Settings.Realtime.Interval = formValues["realtime.interval"].(int)

	if val, ok := formValues["realtime.birdweather.enabled"].(bool); ok {
		s.Settings.Realtime.Birdweather.Enabled = val
	}
	if birdweatherID, ok := formValues["realtime.birdweather.id"].(string); ok {
		s.Settings.Realtime.Birdweather.ID = birdweatherID
	}

	// SQLite settings
	if sqlitePath, ok := formValues["output.sqlite.path"].(string); ok {
		s.Settings.Output.SQLite.Path = sqlitePath
	}
}

// findConfigFile locates the configuration file.
func findConfigFile() (string, error) {
	configPaths, err := conf.GetDefaultConfigPaths()
	if err != nil {
		return "", fmt.Errorf("error getting default config paths: %w", err)
	}

	for _, path := range configPaths {
		configFilePath := filepath.Join(path, "config.yaml")
		if _, err := os.Stat(configFilePath); err == nil {
			return configFilePath, nil
		}
	}

	return "", fmt.Errorf("config file not found")
}

// parseFloat parses a string into a float64.
func parseFloat(str string) float64 {
	val, err := strconv.ParseFloat(str, 64)
	if err != nil {
		return 0
	}
	return val
}

// parseInt parses a string into an int.
func parseInt(str string) int {
	val, err := strconv.Atoi(str)
	if err != nil {
		return 0
	}
	return val
}
