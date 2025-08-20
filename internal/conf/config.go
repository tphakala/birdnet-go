// config.go: This file contains the configuration for the BirdNET-Go application. It defines the settings struct and functions to load and save the settings.
package conf

import (
	"crypto/rand"
	"embed"
	"encoding/base64"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/spf13/viper"
	"github.com/tphakala/birdnet-go/internal/errors"
	"gopkg.in/yaml.v3"
)

//go:embed config.yaml
var configFiles embed.FS

// EqualizerFilter is a struct for equalizer filter settings
type EqualizerFilter struct {
	Type      string  `json:"type"` // e.g., "LowPass", "HighPass", "BandPass", etc.
	Frequency float64 `json:"frequency"`
	Q         float64 `json:"q"`
	Gain      float64 `json:"gain"`   // Only used for certain filter types like Peaking
	Width     float64 `json:"width"`  // Only used for certain filter types like BandPass and BandReject
	Passes    int     `json:"passes"` // Filter passes for added attenuation or gain
}

// EqualizerSettings is a struct for audio EQ settings
type EqualizerSettings struct {
	Enabled bool              `json:"enabled"` // global flag to enable/disable equalizer filters
	Filters []EqualizerFilter `json:"filters"` // equalizer filter configuration
}

type ExportSettings struct {
	Debug     bool              `json:"debug"`     // true to enable audio export debug
	Enabled   bool              `json:"enabled"`   // export audio clips containing indentified bird calls
	Path      string            `json:"path"`      // path to audio clip export directory
	Type      string            `json:"type"`      // audio file type, wav, mp3 or flac
	Bitrate   string            `json:"bitrate"`   // bitrate for audio export
	Retention RetentionSettings `json:"retention"` // retention settings
}

type RetentionSettings struct {
	Debug            bool   `json:"debug"`            // true to enable retention debug
	Policy           string `json:"policy"`           // retention policy, "none", "age" or "usage"
	MaxAge           string `json:"maxAge"`           // maximum age of audio clips to keep
	MaxUsage         string `json:"maxUsage"`         // maximum disk usage percentage before cleanup
	MinClips         int    `json:"minClips"`         // minimum number of clips per species to keep
	KeepSpectrograms bool   `json:"keepSpectrograms"` // true to keep spectrograms
}

// AudioSettings contains settings for audio processing and export.
// SoundLevelSettings contains settings for sound level monitoring
type SoundLevelSettings struct {
	Enabled              bool `yaml:"enabled" mapstructure:"enabled" json:"enabled"`                                            // true to enable sound level monitoring
	Interval             int  `yaml:"interval" mapstructure:"interval" json:"interval"`                                         // measurement interval in seconds (default: 10)
	Debug                bool `yaml:"debug" mapstructure:"debug" json:"debug"`                                                  // true to enable debug logging for sound level monitoring
	DebugRealtimeLogging bool `yaml:"debug_realtime_logging" mapstructure:"debug_realtime_logging" json:"debugRealtimeLogging"` // true to log debug messages for every realtime update, false to log only at configured interval
}

type AudioSettings struct {
	Source          string             `yaml:"source" mapstructure:"source" json:"source"`                   // audio source to use for analysis
	FfmpegPath      string             `yaml:"ffmpegpath" mapstructure:"ffmpegpath" json:"ffmpegPath"`       // path to ffmpeg, runtime value
	SoxPath         string             `yaml:"soxpath" mapstructure:"soxpath" json:"soxPath"`                // path to sox, runtime value
	SoxAudioTypes   []string           `yaml:"-" json:"-"`                                                   // supported audio types of sox, runtime value
	StreamTransport string             `json:"streamTransport"`                                              // preferred transport for audio streaming: "auto", "sse", or "ws"
	Export          ExportSettings     `json:"export"`                                                       // export settings
	SoundLevel      SoundLevelSettings `json:"soundLevel"`                                                   // sound level monitoring settings
	UseAudioCore    bool               `yaml:"useaudiocore" mapstructure:"useaudiocore" json:"useAudioCore"` // true to use new audiocore package instead of myaudio

	Equalizer EqualizerSettings `json:"equalizer"` // equalizer settings
}
type Thumbnails struct {
	Debug          bool   `json:"debug"`          // true to enable debug mode
	Summary        bool   `json:"summary"`        // show thumbnails on summary table
	Recent         bool   `json:"recent"`         // show thumbnails on recent table
	ImageProvider  string `json:"imageProvider"`  // preferred image provider: "auto", "wikimedia", "avicommons"
	FallbackPolicy string `json:"fallbackPolicy"` // fallback policy: "none", "all" - try all available providers if preferred fails
}

// Dashboard contains settings for the web dashboard.
type Dashboard struct {
	Thumbnails   Thumbnails `json:"thumbnails"`       // thumbnails settings
	SummaryLimit int        `json:"summaryLimit"`     // limit for the number of species shown in the summary table
	Locale       string     `json:"locale,omitempty"` // UI locale setting
	NewUI        bool       `json:"newUI"`            // Enable redirect from old HTMX UI to new Svelte UI
}

// DynamicThresholdSettings contains settings for dynamic threshold adjustment.
type DynamicThresholdSettings struct {
	Enabled    bool    `json:"enabled"`    // true to enable dynamic threshold
	Debug      bool    `json:"debug"`      // true to enable debug mode
	Trigger    float64 `json:"trigger"`    // trigger threshold for dynamic threshold
	Min        float64 `json:"min"`        // minimum threshold for dynamic threshold
	ValidHours int     `json:"validHours"` // number of hours to consider for dynamic threshold
}

// RetrySettings contains common settings for retry mechanisms
type RetrySettings struct {
	Enabled           bool    `json:"enabled"`           // true to enable retry mechanism
	MaxRetries        int     `json:"maxRetries"`        // maximum number of retry attempts
	InitialDelay      int     `json:"initialDelay"`      // initial delay before first retry in seconds
	MaxDelay          int     `json:"maxDelay"`          // maximum delay between retries in seconds
	BackoffMultiplier float64 `json:"backoffMultiplier"` // multiplier for exponential backoff
}

// BirdweatherSettings contains settings for BirdWeather API integration.
type BirdweatherSettings struct {
	Enabled          bool          `json:"enabled"`          // true to enable birdweather uploads
	Debug            bool          `json:"debug"`            // true to enable debug mode
	ID               string        `json:"id"`               // birdweather ID
	Threshold        float64       `json:"threshold"`        // threshold for prediction confidence for uploads
	LocationAccuracy float64       `json:"locationAccuracy"` // accuracy of location in meters
	RetrySettings    RetrySettings `json:"retrySettings"`    // settings for retry mechanism
}

