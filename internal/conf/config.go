// config.go: This file contains the configuration for the BirdNET-Go application. It defines the settings struct and functions to load and save the settings.
package conf

import (
	"embed"
	"encoding/csv"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/spf13/viper"
)

//go:embed config.yaml
var configFiles embed.FS

// Dashboard contains settings for the web dashboard.
type Dashboard struct {
	Thumbnails struct {
		Summary bool // show thumbnails on summary table
		Recent  bool // show thumbnails on recent table
	}
}

// AudioSettings contains settings for audio processing and export.
type AudioSettings struct {
	Source string // audio source to use for analysis
	Export struct {
		Debug     bool   // true to enable audio export debug
		Enabled   bool   // export audio clips containing indentified bird calls
		Path      string // path to audio clip export directory
		Type      string // audio file type, wav, mp3 or flac
		Retention struct {
			Debug    bool   // true to enable retention debug
			Policy   string // retention policy, "none", "age" or "usage"
			MaxAge   string // maximum age of audio clips to keep
			MaxUsage string // maximum disk usage percentage before cleanup
			MinClips int    // minimum number of clips per species to keep
		}
	}
}

// DynamicThresholdSettings contains settings for dynamic threshold adjustment.
type DynamicThresholdSettings struct {
	Enabled    bool    // true to enable dynamic threshold
	Debug      bool    // true to enable debug mode
	Trigger    float64 // trigger threshold for dynamic threshold
	Min        float64 // minimum threshold for dynamic threshold
	ValidHours int     // number of hours to consider for dynamic threshold
}

// BirdweatherSettings contains settings for Birdweather integration.
type BirdweatherSettings struct {
	Enabled          bool    // true to enable birdweather uploads
	Debug            bool    // true to enable debug mode
	ID               string  // birdweather ID
	Threshold        float64 // threshold for prediction confidence for uploads
	LocationAccuracy float64 // accuracy of location in meters
}

// OpenWeatherSettings contains settings for OpenWeather integration.
type OpenWeatherSettings struct {
	Enabled  bool   // true to enable OpenWeather integration
	Debug    bool   // true to enable debug mode
	APIKey   string // OpenWeather API key
	Endpoint string // OpenWeather API endpoint
	Interval int    // interval for fetching weather data in minutes
	Units    string // units of measurement: standard, metric, or imperial
	Language string // language code for the response
}

// PrivacyFilterSettings contains settings for the privacy filter.
type PrivacyFilterSettings struct {
	Debug      bool    // true to enable debug mode
	Enabled    bool    // true to enable privacy filter
	Confidence float32 // confidence threshold for human detection
}

// DogBarkFilterSettings contains settings for the dog bark filter.
type DogBarkFilterSettings struct {
	Debug      bool     // true to enable debug mode
	Enabled    bool     // true to enable dog bark filter
	Confidence float32  // confidence threshold for dog bark detection
	Remember   int      // how long we should remember bark for filtering?
	Species    []string // species list for filtering
}

// RTSPSettings contains settings for RTSP streaming.
type RTSPSettings struct {
	Transport string   // RTSP Transport Protocol
	URLs      []string // RTSP stream URL
}

// MQTTSettings contains settings for MQTT integration.
type MQTTSettings struct {
	Enabled  bool   // true to enable MQTT
	Broker   string // MQTT (tcp://host:port)
	Topic    string // MQTT topic
	Username string // MQTT username
	Password string // MQTT password
}

// TelemetrySettings contains settings for telemetry.
type TelemetrySettings struct {
	Enabled bool   // true to enable Prometheus compatible telemetry endpoint
	Listen  string // IP address and port to listen on
}

// RealtimeSettings contains all settings related to realtime processing.
type RealtimeSettings struct {
	Interval         int                      // minimum interval between log messages in seconds
	ProcessingTime   bool                     // true to report processing time for each prediction
	Audio            AudioSettings            // Audio processing settings
	Dashboard        Dashboard                // Dashboard settings
	DynamicThreshold DynamicThresholdSettings // Dynamic threshold settings
	Log              struct {
		Enabled bool   // true to enable OBS chat log
		Path    string // path to OBS chat log
	}
	Birdweather   BirdweatherSettings   // Birdweather integration settings
	OpenWeather   OpenWeatherSettings   // OpenWeather integration settings
	PrivacyFilter PrivacyFilterSettings // Privacy filter settings
	DogBarkFilter DogBarkFilterSettings // Dog bark filter settings
	RTSP          RTSPSettings          // RTSP settings
	MQTT          MQTTSettings          // MQTT settings
	Telemetry     TelemetrySettings     // Telemetry settings
}

