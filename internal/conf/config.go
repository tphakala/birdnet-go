// config.go: This file contains the configuration for the BirdNET-Go application. It defines the settings struct and functions to load and save the settings.
package conf

import (
	"crypto/rand"
	"embed"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/spf13/viper"
	"gopkg.in/yaml.v3"
)

//go:embed config.yaml
var configFiles embed.FS

// EqualizerFilter is a struct for equalizer filter settings
type EqualizerFilter struct {
	Type      string // e.g., "LowPass", "HighPass", "BandPass", etc.
	Frequency float64
	Q         float64
	Gain      float64 // Only used for certain filter types like Peaking
	Width     float64 // Only used for certain filter types like BandPass and BandReject
	Passes    int     // Filter passes for added attenuation or gain
}

// EqualizerSettings is a struct for audio EQ settings
type EqualizerSettings struct {
	Enabled bool              // global flag to enable/disable equalizer filters
	Filters []EqualizerFilter // equalizer filter configuration
}

// AudioSettings contains settings for audio processing and export.
type AudioSettings struct {
	Source        string   // audio source to use for analysis
	FfmpegPath    string   // path to ffmpeg, runtime value
	SoxPath       string   // path to sox, runtime value
	SoxAudioTypes []string `yaml:"-"` // supported audio types of sox, runtime value
	Export        struct {
		Debug     bool   // true to enable audio export debug
		Enabled   bool   // export audio clips containing indentified bird calls
		Path      string // path to audio clip export directory
		Type      string // audio file type, wav, mp3 or flac
		Bitrate   string // bitrate for audio export
		Retention struct {
			Debug    bool   // true to enable retention debug
			Policy   string // retention policy, "none", "age" or "usage"
			MaxAge   string // maximum age of audio clips to keep
			MaxUsage string // maximum disk usage percentage before cleanup
			MinClips int    // minimum number of clips per species to keep
		}
	}
	Equalizer EqualizerSettings // equalizer settings
}
type Thumbnails struct {
	Debug   bool // true to enable debug mode
	Summary bool // show thumbnails on summary table
	Recent  bool // show thumbnails on recent table
}