// EBirdSettings contains settings for eBird API integration.
type EBirdSettings struct {
	Enabled  bool   `json:"enabled"`  // true to enable eBird integration
	APIKey   string `json:"apiKey"`   // eBird API key
	CacheTTL int    `json:"cacheTTL"` // cache time-to-live in hours (default: 24)
	Locale   string `json:"locale"`   // locale for eBird data (e.g., "en", "es")
}

// WeatherSettings contains all weather-related settings
type WeatherSettings struct {
	Provider     string               `json:"provider"`     // "none", "yrno", "openweather", or "wunderground"
	PollInterval int                  `json:"pollInterval"` // weather data polling interval in minutes
	Debug        bool                 `json:"debug"`        // true to enable debug mode
	OpenWeather  OpenWeatherSettings  `json:"openWeather"`  // OpenWeather integration settings
	Wunderground WundergroundSettings `json:"wunderground"` // WeatherUnderground integration settings
}

// WundergroundSettings contains settings for WeatherUnderground integration.
type WundergroundSettings struct {
	APIKey    string `json:"apiKey"`    // WeatherUnderground API key
	StationID string `json:"stationId"` // WeatherUnderground station ID
	Endpoint  string `json:"endpoint"`  // WeatherUnderground API endpoint
	Units     string `json:"units"`     // units of measurement: "e" (imperial), "m" (metric), "h" (UK hybrid)
}

// OpenWeatherSettings contains settings for OpenWeather integration.
type OpenWeatherSettings struct {
	Enabled  bool   `json:"enabled"`  // true to enable OpenWeather integration, for legacy support
	APIKey   string `json:"apiKey"`   // OpenWeather API key
	Endpoint string `json:"endpoint"` // OpenWeather API endpoint
	Units    string `json:"units"`    // units of measurement: standard, metric, or imperial
	Language string `json:"language"` // language code for the response
}

// PrivacyFilterSettings contains settings for the privacy filter.
type PrivacyFilterSettings struct {
	Debug      bool    `json:"debug"`      // true to enable debug mode
	Enabled    bool    `json:"enabled"`    // true to enable privacy filter
	Confidence float32 `json:"confidence"` // confidence threshold for human detection
}

// DogBarkFilterSettings contains settings for the dog bark filter.
type DogBarkFilterSettings struct {
	Debug      bool     `json:"debug"`      // true to enable debug mode
	Enabled    bool     `json:"enabled"`    // true to enable dog bark filter
	Confidence float32  `json:"confidence"` // confidence threshold for dog bark detection
	Remember   int      `json:"remember"`   // how long we should remember bark for filtering?
	Species    []string `json:"species"`    // species list for filtering
}

// RTSPHealthSettings contains settings for RTSP stream health monitoring.
type RTSPHealthSettings struct {
	HealthyDataThreshold int `json:"healthyDataThreshold"` // seconds before stream considered unhealthy (default: 60)
	MonitoringInterval   int `json:"monitoringInterval"`   // health check interval in seconds (default: 30)
}

// RTSPSettings contains settings for RTSP streaming.
type RTSPSettings struct {
	Transport        string             `json:"transport"`        // RTSP Transport Protocol
	URLs             []string           `json:"urls"`             // RTSP stream URL
	Health           RTSPHealthSettings `json:"health"`           // health monitoring settings
	FFmpegParameters []string           `json:"ffmpegParameters"` // optional custom FFmpeg parameters
}

// MQTTSettings contains settings for MQTT integration.
type MQTTSettings struct {
	Enabled       bool            `json:"enabled"`       // true to enable MQTT
	Debug         bool            `json:"debug"`         // true to enable MQTT debug
	Broker        string          `json:"broker"`        // MQTT broker URL
	Topic         string          `json:"topic"`         // MQTT topic
	Username      string          `json:"username"`      // MQTT username
	Password      string          `json:"password"`      // MQTT password
	Retain        bool            `json:"retain"`        // true to retain messages
	RetrySettings RetrySettings   `json:"retrySettings"` // settings for retry mechanism
	TLS           MQTTTLSSettings `json:"tls"`           // TLS/SSL configuration
}

// MQTTTLSSettings contains TLS/SSL configuration for secure MQTT connections
type MQTTTLSSettings struct {
	Enabled            bool   `json:"enabled"`                                          // true to enable TLS (auto-detected from broker URL)
	InsecureSkipVerify bool   `json:"insecureSkipVerify"`                               // true to skip certificate verification (for self-signed certs)
	CACert             string `yaml:"cacert,omitempty" json:"caCert,omitempty"`         // path to CA certificate file (managed internally)
	ClientCert         string `yaml:"clientcert,omitempty" json:"clientCert,omitempty"` // path to client certificate file (managed internally)
	ClientKey          string `yaml:"clientkey,omitempty" json:"clientKey,omitempty"`   // path to client key file (managed internally)
}

// TelemetrySettings contains settings for telemetry.
type TelemetrySettings struct {
	Enabled bool   `json:"enabled"` // true to enable Prometheus compatible telemetry endpoint
	Listen  string `json:"listen"`  // IP address and port to listen on
}

// MonitoringSettings contains settings for system resource monitoring
type MonitoringSettings struct {
	Enabled                bool                  `json:"enabled"`                // true to enable system resource monitoring
	CheckInterval          int                   `json:"checkInterval"`          // interval in seconds between resource checks
	CriticalResendInterval int                   `json:"criticalResendInterval"` // interval in minutes between critical alert resends (default: 30)
	HysteresisPercent      float64               `json:"hysteresisPercent"`      // hysteresis percentage for state transitions (default: 5.0)
	CPU                    ThresholdSettings     `json:"cpu"`                    // CPU usage thresholds
	Memory                 ThresholdSettings     `json:"memory"`                 // Memory usage thresholds
	Disk                   DiskThresholdSettings `json:"disk"`                   // Disk usage thresholds
}

// ThresholdSettings contains warning and critical thresholds
type ThresholdSettings struct {
	Enabled  bool    `json:"enabled"`  // true to enable monitoring for this resource
	Warning  float64 `json:"warning"`  // warning threshold percentage
	Critical float64 `json:"critical"` // critical threshold percentage
}

// DiskThresholdSettings contains disk monitoring configuration for multiple paths
type DiskThresholdSettings struct {
	Enabled  bool     `json:"enabled"`  // true to enable disk monitoring
	Warning  float64  `json:"warning"`  // warning threshold percentage
	Critical float64  `json:"critical"` // critical threshold percentage
	Paths    []string `json:"paths"`    // filesystem paths to monitor
}

// SentrySettings contains settings for Sentry error tracking
type SentrySettings struct {
	Enabled bool `json:"enabled"` // true to enable Sentry error tracking (opt-in)
	Debug   bool `json:"debug"`   // true to enable transparent telemetry logging
}