// Settings contains all configuration options for the BirdNET-Go application.
type Settings struct {
	Debug bool // true to enable debug mode

	Main struct {
		Name      string    // name of BirdNET-Go node, can be used to identify source of notes
		TimeAs24h bool      // true 24-hour time format, false 12-hour time format
		Log       LogConfig // logging configuration
	}

	BirdNET struct {
		Sensitivity float64 // birdnet analysis sigmoid sensitivity
		Threshold   float64 // threshold for prediction confidence to report
		Overlap     float64 // birdnet analysis overlap between chunks
		Longitude   float64 // longitude of recording location for prediction filtering
		Latitude    float64 // latitude of recording location for prediction filtering
		Threads     int     // number of CPU threads to use for analysis
		Locale      string  // language to use for labels
		RangeFilter struct {
			Model     string  // range filter model model
			Threshold float32 // rangefilter species occurrence threshold
		}
	}

	Input struct {
		Path      string // path to input file or directory
		Recursive bool   // true for recursive directory analysis
	}

	Realtime RealtimeSettings // Realtime processing settings

	WebServer struct {
		Enabled bool      // true to enable web server
		Port    string    // port for web server
		AutoTLS bool      // true to enable auto TLS
		Log     LogConfig // logging configuration for web server
	}

	Output struct {
		File struct {
			Enabled bool   // true to enable file output
			Path    string // directory to output results
			Type    string // table, csv
		}

		SQLite struct {
			Enabled bool   // true to enable sqlite output
			Path    string // path to sqlite database
		}

		MySQL struct {
			Enabled  bool   // true to enable mysql output
			Username string // username for mysql database
			Password string // password for mysql database
			Database string // database name for mysql database
			Host     string // host for mysql database
			Port     string // port for mysql database
		}
	}
}

// LogConfig defines the configuration for a log file
type LogConfig struct {
	Enabled     bool         // true to enable this log
	Path        string       // Path to the log file
	Rotation    RotationType // Type of log rotation
	MaxSize     int64        // Max size in bytes for RotationSize
	RotationDay string       // Day of the week for RotationWeekly (as a string: "Sunday", "Monday", etc.)
}

// RotationType defines different types of log rotations.
type RotationType string

const (
	RotationDaily  RotationType = "daily"
	RotationWeekly RotationType = "weekly"
	RotationSize   RotationType = "size"
)

// settingsInstance is the current settings instance
var (
	settingsInstance *Settings
	once             sync.Once
	settingsMutex    sync.RWMutex
)

// Load reads the configuration file and environment variables into GlobalConfig.
func Load() (*Settings, error) {
	settingsMutex.Lock()
	defer settingsMutex.Unlock()

	// Create a new settings struct
	settings := &Settings{}

	// Initialize viper and read config
	if err := initViper(); err != nil {
		return nil, fmt.Errorf("error initializing viper: %w", err)
	}

	// Unmarshal the config into settings
	if err := viper.Unmarshal(settings); err != nil {
		return nil, fmt.Errorf("error unmarshaling config into struct: %w", err)
	}

	// Validate settings
	if err := ValidateSettings(settings); err != nil {
		return nil, fmt.Errorf("error validating settings: %w", err)
	}

	// Save settings instance
	settingsInstance = settings
	return settingsInstance, nil
}

// initViper initializes viper with default values and reads the configuration file.
func initViper() error {
	viper.SetConfigName("config")
	viper.SetConfigType("yaml")

	// Get OS specific config paths
	configPaths, err := GetDefaultConfigPaths()
	if err != nil {
		return fmt.Errorf("error getting default config paths: %w", err)
	}

	// Assign config paths to Viper
	for _, path := range configPaths {
		viper.AddConfigPath(path)
	}

	// Set default values for each configuration parameter
	// function defined in defaults.go
	setDefaultConfig()

	// Read configuration file
	err = viper.ReadInConfig()
	if err != nil {
		var configFileNotFoundError viper.ConfigFileNotFoundError
		if errors.As(err, &configFileNotFoundError) {
			// Config file not found, create config with defaults
			return createDefaultConfig()
		}
		return fmt.Errorf("fatal error reading config file: %w", err)
	}

	return nil
}