// Dashboard contains settings for the web dashboard.
type Dashboard struct {
	Thumbnails   Thumbnails // thumbnails settings
	SummaryLimit int        // limit for the number of species shown in the summary table
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

// WeatherSettings contains all weather-related settings
type WeatherSettings struct {
	Provider     string              // "none", "yrno" or "openweather"
	PollInterval int                 // weather data polling interval in minutes
	Debug        bool                // true to enable debug mode
	OpenWeather  OpenWeatherSettings // OpenWeather integration settings
}

// OpenWeatherSettings contains settings for OpenWeather integration.
type OpenWeatherSettings struct {
	Enabled  bool   // true to enable OpenWeather integration, for legacy support
	APIKey   string // OpenWeather API key
	Endpoint string // OpenWeather API endpoint
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
	OpenWeather   OpenWeatherSettings   `yaml:"-"` // OpenWeather integration settings
	PrivacyFilter PrivacyFilterSettings // Privacy filter settings
	DogBarkFilter DogBarkFilterSettings // Dog bark filter settings
	RTSP          RTSPSettings          // RTSP settings
	MQTT          MQTTSettings          // MQTT settings
	Telemetry     TelemetrySettings     // Telemetry settings
	Species       SpeciesSettings       // Custom thresholds and actions for species
	Weather       WeatherSettings       // Weather provider related settings
}

// SpeciesAction represents a single action configuration
type SpeciesAction struct {
	Type       string   `yaml:"type"`       // Type of action (ExecuteCommand, etc)
	Command    string   `yaml:"command"`    // Path to the command to execute
	Parameters []string `yaml:"parameters"` // Action parameters
}

// SpeciesConfig represents configuration for a specific species
type SpeciesConfig struct {
	Threshold float64         `yaml:"threshold"` // Confidence threshold
	Actions   []SpeciesAction `yaml:"actions"`   // List of actions to execute
}

// RealtimeSpeciesSettings contains all species-specific settings
type SpeciesSettings struct {
	Include []string                 `yaml:"include"` // Always include these species
	Exclude []string                 `yaml:"exclude"` // Always exclude these species
	Config  map[string]SpeciesConfig `yaml:"config"`  // Per-species configuration
}

// ActionConfig holds configuration details for a specific action.
type ActionConfig struct {
	Type       string   // Type of the action (e.g. ExecuteScript which is only type for now)
	Parameters []string // List of parameters for the action
}

// InputConfig holds settings for file or directory analysis
type InputConfig struct {
	Path      string `yaml:"-"` // path to input file or directory
	Recursive bool   `yaml:"-"` // true for recursive directory analysis
	Watch     bool   `yaml:"-"` // true to watch directory for new files
}

type BirdNETConfig struct {
	Debug       bool                // true to enable debug mode
	Sensitivity float64             // birdnet analysis sigmoid sensitivity
	Threshold   float64             // threshold for prediction confidence to report
	Overlap     float64             // birdnet analysis overlap between chunks
	Longitude   float64             // longitude of recording location for prediction filtering
	Latitude    float64             // latitude of recording location for prediction filtering
	Threads     int                 // number of CPU threads to use for analysis
	Locale      string              // language to use for labels
	RangeFilter RangeFilterSettings // range filter settings
	ModelPath   string              // path to external model file (empty for embedded)
	LabelPath   string              // path to external label file (empty for embedded)
	Labels      []string            `yaml:"-"` // list of available species labels, runtime value
	UseXNNPACK  bool                // true to use XNNPACK delegate for inference acceleration
}

// RangeFilterSettings contains settings for the range filter
type RangeFilterSettings struct {
	Debug       bool      // true to enable debug mode
	Model       string    // range filter model model
	Threshold   float32   // rangefilter species occurrence threshold
	Species     []string  `yaml:"-"` // list of included species, runtime value
	LastUpdated time.Time `yaml:"-"` // last time the species list was updated, runtime value
}

// BasicAuth holds settings for the password authentication
type BasicAuth struct {
	Enabled        bool          // true to enable password authentication
	Password       string        // password for admin interface
	ClientID       string        // client id for OAuth2
	ClientSecret   string        // client secret for OAuth2
	RedirectURI    string        // redirect uri for OAuth2
	AuthCodeExp    time.Duration // duration for authorization code
	AccessTokenExp time.Duration // duration for access token
}

// SocialProvider holds settings for an OAuth2 identity provider
type SocialProvider struct {
	Enabled      bool   // true to enable social provider
	ClientID     string // client id for OAuth2
	ClientSecret string // client secret for OAuth2
	RedirectURI  string // redirect uri for OAuth2
	UserId       string // valid user id for OAuth2
}

type AllowSubnetBypass struct {
	Enabled bool   // true to enable subnet bypass
	Subnet  string // disable OAuth2 in subnet
}

type AllowCloudflareBypass struct {
	Enabled    bool   // true to enable CF Access
	TeamDomain string // Cloudflare team domain
	Audience   string // Cloudflare policy audience
}

// SecurityConfig handles all security-related settings and validations
// for the application, including authentication, TLS, and access control.
type Security struct {
	Debug bool // true to enable debug mode

	// Host is the primary hostname used for TLS certificates
	// and OAuth redirect URLs. Required when using AutoTLS or
	// authentication providers. Used to form the redirect URIs.
	Host string

	// AutoTLS enables automatic TLS certificate management using
	// Let's Encrypt. Requires Host to be set and port 80/443 access.
	AutoTLS bool

	RedirectToHTTPS       bool                  // true to redirect to HTTPS
	AllowSubnetBypass     AllowSubnetBypass     // subnet bypass configuration
	AllowCloudflareBypass AllowCloudflareBypass // Cloudflare Access configuration
	BasicAuth             BasicAuth             // password authentication configuration
	GoogleAuth            SocialProvider        // Google OAuth2 configuration
	GithubAuth            SocialProvider        // Github OAuth2 configuration
	SessionSecret         string                // secret for session cookie
}

// Settings contains all configuration options for the BirdNET-Go application.
type Settings struct {
	Debug bool // true to enable debug mode

	// Runtime values, not stored in config file
	Version   string `yaml:"-"` // Version from build
	BuildDate string `yaml:"-"` // Build date from build

	Main struct {
		Name      string    // name of BirdNET-Go node, can be used to identify source of notes
		TimeAs24h bool      // true 24-hour time format, false 12-hour time format
		Log       LogConfig // logging configuration
	}

	BirdNET BirdNETConfig // BirdNET configuration

	Input InputConfig `yaml:"-"` // Input configuration for file and directory analysis

	Realtime RealtimeSettings // Realtime processing settings

	WebServer struct {
		Debug   bool      // true to enable debug mode
		Enabled bool      // true to enable web server
		Port    string    // port for web server
		Log     LogConfig // logging configuration for web server
	}

	Security Security // security configuration

	Output struct {
		File struct {
			Enabled bool   `yaml:"-"` // true to enable file output
			Path    string `yaml:"-"` // directory to output results
			Type    string `yaml:"-"` // table, csv
		}

		SQLite struct {
			Enabled bool   // true to enable sqlite output
			Path    string // path to sqlite database
			TempDir string // path to temporary directory for backups
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

	Backup BackupConfig // Backup configuration
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

// BackupProvider defines settings for a specific backup provider
type BackupProvider struct {
	Type     string                 `yaml:"type"`     // "local", "cifs", "nfs", "ftp", "onedrive", etc.
	Enabled  bool                   `yaml:"enabled"`  // true to enable this provider
	Settings map[string]interface{} `yaml:"settings"` // Provider-specific settings
}

// BackupRetention defines backup retention policy
type BackupRetention struct {
	MaxAge     string `yaml:"maxage"`     // Duration string like "30d", "6m", "1y"
	MaxBackups int    `yaml:"maxbackups"` // Maximum number of backups to keep
	MinBackups int    `yaml:"minbackups"` // Minimum number of backups to keep regardless of age
}

// BackupTarget defines settings for a backup target
type BackupTarget struct {
	Type     string                 `yaml:"type"`     // "local", "ftp", "sftp", "rsync", "gdrive"
	Enabled  bool                   `yaml:"enabled"`  // true to enable this target
	Settings map[string]interface{} `yaml:"settings"` // Target-specific settings
}

// BackupConfig defines the configuration for backups
type BackupConfig struct {
	Enabled       bool            `yaml:"enabled"`        // true to enable backup functionality
	Debug         bool            `yaml:"debug"`          // true to enable debug logging
	Schedule      string          `yaml:"schedule"`       // Cron expression for backup schedule
	Encryption    bool            `yaml:"encryption"`     // true to enable backup encryption
	EncryptionKey string          `yaml:"encryption_key"` // AES-256 key for encrypting backups (hex-encoded)
	Retention     BackupRetention `yaml:"retention"`      // Backup retention settings
	Targets       []BackupTarget  `yaml:"targets"`        // List of backup targets
}

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

	// Log the loaded species settings for debugging
	/*
		log.Printf("Loaded Species Settings: Include: %v, Exclude: %v, Threshold: %v",
			settings.Realtime.Species.Include,
			settings.Realtime.Species.Exclude,
			settings.Realtime.Species.Threshold)
	*/

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

	// If the basicauth secret is not set, generate a random one
	if viper.GetString("security.basicauth.clientsecret") == "" {
		viper.Set("security.basicauth.clientsecret", GenerateRandomSecret())
	}

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
	settingsMutex.RLock()
	defer settingsMutex.RUnlock()

	// Create a deep copy of the settings
	settingsCopy := *settingsInstance

	// Create a separate copy of the species list
	speciesListMutex.RLock()
	settingsCopy.BirdNET.RangeFilter.Species = make([]string, len(settingsInstance.BirdNET.RangeFilter.Species))
	copy(settingsCopy.BirdNET.RangeFilter.Species, settingsInstance.BirdNET.RangeFilter.Species)
	speciesListMutex.RUnlock()

	// Find the path of the current config file
	configPath, err := FindConfigFile()
	if err != nil {
		return fmt.Errorf("error finding config file: %w", err)
	}

	// Save the settings to the config file
	if err := SaveYAMLConfig(configPath, &settingsCopy); err != nil {
		return fmt.Errorf("error saving config: %w", err)
	}

	log.Printf("Settings saved successfully to %s", configPath)
	return nil
}

// Setting returns the current settings instance, initializing it if necessary
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

// SaveYAMLConfig updates the YAML configuration file with new settings.
// It overwrites the existing file, not preserving comments or structure.
func SaveYAMLConfig(configPath string, settings *Settings) error {
	// Marshal the settings struct to YAML
	yamlData, err := yaml.Marshal(settings)
	if err != nil {
		return fmt.Errorf("error marshaling settings to YAML: %w", err)
	}

	// Write the YAML data to a temporary file
	// This is done to ensure atomic write operation
	tempFile, err := os.CreateTemp(filepath.Dir(configPath), "config-*.yaml")
	if err != nil {
		return fmt.Errorf("error creating temporary file: %w", err)
	}
	tempFileName := tempFile.Name()
	// Ensure the temporary file is removed in case of any failure
	defer os.Remove(tempFileName)

	// Write the YAML data to the temporary file
	if _, err := tempFile.Write(yamlData); err != nil {
		tempFile.Close()
		return fmt.Errorf("error writing to temporary file: %w", err)
	}
	// Close the temporary file after writing
	if err := tempFile.Close(); err != nil {
		return fmt.Errorf("error closing temporary file: %w", err)
	}

	// Try to rename the temporary file to replace the original config file
	// This is typically an atomic operation on most filesystems
	if err := os.Rename(tempFileName, configPath); err != nil {
		// If rename fails (e.g., cross-device link), fall back to copy & delete
		// This might happen when the temp directory is on a different filesystem
		if err := moveFile(tempFileName, configPath); err != nil {
			return fmt.Errorf("error copying config file: %w", err)
		}
	}

	// If we've reached this point, the operation was successful
	return nil
}

// GenerateRandomSecret generates a URL-safe base64 encoded random string
// suitable for use as a client secret. The output is 43 characters long,
// providing 256 bits of entropy.
func GenerateRandomSecret() string {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		// Log the error and return a safe fallback or empty string
		log.Printf("Failed to generate random secret: %v", err)
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(bytes)
}

// GetWeatherSettings returns the appropriate weather settings based on the configuration
func (s *Settings) GetWeatherSettings() (provider string, openweather OpenWeatherSettings) {
	// First check new format
	if s.Realtime.Weather.Provider != "" {
		return s.Realtime.Weather.Provider, s.Realtime.Weather.OpenWeather
	}

	if s.Realtime.OpenWeather.Enabled {
		return "openweather", s.Realtime.OpenWeather
	}

	// Default to YrNo if nothing is configured
	return "yrno", OpenWeatherSettings{}
}