// RealtimeSettings contains all settings related to realtime processing.
type RealtimeSettings struct {
	Interval         int                      `json:"interval"`         // minimum interval between log messages in seconds
	ProcessingTime   bool                     `json:"processingTime"`   // true to report processing time for each prediction
	Audio            AudioSettings            `json:"audio"`            // Audio processing settings
	Dashboard        Dashboard                `json:"dashboard"`        // Dashboard settings
	DynamicThreshold DynamicThresholdSettings `json:"dynamicThreshold"` // Dynamic threshold settings
	Log              struct {
		Enabled bool   `json:"enabled"` // true to enable OBS chat log
		Path    string `json:"path"`    // path to OBS chat log
	} `json:"log"`
	LogDeduplication LogDeduplicationSettings `json:"logDeduplication"` // Log deduplication settings
	Birdweather      BirdweatherSettings      `json:"birdweather"`      // Birdweather integration settings
	EBird            EBirdSettings            `json:"ebird"`            // eBird integration settings
	OpenWeather      OpenWeatherSettings      `yaml:"-" json:"-"`       // OpenWeather integration settings
	PrivacyFilter    PrivacyFilterSettings    `json:"privacyFilter"`    // Privacy filter settings
	DogBarkFilter    DogBarkFilterSettings    `json:"dogBarkFilter"`    // Dog bark filter settings
	RTSP             RTSPSettings             `json:"rtsp"`             // RTSP settings
	MQTT             MQTTSettings             `json:"mqtt"`             // MQTT settings
	Telemetry        TelemetrySettings        `json:"telemetry"`        // Telemetry settings
	Monitoring       MonitoringSettings       `json:"monitoring"`       // System resource monitoring settings
	Species          SpeciesSettings          `json:"species"`          // Custom thresholds and actions for species
	Weather          WeatherSettings          `json:"weather"`          // Weather provider related settings
	SpeciesTracking  SpeciesTrackingSettings  `json:"speciesTracking"`  // New species tracking settings
}

// SpeciesAction represents a single action configuration
type SpeciesAction struct {
	Type            string   `yaml:"type" json:"type"`                       // Type of action (ExecuteCommand, etc)
	Command         string   `yaml:"command" json:"command"`                 // Path to the command to execute
	Parameters      []string `yaml:"parameters" json:"parameters"`           // Action parameters
	ExecuteDefaults bool     `yaml:"executeDefaults" json:"executeDefaults"` // Whether to also execute default actions
}

// SpeciesConfig represents configuration for a specific species
type SpeciesConfig struct {
	Threshold float64         `yaml:"threshold" json:"threshold"`                   // Confidence threshold
	Interval  int             `yaml:"interval,omitempty" json:"interval,omitempty"` // New field: Custom interval in seconds
	Actions   []SpeciesAction `yaml:"actions" json:"actions"`                       // List of actions to execute
}

// RealtimeSpeciesSettings contains all species-specific settings
type SpeciesSettings struct {
	Include []string                 `yaml:"include" json:"include"` // Always include these species
	Exclude []string                 `yaml:"exclude" json:"exclude"` // Always exclude these species
	Config  map[string]SpeciesConfig `yaml:"config" json:"config"`   // Per-species configuration
}

// LogDeduplicationSettings contains settings for log deduplication
type LogDeduplicationSettings struct {
	Enabled                    bool `json:"enabled"`                    // true to enable log deduplication
	HealthCheckIntervalSeconds int  `json:"healthCheckIntervalSeconds"` // Health check interval in seconds (default: 60)
}

// SpeciesTrackingSettings contains settings for tracking new species
type SpeciesTrackingSettings struct {
	Enabled              bool                     `json:"enabled"`              // true to enable new species tracking
	NewSpeciesWindowDays int                      `json:"newSpeciesWindowDays"` // Days to consider a species "new" (default: 14)
	SyncIntervalMinutes  int                      `json:"syncIntervalMinutes"`  // Interval to sync with database (default: 60)
	NotificationSuppressionHours int             `json:"notificationSuppressionHours"` // Hours to suppress duplicate notifications (default: 168)
	YearlyTracking       YearlyTrackingSettings   `json:"yearlyTracking"`       // Settings for yearly species tracking
	SeasonalTracking     SeasonalTrackingSettings `json:"seasonalTracking"`     // Settings for seasonal species tracking
}

// YearlyTrackingSettings contains settings for tracking first arrivals each year
type YearlyTrackingSettings struct {
	Enabled    bool `json:"enabled"`    // true to enable yearly tracking
	ResetMonth int  `json:"resetMonth"` // Month to reset yearly tracking (1=January, default: 1)
	ResetDay   int  `json:"resetDay"`   // Day to reset yearly tracking (default: 1)
	WindowDays int  `json:"windowDays"` // Days to show "new this year" indicator (default: 30)
}

// SeasonalTrackingSettings contains settings for tracking first arrivals each season
type SeasonalTrackingSettings struct {
	Enabled    bool              `json:"enabled"`    // true to enable seasonal tracking
	WindowDays int               `json:"windowDays"` // Days to show "new this season" indicator (default: 21)
	Seasons    map[string]Season `json:"seasons"`    // Season definitions
}

// Season defines the start date for a season
type Season struct {
	StartMonth int `json:"startMonth"` // Month when season starts (1-12)
	StartDay   int `json:"startDay"`   // Day when season starts (1-31)
}

// DetectHemisphere determines the hemisphere based on latitude
// Returns "equatorial" for latitudes between -10 and 10 degrees,
// "northern" for latitude > 10, "southern" for latitude < -10
func DetectHemisphere(latitude float64) string {
	if latitude > 10 {
		return "northern"
	} else if latitude < -10 {
		return "southern"
	}
	return "equatorial"
}

// GetSeasonalTrackingWithHemisphere returns seasonal tracking configuration adjusted for hemisphere
// For southern hemisphere, seasons are shifted by 6 months
func GetSeasonalTrackingWithHemisphere(settings SeasonalTrackingSettings, latitude float64) SeasonalTrackingSettings {
	// If no custom seasons are defined, use defaults based on hemisphere
	if len(settings.Seasons) == 0 {
		settings.Seasons = GetDefaultSeasons(latitude)
	}
	return settings
}