// createDefaultConfig creates a default config file and writes it to the default config path
func createDefaultConfig() error {
	configPaths, err := GetDefaultConfigPaths() // Again, adjusted for error handling
	if err != nil {
		return fmt.Errorf("error getting default config paths: %w", err)
	}
	configPath := filepath.Join(configPaths[0], "config.yaml")
	defaultConfig := getDefaultConfig()

	// Create directories for config file
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		return fmt.Errorf("error creating directories for config file: %w", err)
	}

	// Write default config file
	if err := os.WriteFile(configPath, []byte(defaultConfig), 0o644); err != nil {
		return fmt.Errorf("error writing default config file: %w", err)
	}

	fmt.Println("Created default config file at:", configPath)
	return viper.ReadInConfig()
}

// getDefaultConfig reads the default configuration from the embedded config.yaml file.
func getDefaultConfig() string {
	data, err := fs.ReadFile(configFiles, "config.yaml")
	if err != nil {
		log.Fatalf("Error reading config file: %v", err)
	}
	return string(data)
}

// GetSettings returns the current settings instance
func GetSettings() *Settings {
	settingsMutex.RLock()
	defer settingsMutex.RUnlock()
	return settingsInstance
}

// SaveSettings saves the current settings to the configuration file.
// It uses UpdateYAMLConfig to handle the atomic write process.
func SaveSettings() error {
	// Create a copy of the current settings
	settingsMutex.RLock()
	settingsCopy := *settingsInstance
	settingsMutex.RUnlock()

	// Find the path of the current config file
	configPath, err := FindConfigFile()
	if err != nil {
		return fmt.Errorf("error finding config file: %w", err)
	}

	// Use UpdateYAMLConfig to write the new settings
	if err := UpdateYAMLConfig(configPath, &settingsCopy); err != nil {
		return fmt.Errorf("error updating config: %w", err)
	}

	if err := settingsCopy.SaveDogBarkFilter(); err != nil {
		return fmt.Errorf("error saving dog bark filter: %w", err)
	}

	log.Printf("Settings saved successfully to %s", configPath)
	return nil
}

// Settings returns the current settings instance, initializing it if necessary
func Setting() *Settings {
	once.Do(func() {
		if settingsInstance == nil {
			_, err := Load()
			if err != nil {
				log.Fatalf("Error loading settings: %v", err)
			}
		}
	})
	return GetSettings()
}

// SaveCSVList saves a list of strings to a CSV file
func SaveCSVList(fileName string, list []string) error {
	// Retrieve the default config paths
	configPaths, err := GetDefaultConfigPaths()
	if err != nil {
		return err
	}

	// Use the first config path to save the file
	fullPath := filepath.Join(configPaths[0], fileName)

	// Create the file
	file, err := os.Create(fullPath)
	if err != nil {
		return fmt.Errorf("error creating file: %w", err)
	}
	defer file.Close()

	// Create a CSV writer
	writer := csv.NewWriter(file)
	defer writer.Flush()

	// Write each item in the list as a separate row
	for _, item := range list {
		if err := writer.Write([]string{item}); err != nil {
			return fmt.Errorf("error writing to CSV: %w", err)
		}
	}

	return nil
}

// LoadDogBarkFilterConfig reads the dog bark filter configuration from a CSV file.
func (s *Settings) LoadDogBarkFilter(fileName string) error {
	var Species []string

	// Init the species list
	s.Realtime.DogBarkFilter.Species = []string{}

	// Retrieve the default config paths from your application settings.
	configPaths, err := GetDefaultConfigPaths()
	if err != nil {
		return err
	}

	var file *os.File
	// Attempt to open the file from one of the default config paths.
	for _, path := range configPaths {
		fullPath := filepath.Join(path, fileName)
		file, err = os.Open(fullPath)
		if err == nil {
			break
		}
	}

	if file == nil {
		// if file is not found just return empty config and error
		return fmt.Errorf("file '%s' not found in default config paths", fileName)
	}
	defer file.Close()

	reader := csv.NewReader(file)
	// Assuming no header and one species per line.
	records, err := reader.ReadAll()
	if err != nil {
		return err
	}

	// Iterate over the records and add them to the species list.
	for _, record := range records {
		if len(record) == 0 {
			continue // Skip empty lines
		}
		// Assuming the species name is the only entry in each record.
		species := strings.ToLower(strings.TrimSpace(record[0]))
		log.Printf("Adding species '%s' to dog bark filter", species)
		Species = append(Species, species)
	}
	log.Println("Dog bark filter config loaded")

	s.Realtime.DogBarkFilter.Species = Species

	return nil
}

// SaveDogBarkFilter saves the dog bark filter species list to a CSV file
func (s *Settings) SaveDogBarkFilter() error {
	return SaveCSVList(DogBarkFilterCSV, s.Realtime.DogBarkFilter.Species)
}