// GetDefaultSeasons returns default seasons based on hemisphere
func GetDefaultSeasons(latitude float64) map[string]Season {
	hemisphere := DetectHemisphere(latitude)

	switch hemisphere {
	case "northern":
		// Northern hemisphere seasons
		return map[string]Season{
			"spring": {StartMonth: 3, StartDay: 20},  // March 20
			"summer": {StartMonth: 6, StartDay: 21},  // June 21
			"fall":   {StartMonth: 9, StartDay: 22},  // September 22
			"winter": {StartMonth: 12, StartDay: 21}, // December 21
		}
	case "southern":
		// Southern hemisphere seasons (shifted by 6 months)
		return map[string]Season{
			"spring": {StartMonth: 9, StartDay: 22},  // September 22
			"summer": {StartMonth: 12, StartDay: 21}, // December 21
			"fall":   {StartMonth: 3, StartDay: 20},  // March 20
			"winter": {StartMonth: 6, StartDay: 21},  // June 21
		}
	default: // equatorial
		// Equatorial regions typically have wet and dry seasons
		// Using approximate dates common to many equatorial regions
		return map[string]Season{
			"wet1": {StartMonth: 3, StartDay: 1},  // March-May wet season
			"dry1": {StartMonth: 6, StartDay: 1},  // June-August dry season
			"wet2": {StartMonth: 9, StartDay: 1},  // September-November wet season
			"dry2": {StartMonth: 12, StartDay: 1}, // December-February dry season
		}
	}
}

// Validate validates the SpeciesTrackingSettings configuration
func (s *SpeciesTrackingSettings) Validate() error {
	// Validate window days
	if s.NewSpeciesWindowDays < 1 || s.NewSpeciesWindowDays > 365 {
		return errors.Newf("new species window days must be between 1 and 365, got %d", s.NewSpeciesWindowDays).
			Component("config").
			Category(errors.CategoryValidation).
			Build()
	}

	// Validate sync interval
	if s.SyncIntervalMinutes < 1 || s.SyncIntervalMinutes > 1440 { // 1440 minutes = 24 hours
		return errors.Newf("sync interval minutes must be between 1 and 1440, got %d", s.SyncIntervalMinutes).
			Component("config").
			Category(errors.CategoryValidation).
			Build()
	}

	// Validate yearly tracking if enabled
	if s.YearlyTracking.Enabled {
		if err := s.YearlyTracking.Validate(); err != nil {
			return err
		}
	}

	// Validate seasonal tracking if enabled
	if s.SeasonalTracking.Enabled {
		if err := s.SeasonalTracking.Validate(); err != nil {
			return err
		}
	}

	return nil
}

// Validate validates the YearlyTrackingSettings configuration
func (y *YearlyTrackingSettings) Validate() error {
	// Validate window days
	if y.WindowDays < 1 || y.WindowDays > 365 {
		return errors.Newf("yearly window days must be between 1 and 365, got %d", y.WindowDays).
			Component("config").
			Category(errors.CategoryValidation).
			Build()
	}

	// Validate reset month
	if y.ResetMonth < 1 || y.ResetMonth > 12 {
		return errors.Newf("reset month must be between 1 and 12, got %d", y.ResetMonth).
			Component("config").
			Category(errors.CategoryValidation).
			Build()
	}

	// Validate reset day based on month
	maxDays := 31
	switch y.ResetMonth {
	case 2: // February
		maxDays = 29
	case 4, 6, 9, 11: // April, June, September, November
		maxDays = 30
	}

	if y.ResetDay < 1 || y.ResetDay > maxDays {
		return errors.Newf("reset day must be between 1 and %d for month %d, got %d", maxDays, y.ResetMonth, y.ResetDay).
			Component("config").
			Category(errors.CategoryValidation).
			Build()
	}

	return nil
}

// Validate validates the SeasonalTrackingSettings configuration
func (s *SeasonalTrackingSettings) Validate() error {
	// Validate window days
	if s.WindowDays < 1 || s.WindowDays > 365 {
		return errors.Newf("seasonal window days must be between 1 and 365, got %d", s.WindowDays).
			Component("config").
			Category(errors.CategoryValidation).
			Build()
	}

	// Validate seasons if custom ones are defined
	if len(s.Seasons) > 0 {
		for name, season := range s.Seasons {
			if err := season.Validate(name); err != nil {
				return err
			}
		}

		// Check that we have at least 2 seasons
		if len(s.Seasons) < 2 {
			return errors.Newf("at least 2 seasons must be defined, got %d", len(s.Seasons)).
				Component("config").
				Category(errors.CategoryValidation).
				Build()
		}
	}

	return nil
}

// Validate validates a Season configuration
func (s *Season) Validate(name string) error {
	// Validate month
	if s.StartMonth < 1 || s.StartMonth > 12 {
		return errors.Newf("season '%s' start month must be between 1 and 12, got %d", name, s.StartMonth).
			Component("config").
			Category(errors.CategoryValidation).
			Build()
	}

	// Validate day based on month
	maxDays := 31
	switch s.StartMonth {
	case 2: // February
		maxDays = 29
	case 4, 6, 9, 11: // April, June, September, November
		maxDays = 30
	}

	if s.StartDay < 1 || s.StartDay > maxDays {
		return errors.Newf("season '%s' start day must be between 1 and %d for month %d, got %d", name, maxDays, s.StartMonth, s.StartDay).
			Component("config").
			Category(errors.CategoryValidation).
			Build()
	}

	return nil
}

// ActionConfig holds configuration details for a specific action.
type ActionConfig struct {
	Type       string   `json:"type"`       // Type of the action (e.g. ExecuteScript which is only type for now)
	Parameters []string `json:"parameters"` // List of parameters for the action
}

// InputConfig holds settings for file or directory analysis
type InputConfig struct {
	Path      string `yaml:"-" json:"-"` // path to input file or directory
	Recursive bool   `yaml:"-" json:"-"` // true for recursive directory analysis
	Watch     bool   `yaml:"-" json:"-"` // true to watch directory for new files
}

type BirdNETConfig struct {
	Debug       bool                `json:"debug"`       // true to enable debug mode
	Sensitivity float64             `json:"sensitivity"` // birdnet analysis sigmoid sensitivity
	Threshold   float64             `json:"threshold"`   // threshold for prediction confidence to report
	Overlap     float64             `json:"overlap"`     // birdnet analysis overlap between chunks
	Longitude   float64             `json:"longitude"`   // longitude of recording location for prediction filtering
	Latitude    float64             `json:"latitude"`    // latitude of recording location for prediction filtering
	Threads     int                 `json:"threads"`     // number of CPU threads to use for analysis
	Locale      string              `json:"locale"`      // language to use for labels
	RangeFilter RangeFilterSettings `json:"rangeFilter"` // range filter settings
	ModelPath   string              `json:"modelPath"`   // path to external model file (empty for embedded)
	LabelPath   string              `json:"labelPath"`   // path to external label file (empty for embedded)
	Labels      []string            `yaml:"-" json:"-"`  // list of available species labels, runtime value
	UseXNNPACK  bool                `json:"useXnnpack"`  // true to use XNNPACK delegate for inference acceleration
}

// RangeFilterSettings contains settings for the range filter
type RangeFilterSettings struct {
	Debug       bool      `json:"debug"`                          // true to enable debug mode
	Model       string    `json:"model"`                          // range filter model version: "legacy" for v1, or empty/default for v2
	ModelPath   string    `json:"modelPath"`                      // path to external meta model file (empty for embedded)
	Threshold   float32   `json:"threshold"`                      // rangefilter species occurrence threshold
	Species     []string  `yaml:"-" json:"species,omitempty"`     // list of included species, runtime value
	LastUpdated time.Time `yaml:"-" json:"lastUpdated,omitempty"` // last time the species list was updated, runtime value
}

// BasicAuth holds settings for the password authentication
type BasicAuth struct {
	Enabled        bool          `json:"enabled"`        // true to enable password authentication
	Password       string        `json:"password"`       // password for admin interface
	ClientID       string        `json:"clientId"`       // client id for OAuth2
	ClientSecret   string        `json:"clientSecret"`   // client secret for OAuth2
	RedirectURI    string        `json:"redirectUri"`    // redirect uri for OAuth2
	AuthCodeExp    time.Duration `json:"authCodeExp"`    // duration for authorization code
	AccessTokenExp time.Duration `json:"accessTokenExp"` // duration for access token
}

// SocialProvider holds settings for an OAuth2 identity provider
type SocialProvider struct {
	Enabled      bool   `json:"enabled"`      // true to enable social provider
	ClientID     string `json:"clientId"`     // client id for OAuth2
	ClientSecret string `json:"clientSecret"` // client secret for OAuth2
	RedirectURI  string `json:"redirectUri"`  // redirect uri for OAuth2
	UserId       string `json:"userId"`       // valid user id for OAuth2
}

type AllowSubnetBypass struct {
	Enabled bool   `json:"enabled"` // true to enable subnet bypass
	Subnet  string `json:"subnet"`  // disable OAuth2 in subnet
}

// SecurityConfig handles all security-related settings and validations
// for the application, including authentication, TLS, and access control.
type Security struct {
	Debug bool `json:"debug"` // true to enable debug mode

	// Host is the primary hostname used for TLS certificates
	// and OAuth redirect URLs. Required when using AutoTLS or
	// authentication providers. Used to form the redirect URIs.
	Host string `json:"host"`

	// AutoTLS enables automatic TLS certificate management using
	// Let's Encrypt. Requires Host to be set and port 80/443 access.
	AutoTLS bool `json:"autoTls"`

	RedirectToHTTPS   bool              `json:"redirectToHttps"`   // true to redirect to HTTPS
	AllowSubnetBypass AllowSubnetBypass `json:"allowSubnetBypass"` // subnet bypass configuration
	BasicAuth         BasicAuth         `json:"basicAuth"`         // password authentication configuration
	GoogleAuth        SocialProvider    `json:"googleAuth"`        // Google OAuth2 configuration
	GithubAuth        SocialProvider    `json:"githubAuth"`        // Github OAuth2 configuration
	SessionSecret     string            `json:"sessionSecret"`     // secret for session cookie
	SessionDuration   time.Duration     `json:"sessionDuration"`   // duration for browser session cookies
}

type WebServerSettings struct {
	Debug      bool               `json:"debug"`      // true to enable debug mode
	Enabled    bool               `json:"enabled"`    // true to enable web server
	Port       string             `json:"port"`       // port for web server
	Log        LogConfig          `json:"log"`        // logging configuration for web server
	LiveStream LiveStreamSettings `json:"liveStream"` // live stream configuration
}

type LiveStreamSettings struct {
	Debug          bool   `json:"debug"`          // true to enable debug mode
	BitRate        int    `json:"bitRate"`        // bitrate for live stream in kbps
	SampleRate     int    `json:"sampleRate"`     // sample rate for live stream in Hz
	SegmentLength  int    `json:"segmentLength"`  // length of each segment in seconds
	FfmpegLogLevel string `json:"ffmpegLogLevel"` // log level for ffmpeg
}

// BackupRetention defines backup retention policy
type BackupRetention struct {
	MaxAge     string `yaml:"maxage" json:"maxAge"`         // Duration string for the maximum age of backups to keep (e.g., "30d" for 30 days, "6m" for 6 months, "1y" for 1 year). Backups older than this may be deleted.
	MaxBackups int    `yaml:"maxbackups" json:"maxBackups"` // Maximum total number of backups to keep for a given source. If 0, no limit by count (only by age or MinBackups).
	MinBackups int    `yaml:"minbackups" json:"minBackups"` // Minimum number of recent backups to keep for a given source, regardless of their age. This ensures a baseline number of backups are always available.
}

// BackupTargetSettings is an interface for type-safe backup target configuration
type BackupTargetSettings interface {
	Validate() error
}

// LocalBackupSettings defines settings for local filesystem backup target
type LocalBackupSettings struct {
	Path string `yaml:"path" json:"path"` // Local filesystem path where backups will be stored
}

// Validate validates local backup settings
func (s *LocalBackupSettings) Validate() error {
	if s.Path == "" {
		return fmt.Errorf("local backup path cannot be empty")
	}
	return nil
}

// FTPBackupSettings defines settings for FTP backup target
type FTPBackupSettings struct {
	Host     string `yaml:"host" json:"host"`         // FTP server hostname or IP address
	Port     int    `yaml:"port" json:"port"`         // FTP server port (default: 21)
	Username string `yaml:"username" json:"username"` // FTP username
	Password string `yaml:"password" json:"password"` // FTP password
	Path     string `yaml:"path" json:"path"`         // Remote path on FTP server
	UseTLS   bool   `yaml:"usetls" json:"useTls"`     // Use FTPS (FTP over TLS)
}

// Validate validates FTP backup settings
func (s *FTPBackupSettings) Validate() error {
	if s.Host == "" {
		return fmt.Errorf("FTP host cannot be empty")
	}
	if s.Port == 0 {
		s.Port = 21 // Set default port
	}
	return nil
}

// SFTPBackupSettings defines settings for SFTP backup target
type SFTPBackupSettings struct {
	Host           string `yaml:"host"`           // SFTP server hostname or IP address
	Port           int    `yaml:"port"`           // SFTP server port (default: 22)
	Username       string `yaml:"username"`       // SFTP username
	Password       string `yaml:"password"`       // SFTP password (optional if using key)
	PrivateKeyPath string `yaml:"privatekeypath"` // Path to private key file (optional)
	Path           string `yaml:"path"`           // Remote path on SFTP server
}

// Validate validates SFTP backup settings
func (s *SFTPBackupSettings) Validate() error {
	if s.Host == "" {
		return fmt.Errorf("SFTP host cannot be empty")
	}
	if s.Port == 0 {
		s.Port = 22 // Set default port
	}
	if s.Username == "" {
		return fmt.Errorf("SFTP username cannot be empty")
	}
	return nil
}

// S3BackupSettings defines settings for S3-compatible backup target
type S3BackupSettings struct {
	Endpoint        string `yaml:"endpoint"`        // S3 endpoint URL
	Region          string `yaml:"region"`          // AWS region
	Bucket          string `yaml:"bucket"`          // S3 bucket name
	AccessKeyID     string `yaml:"accesskeyid"`     // AWS access key ID
	SecretAccessKey string `yaml:"secretaccesskey"` // AWS secret access key
	Prefix          string `yaml:"prefix"`          // Object key prefix
	UseSSL          bool   `yaml:"usessl"`          // Use SSL/TLS (default: true)
}

// Validate validates S3 backup settings
func (s *S3BackupSettings) Validate() error {
	if s.Bucket == "" {
		return fmt.Errorf("S3 bucket name cannot be empty")
	}
	if s.Region == "" {
		return fmt.Errorf("S3 region cannot be empty")
	}
	return nil
}

// RsyncBackupSettings defines settings for rsync backup target
type RsyncBackupSettings struct {
	Host       string   `yaml:"host"`       // Remote host (optional for local rsync)
	Port       int      `yaml:"port"`       // SSH port for remote rsync (default: 22)
	Username   string   `yaml:"username"`   // SSH username for remote rsync
	Path       string   `yaml:"path"`       // Destination path
	SSHKeyPath string   `yaml:"sshkeypath"` // Path to SSH private key
	Options    []string `yaml:"options"`    // Additional rsync options
}

// Validate validates rsync backup settings
func (s *RsyncBackupSettings) Validate() error {
	if s.Path == "" {
		return fmt.Errorf("rsync path cannot be empty")
	}
	if s.Host != "" && s.Port == 0 {
		s.Port = 22 // Set default SSH port for remote rsync
	}
	return nil
}

// GoogleDriveBackupSettings defines settings for Google Drive backup target
type GoogleDriveBackupSettings struct {
	CredentialsPath string `yaml:"credentialspath"` // Path to Google service account credentials JSON
	FolderID        string `yaml:"folderid"`        // Google Drive folder ID where backups will be stored
}

// Validate validates Google Drive backup settings
func (s *GoogleDriveBackupSettings) Validate() error {
	if s.CredentialsPath == "" {
		return fmt.Errorf("google drive credentials path cannot be empty")
	}
	return nil
}

// BackupTarget defines settings for a backup target
type BackupTarget struct {
	Type     string         `yaml:"type" json:"type"`         // Specifies the type of the backup target (e.g., "local", "s3", "ftp", "sftp"). This determines the storage mechanism.
	Enabled  bool           `yaml:"enabled" json:"enabled"`   // If true, this backup target will be used for storing backups. At least one target should be enabled for backups to be stored.
	Settings map[string]any `yaml:"settings" json:"settings"` // A map of key-value pairs for target-specific settings. TODO: Consider using BackupTargetSettings interface for type safety after implementing custom YAML unmarshaling.
}

// BackupScheduleConfig defines a single backup schedule
type BackupScheduleConfig struct {
	Enabled  bool   `yaml:"enabled" json:"enabled"`   // If true, this specific schedule is active and backups will be attempted at the defined interval. (Valid: true or false)
	Hour     int    `yaml:"hour" json:"hour"`         // The hour of the day when the backup is scheduled to run. (Valid range: 0-23, where 0 is midnight and 23 is 11 PM)
	Minute   int    `yaml:"minute" json:"minute"`     // The minute of the hour when the backup is scheduled to run. (Valid range: 0-59)
	Weekday  string `yaml:"weekday" json:"weekday"`   // For weekly schedules, the day of the week. Accepts: "Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday" (case-insensitive), or numeric: "0" (Sunday) through "6" (Saturday). Empty or ignored for daily schedules.
	IsWeekly bool   `yaml:"isweekly" json:"isWeekly"` // If true, this schedule is weekly (runs on the specified Weekday at Hour:Minute). If false, it's a daily schedule (runs every day at Hour:Minute). (Valid: true or false)
}

// BackupConfig contains backup-related configuration
type BackupConfig struct {
	Enabled        bool                   `yaml:"enabled" json:"enabled"`                // Global flag to enable or disable the entire backup system. If false, no backups (manual or scheduled) will occur.
	Debug          bool                   `yaml:"debug" json:"debug"`                    // If true, enables detailed debug logging for backup operations.
	Encryption     bool                   `yaml:"encryption" json:"encryption"`          // If true, enables encryption for backup archives. Requires EncryptionKey to be set.
	EncryptionKey  string                 `yaml:"encryption_key" json:"encryptionKey"`   // Base64-encoded encryption key used for AES-256-GCM encryption of backup archives. Must be kept secret and safe.
	SanitizeConfig bool                   `yaml:"sanitize_config" json:"sanitizeConfig"` // If true, sensitive information (like passwords, API keys) will be removed from the configuration file copy that is included in the backup archive.
	Retention      BackupRetention        `yaml:"retention" json:"retention"`            // Defines policies for how long and how many backups are kept.
	Targets        []BackupTarget         `yaml:"targets" json:"targets"`                // A list of configured backup targets (destinations) where backup archives will be stored.
	Schedules      []BackupScheduleConfig `yaml:"schedules" json:"schedules"`            // A list of schedules (e.g., daily, weekly) that define when automatic backups should run.

	// OperationTimeouts defines timeouts for various backup operations
	OperationTimeouts struct {
		Backup  time.Duration `yaml:"backup" json:"backup"`   // Maximum duration allowed for the entire backup operation for a single source (including data extraction, archiving, compression, encryption). Default: 2h.
		Store   time.Duration `yaml:"store" json:"store"`     // Maximum duration allowed for storing a single backup archive to one target. Default: 15m.
		Cleanup time.Duration `yaml:"cleanup" json:"cleanup"` // Maximum duration allowed for the backup cleanup process (deleting old backups based on retention policy). Default: 10m.
		Delete  time.Duration `yaml:"delete" json:"delete"`   // Maximum duration allowed for deleting a single backup archive from a target. Default: 2m.
	} `json:"operationTimeouts"`
}

// Settings contains all configuration options for the BirdNET-Go application.
type Settings struct {
	Debug bool `json:"debug"` // true to enable debug mode

	// Runtime values, not stored in config file
	Version            string   `yaml:"-" json:"version,omitempty"`            // Version from build
	BuildDate          string   `yaml:"-" json:"buildDate,omitempty"`          // Build date from build
	SystemID           string   `yaml:"-" json:"systemId,omitempty"`           // Unique system identifier for telemetry
	ValidationWarnings []string `yaml:"-" json:"validationWarnings,omitempty"` // Configuration validation warnings for telemetry

	Main struct {
		Name      string    `json:"name"`      // name of BirdNET-Go node, can be used to identify source of notes
		TimeAs24h bool      `json:"timeAs24h"` // true 24-hour time format, false 12-hour time format
		Log       LogConfig `json:"log"`       // logging configuration
	} `json:"main"`

	BirdNET BirdNETConfig `json:"birdnet"` // BirdNET configuration

	Input InputConfig `yaml:"-" json:"-"` // Input configuration for file and directory analysis

	Realtime  RealtimeSettings  `json:"realtime"`  // Realtime processing settings
	WebServer WebServerSettings `json:"webServer"` // web server configuration
	Security  Security          `json:"security"`  // security configuration
	Sentry    SentrySettings    `json:"sentry"`    // Sentry error tracking configuration

	Output struct {
		File struct {
			Enabled bool   `yaml:"-" json:"-"` // true to enable file output
			Path    string `yaml:"-" json:"-"` // directory to output results
			Type    string `yaml:"-" json:"-"` // table, csv
		} `json:"file"`

		SQLite struct {
			Enabled bool   `json:"enabled"` // true to enable sqlite output
			Path    string `json:"path"`    // path to sqlite database
		} `json:"sqlite"`

		MySQL struct {
			Enabled  bool   `json:"enabled"`  // true to enable mysql output
			Username string `json:"username"` // username for mysql database
			Password string `json:"password"` // password for mysql database
			Database string `json:"database"` // database name for mysql database
			Host     string `json:"host"`     // host for mysql database
			Port     string `json:"port"`     // port for mysql database
		} `json:"mysql"`
	} `json:"output"`

	Backup BackupConfig `json:"backup"` // Backup configuration
}

// LogConfig defines the configuration for a log file
type LogConfig struct {
	Enabled     bool         `json:"enabled"`     // true to enable this log
	Path        string       `json:"path"`        // Path to the log file
	Rotation    RotationType `json:"rotation"`    // Type of log rotation
	MaxSize     int64        `json:"maxSize"`     // Max size in bytes for RotationSize
	RotationDay string       `json:"rotationDay"` // Day of the week for RotationWeekly (as a string: "Sunday", "Monday", etc.)
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
		return nil, errors.New(err).
			Category(errors.CategoryConfiguration).
			Context("operation", "init-viper").
			Build()
	}

	// Unmarshal the config into settings
	if err := viper.Unmarshal(settings); err != nil {
		return nil, errors.New(err).
			Category(errors.CategoryConfiguration).
			Context("operation", "unmarshal-config").
			Build()
	}

	// Auto-generate SessionSecret if not set (for backward compatibility)
	if settings.Security.SessionSecret == "" {
		// Generate a new session secret
		sessionSecret := GenerateRandomSecret()
		if sessionSecret == "" {
			return nil, errors.Newf("failed to generate session secret").
				Component("conf").
				Category(errors.CategoryConfiguration).
				Context("operation", "generate_session_secret").
				Build()
		}

		settings.Security.SessionSecret = sessionSecret

		// Also set it in viper so it gets saved to config file
		viper.Set("security.sessionsecret", sessionSecret)

		// Log that we generated a new session secret
		log.Printf("Generated new SessionSecret for existing configuration")

		// Save the updated config back to file to persist the generated secret
		// This ensures the secret remains the same across restarts
		configFile := viper.ConfigFileUsed()
		if configFile != "" {
			if err := SaveYAMLConfig(configFile, settings); err != nil {
				// Log the error but don't fail - the generated secret will work for this session
				log.Printf("Warning: Failed to save generated SessionSecret to config file: %v", err)
			} else {
				// Set secure file permissions after saving
				if err := os.Chmod(configFile, 0o600); err != nil {
					log.Printf("Warning: Failed to set secure permissions on config file: %v", err)
				}
			}
		}
	}

	// Validate settings
	if err := ValidateSettings(settings); err != nil {
		// Check if it's just a validation warning (contains fallback info)
		var validationErr ValidationError
		if errors.As(err, &validationErr) {
			// Report configuration issues to telemetry for debugging
			for _, errMsg := range validationErr.Errors {
				if strings.Contains(errMsg, "fallback") || strings.Contains(errMsg, "not supported") ||
					strings.Contains(errMsg, "OAuth authentication warning") {
					// This is a warning - report to telemetry but don't fail
					log.Printf("Configuration warning: %s", errMsg)
					// Store the warning for later telemetry reporting
					settings.ValidationWarnings = append(settings.ValidationWarnings, errMsg)
					// Note: Telemetry reporting will happen later in birdnet package when Sentry is initialized
				} else {
					// This is a real validation error - fail the config load
					return nil, errors.New(err).
						Category(errors.CategoryValidation).
						Context("component", "settings").
						Context("error_msg", errMsg).
						Build()
				}
			}
		} else {
			// Other validation errors should fail the config load
			return nil, errors.New(err).
				Category(errors.CategoryValidation).
				Context("component", "settings").
				Build()
		}
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
		return errors.New(err).
			Category(errors.CategoryConfiguration).
			Context("operation", "get-config-paths").
			Build()
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
		// Report critical config file read errors
		return errors.New(err).
			Category(errors.CategoryFileIO).
			Context("operation", "read-config-file").
			Build()
	}

	return nil
}

// createDefaultConfig creates a default config file and writes it to the default config path
func createDefaultConfig() error {
	configPaths, err := GetDefaultConfigPaths()
	if err != nil {
		return errors.New(err).
			Category(errors.CategoryConfiguration).
			Context("operation", "create-default-config-paths").
			Build()
	}
	configPath := filepath.Join(configPaths[0], "config.yaml")
	defaultConfig := getDefaultConfig()

	// If the basicauth secret is not set, generate a random one
	if viper.GetString("security.basicauth.clientsecret") == "" {
		viper.Set("security.basicauth.clientsecret", GenerateRandomSecret())
	}
	// If the session secret is not set, generate a random one
	// This ensures backward compatibility for existing deployments
	if viper.GetString("security.sessionsecret") == "" {
		sessionSecret := GenerateRandomSecret()
		if sessionSecret == "" {
			return errors.Newf("failed to generate session secret for default config").
				Component("conf").
				Category(errors.CategoryConfiguration).
				Context("operation", "create_default_config").
				Build()
		}
		viper.Set("security.sessionsecret", sessionSecret)
	}

	// Create directories for config file
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		return errors.New(err).
			Category(errors.CategoryFileIO).
			Context("operation", "create-config-dirs").
			Context("path", filepath.Dir(configPath)).
			Build()
	}

	// Write default config file with secure permissions (0600)
	// Only the owner should be able to read/write the config file for security
	if err := os.WriteFile(configPath, []byte(defaultConfig), 0o600); err != nil {
		return errors.New(err).
			Category(errors.CategoryFileIO).
			Context("operation", "write-default-config").
			Context("path", configPath).
			Build()
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
		return errors.New(err).
			Category(errors.CategoryFileIO).
			Context("operation", "find-config-file").
			Build()
	}

	// Save the settings to the config file
	if err := SaveYAMLConfig(configPath, &settingsCopy); err != nil {
		return errors.New(err).
			Category(errors.CategoryFileIO).
			Context("operation", "save-yaml-config").
			Context("path", configPath).
			Build()
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
				// Fatal error loading settings - application cannot continue
				enhancedErr := errors.New(err).
					Category(errors.CategoryConfiguration).
					Context("operation", "load-settings-init").
					Build()
				log.Fatalf("Error loading settings: %v", enhancedErr)
			}
		}
	})
	return GetSettings()
}

// SetTestSettings allows tests to inject their own settings instance.
// This must be called before any call to Setting() to be effective.
// This is intended for testing purposes only.
func SetTestSettings(settings *Settings) {
	settingsMutex.Lock()
	defer settingsMutex.Unlock()
	settingsInstance = settings
	// Reset the sync.Once to allow reinitialization in tests
	once = sync.Once{}
}

// GetTestSettings returns a copy of default settings suitable for testing.
// This creates isolated settings that won't affect the global configuration.
func GetTestSettings() *Settings {
	settings := &Settings{}

	// Initialize with defaults
	settings.Debug = false
	settings.Main.Name = "BirdNET-Go-Test"
	settings.Main.TimeAs24h = true

	// Set up minimal test configuration
	settings.BirdNET.Sensitivity = 1.0
	settings.BirdNET.Threshold = 0.8
	settings.BirdNET.Overlap = 0.0
	settings.BirdNET.Locale = "en"

	// Dashboard settings with thumbnails
	settings.Realtime.Dashboard.Thumbnails.Debug = false
	settings.Realtime.Dashboard.Thumbnails.Summary = false
	settings.Realtime.Dashboard.Thumbnails.Recent = true
	settings.Realtime.Dashboard.Thumbnails.ImageProvider = "avicommons"
	settings.Realtime.Dashboard.Thumbnails.FallbackPolicy = "none"

	// Other realtime settings
	settings.Realtime.Interval = 15
	settings.Realtime.ProcessingTime = false

	// Web server settings
	settings.WebServer.Enabled = false
	settings.WebServer.Port = "8080"

	// Output settings
	settings.Output.SQLite.Enabled = false
	settings.Output.SQLite.Path = ":memory:"

	return settings
}

// Note: SendValidationWarningsAsNotifications function removed as it was unused

// SaveYAMLConfig updates the YAML configuration file with new settings.
// It overwrites the existing file, not preserving comments or structure.
func SaveYAMLConfig(configPath string, settings *Settings) error {
	// Marshal the settings struct to YAML
	yamlData, err := yaml.Marshal(settings)
	if err != nil {
		return errors.New(err).
			Category(errors.CategoryConfiguration).
			Context("operation", "yaml-marshal").
			Build()
	}

	// Write the YAML data to a temporary file
	// This is done to ensure atomic write operation
	tempFile, err := os.CreateTemp(filepath.Dir(configPath), "config-*.yaml")
	if err != nil {
		return errors.New(err).
			Category(errors.CategoryFileIO).
			Context("operation", "create-temp-file").
			Context("dir", filepath.Dir(configPath)).
			Build()
	}
	tempFileName := tempFile.Name()
	// Ensure the temporary file is removed in case of any failure
	defer func() {
		if err := os.Remove(tempFileName); err != nil && !os.IsNotExist(err) {
			log.Printf("Failed to remove temporary file: %v", err)
		}
	}()

	// Write the YAML data to the temporary file
	if _, err := tempFile.Write(yamlData); err != nil {
		// Best effort close on error path
		_ = tempFile.Close()
		return errors.New(err).
			Category(errors.CategoryFileIO).
			Context("operation", "write-temp-file").
			Build()
	}
	// Close the temporary file after writing
	if err := tempFile.Close(); err != nil {
		return errors.New(err).
			Category(errors.CategoryFileIO).
			Context("operation", "close-temp-file").
			Build()
	}

	// Try to rename the temporary file to replace the original config file
	// This is typically an atomic operation on most filesystems
	if err := os.Rename(tempFileName, configPath); err != nil {
		// If rename fails (e.g., cross-device link), fall back to copy & delete
		// This might happen when the temp directory is on a different filesystem
		if err := moveFile(tempFileName, configPath); err != nil {
			return errors.New(err).
				Category(errors.CategoryFileIO).
				Context("operation", "move-config-file").
				Context("src", tempFileName).
				Context("dst", configPath).
				Build()
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
		enhancedErr := errors.New(err).
			Category(errors.CategorySystem).
			Context("operation", "generate-random-secret").
			Build()
		log.Printf("Failed to generate random secret: %v", enhancedErr)
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(bytes)
}

// GetWeatherProvider returns the configured provider and its settings as any.
type WeatherProvider string

const (
	WeatherNone         WeatherProvider = "none"
	WeatherYrNo         WeatherProvider = "yrno"
	WeatherOpenWeather  WeatherProvider = "openweather"
	WeatherWunderground WeatherProvider = "wunderground"
)

// Prefer explicit settings return to avoid confusion at call sites.
func (s *Settings) GetWeatherProvider() (provider WeatherProvider, settings any) {
	p := s.Realtime.Weather.Provider
	switch p {
	case string(WeatherOpenWeather):
		return WeatherOpenWeather, s.Realtime.Weather.OpenWeather
	case string(WeatherWunderground):
		return WeatherWunderground, s.Realtime.Weather.Wunderground
	case string(WeatherYrNo), string(WeatherNone):
		return WeatherProvider(p), nil
	default:
		// Sensible default for legacy configs
		if s.Realtime.OpenWeather.Enabled {
			return WeatherOpenWeather, s.Realtime.OpenWeather
		}
		return WeatherYrNo, nil
	}
}

// ValidateWunderground validates Wunderground settings when the provider is "wunderground"
func (w *WundergroundSettings) ValidateWunderground() error {
	// Validate required fields when provider is "wunderground"
	if w.APIKey == "" {
		return fmt.Errorf("wunderground.apiKey is required when provider is wunderground")
	}
	if w.StationID == "" {
		return fmt.Errorf("wunderground.stationId is required when provider is wunderground")
	}
	// Validate units
	validUnits := map[string]bool{"m": true, "e": true, "h": true}
	if !validUnits[w.Units] {
		return fmt.Errorf("wunderground.units must be one of [m, e, h], got: %s", w.Units)
	}
	return nil
}
