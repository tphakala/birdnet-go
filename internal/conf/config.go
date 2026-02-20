// config.go: This file contains the configuration for the BirdNET-Go application. It defines the settings struct and functions to load and save the settings.
package conf

import (
	"crypto/rand"
	"embed"
	"encoding/base64"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/spf13/viper"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
	"gopkg.in/yaml.v3"
)

// Hemisphere detection thresholds
// Tropics of Cancer/Capricorn are at ±23.5°, but ±10° provides
// a practical buffer for equatorial weather patterns
const (
	NorthernHemisphereThreshold = 10.0
	SouthernHemisphereThreshold = -10.0
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
	Debug         bool                  `json:"debug" mapstructure:"debug"`                 // true to enable audio export debug
	Enabled       bool                  `json:"enabled" mapstructure:"enabled"`             // export audio clips containing indentified bird calls
	Path          string                `json:"path" mapstructure:"path"`                   // path to audio clip export directory
	Type          string                `json:"type" mapstructure:"type"`                   // audio file type, wav, mp3 or flac
	Bitrate       string                `json:"bitrate" mapstructure:"bitrate"`             // bitrate for audio export
	Retention     RetentionSettings     `json:"retention" mapstructure:"retention"`         // retention settings
	Length        int                   `json:"length" mapstructure:"length"`               // audio capture length in seconds
	PreCapture    int                   `json:"preCapture" mapstructure:"preCapture"`       // pre-capture in seconds
	Gain          float64               `json:"gain" mapstructure:"gain"`                   // gain in dB for audio capture
	Normalization NormalizationSettings `json:"normalization" mapstructure:"normalization"` // audio normalization settings (EBU R128)
}

// NormalizationSettings contains audio normalization configuration based on EBU R128 standard
type NormalizationSettings struct {
	Enabled       bool    `json:"enabled" mapstructure:"enabled"`             // true to enable loudness normalization
	TargetLUFS    float64 `json:"targetLUFS" mapstructure:"targetLUFS"`       // target integrated loudness in LUFS (default: -23)
	LoudnessRange float64 `json:"loudnessRange" mapstructure:"loudnessRange"` // loudness range in LU (default: 7)
	TruePeak      float64 `json:"truePeak" mapstructure:"truePeak"`           // true peak limit in dBTP (default: -2)
}

type RetentionSettings struct {
	Debug            bool   `json:"debug"`            // true to enable retention debug
	Policy           string `json:"policy"`           // retention policy, "none", "age" or "usage"
	MaxAge           string `json:"maxAge"`           // maximum age of audio clips to keep
	MaxUsage         string `json:"maxUsage"`         // maximum disk usage percentage before cleanup
	MinClips         int    `json:"minClips"`         // minimum number of clips per species to keep
	KeepSpectrograms bool   `json:"keepSpectrograms"` // true to keep spectrograms
	CheckInterval    int    `json:"checkInterval"`    // cleanup check interval in minutes (default: 15)
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
	Source          string             `yaml:"source" mapstructure:"source" json:"source"`             // audio source to use for analysis
	FfmpegPath      string             `yaml:"ffmpegpath" mapstructure:"ffmpegpath" json:"ffmpegPath"` // path to ffmpeg, runtime value
	FfmpegVersion   string             `yaml:"-" json:"ffmpegVersion,omitempty"`                       // ffmpeg version string, runtime value
	FfmpegMajor     int                `yaml:"-" json:"ffmpegMajor,omitempty"`                         // ffmpeg major version number, runtime value
	FfmpegMinor     int                `yaml:"-" json:"ffmpegMinor,omitempty"`                         // ffmpeg minor version number, runtime value
	SoxPath         string             `yaml:"soxpath" mapstructure:"soxpath" json:"soxPath"`          // path to sox, runtime value
	SoxAudioTypes   []string           `yaml:"-" json:"-"`                                             // supported audio types of sox, runtime value
	StreamTransport string             `json:"streamTransport"`                                        // preferred transport for audio streaming: "auto", "sse", or "ws"
	Export          ExportSettings     `json:"export"`                                                 // export settings
	SoundLevel      SoundLevelSettings `json:"soundLevel"`                                             // sound level monitoring settings

	Equalizer EqualizerSettings `json:"equalizer"` // equalizer settings
}

// NeedsFfprobeWorkaround returns true if the current FFmpeg version requires
// using ffprobe to get audio file length for spectrograms (FFmpeg 5.x bug).
// FFmpeg 7.x and later have this issue fixed.
func (a *AudioSettings) NeedsFfprobeWorkaround() bool {
	// FFmpeg 5.x has a bug that requires using ffprobe for audio duration
	// FFmpeg 7.x and later have this fixed
	return a.FfmpegMajor == 5
}

// HasFfmpegVersion returns true if FFmpeg version information has been detected and populated.
func (a *AudioSettings) HasFfmpegVersion() bool {
	return a.FfmpegVersion != "" && a.FfmpegMajor > 0
}

type Thumbnails struct {
	Debug          bool   `json:"debug"`          // true to enable debug mode
	Summary        bool   `json:"summary"`        // show thumbnails on summary table
	Recent         bool   `json:"recent"`         // show thumbnails on recent table
	ImageProvider  string `json:"imageProvider"`  // preferred image provider: "auto", "wikimedia", "avicommons"
	FallbackPolicy string `json:"fallbackPolicy"` // fallback policy: "none", "all" - try all available providers if preferred fails
}

// Temperature unit constants for display preference
const (
	TemperatureUnitCelsius    = "celsius"
	TemperatureUnitFahrenheit = "fahrenheit"
)

// Dashboard contains settings for the web dashboard.
type Dashboard struct {
	Thumbnails      Thumbnails           `json:"thumbnails"`       // thumbnails settings
	SummaryLimit    int                  `json:"summaryLimit"`     // limit for the number of species shown in the summary table
	Locale          string               `json:"locale,omitempty"` // UI locale setting
	Spectrogram     SpectrogramPreRender `json:"spectrogram"`      // Spectrogram pre-rendering settings
	TemperatureUnit string               `json:"temperatureUnit"`  // display unit for temperature: "celsius" or "fahrenheit"
}

// Spectrogram generation mode constants
const (
	SpectrogramModeAuto          = "auto"
	SpectrogramModePreRender     = "prerender"
	SpectrogramModeUserRequested = "user-requested"
)

// Spectrogram style preset constants
const (
	SpectrogramStyleDefault          = "default"
	SpectrogramStyleScientificDark   = "scientific_dark"
	SpectrogramStyleHighContrastDark = "high_contrast_dark"
	SpectrogramStyleScientific       = "scientific"
)

// Spectrogram dynamic range preset constants (dB values for Sox -z parameter).
// Lower values increase contrast, making weak signals stand out better.
// Higher values show more detail but with lower contrast.
const (
	SpectrogramDynamicRangeHighContrast = "80"  // High contrast - weak signals visible
	SpectrogramDynamicRangeStandard     = "100" // Standard - balanced (default)
	SpectrogramDynamicRangeExtended     = "120" // Extended - more detail, Sox default
)

// SpectrogramPreRender contains settings for spectrogram generation modes.
// Three modes control when and how spectrograms are generated:
//   - "auto": Generate on-demand when API is called (default, suitable for most systems)
//   - "prerender": Background worker generates during audio clip save (continuous CPU usage)
//   - "user-requested": Only generate when user clicks button in UI (zero automatic overhead)
type SpectrogramPreRender struct {
	Mode         string `json:"mode"         mapstructure:"mode"`         // Generation mode: "auto" (default), "prerender", "user-requested"
	Enabled      bool   `json:"enabled"      mapstructure:"enabled"`      // DEPRECATED: Use Mode instead. Kept for backward compatibility (true = "prerender", false = "auto")
	Size         string `json:"size"         mapstructure:"size"`         // Default size for all modes (see recommendations below)
	Raw          bool   `json:"raw"          mapstructure:"raw"`          // Generate raw spectrogram without axes/legend (default: true)
	Style        string `json:"style"        mapstructure:"style"`        // Visual style preset: "default", "scientific_dark", "high_contrast_dark", "scientific"
	DynamicRange string `json:"dynamicRange" mapstructure:"dynamicRange"` // Dynamic range in dB: "80" (high contrast), "100" (standard), "120" (extended)
}

// GetMode returns the effective spectrogram generation mode, handling backward compatibility.
// If Mode is explicitly set, it is used. Otherwise, it derives the mode from the deprecated
// Enabled field: true = "prerender", false = "auto".
func (s *SpectrogramPreRender) GetMode() string {
	// If Mode is explicitly set to a valid value, use it
	if s.Mode == SpectrogramModeAuto || s.Mode == SpectrogramModePreRender || s.Mode == SpectrogramModeUserRequested {
		return s.Mode
	}

	// Backward compatibility: derive from Enabled field
	if s.Enabled {
		return SpectrogramModePreRender
	}

	// Default to "auto" mode
	return SpectrogramModeAuto
}

// IsPreRenderEnabled returns true if spectrograms should be pre-rendered in background.
func (s *SpectrogramPreRender) IsPreRenderEnabled() bool {
	return s.GetMode() == "prerender"
}

// IsAutoMode returns true if spectrograms should be generated on-demand via API.
func (s *SpectrogramPreRender) IsAutoMode() bool {
	return s.GetMode() == "auto"
}

// IsUserRequestedMode returns true if spectrograms should only be generated on explicit user request.
func (s *SpectrogramPreRender) IsUserRequestedMode() bool {
	return s.GetMode() == "user-requested"
}

// Size recommendations for SpectrogramPreRender.Size:
//
//	"sm" (400px)  - Recommended. Used by recent detections card and detections list view
//	"md" (800px)  - Not currently used by web UI
//	"lg" (1000px) - Used by detailed detection view in web UI
//	"xl" (1200px) - Not currently used by web UI
//
// Choose "sm" for optimal performance - it covers the most common UI views and minimizes
// storage and processing overhead. Only use "lg" if detailed view performance is critical.

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

// ---------------- Notification push configuration -----------------

// NotificationConfig is the root for notification-specific settings.
type NotificationConfig struct {
	Push      PushSettings          `json:"push" yaml:"push"`
	Templates NotificationTemplates `json:"templates" yaml:"templates"`
}

// NotificationTemplates contains customizable notification message templates.
type NotificationTemplates struct {
	NewSpecies NewSpeciesTemplate `json:"newSpecies" yaml:"newspecies"`
}

// NewSpeciesTemplate contains templates for new species detection notifications.
type NewSpeciesTemplate struct {
	Title   string `json:"title" yaml:"title"`
	Message string `json:"message" yaml:"message"`
}

// PushSettings controls global push delivery and provider list.
type PushSettings struct {
	Enabled        bool                 `json:"enabled"`
	DefaultTimeout Duration             `json:"default_timeout" mapstructure:"default_timeout"`
	MaxRetries     int                  `json:"max_retries" mapstructure:"max_retries"`
	RetryDelay     Duration             `json:"retry_delay" mapstructure:"retry_delay"`
	CircuitBreaker CircuitBreakerConfig `json:"circuit_breaker" mapstructure:"circuit_breaker"`
	HealthCheck    HealthCheckConfig    `json:"health_check" mapstructure:"health_check"`
	RateLimiting   RateLimitingConfig   `json:"rate_limiting" mapstructure:"rate_limiting"`
	Providers      []PushProviderConfig `json:"providers"`
	// Detection filtering settings
	MinConfidenceThreshold float64 `json:"minConfidenceThreshold" mapstructure:"min_confidence_threshold"` // 0.0-1.0, 0 = disabled
	SpeciesCooldownMinutes int     `json:"speciesCooldownMinutes" mapstructure:"species_cooldown_minutes"` // 0 = disabled
}

// CircuitBreakerConfig holds circuit breaker configuration.
type CircuitBreakerConfig struct {
	Enabled             bool     `json:"enabled" mapstructure:"enabled"`
	MaxFailures         int      `json:"max_failures" mapstructure:"max_failures"`
	Timeout             Duration `json:"timeout" mapstructure:"timeout"`
	HalfOpenMaxRequests int      `json:"half_open_max_requests" mapstructure:"half_open_max_requests"`
}

// HealthCheckConfig holds health check configuration.
type HealthCheckConfig struct {
	Enabled  bool     `json:"enabled" mapstructure:"enabled"`
	Interval Duration `json:"interval" mapstructure:"interval"`
	Timeout  Duration `json:"timeout" mapstructure:"timeout"`
}

// RateLimitingConfig holds rate limiting configuration.
type RateLimitingConfig struct {
	Enabled           bool `json:"enabled"`
	RequestsPerMinute int  `json:"requests_per_minute" mapstructure:"requests_per_minute"`
	BurstSize         int  `json:"burst_size" mapstructure:"burst_size"`
}

// PushProviderConfig configures a single push provider instance.
type PushProviderConfig struct {
	Type    string           `json:"type"`
	Enabled bool             `json:"enabled"`
	Name    string           `json:"name"`
	Filter  PushFilterConfig `json:"filter"`
	// Shoutrrr-specific
	URLs    []string `json:"urls"`
	Timeout Duration `json:"timeout" mapstructure:"timeout"`
	// Script-specific
	Command     string            `json:"command"`
	Args        []string          `json:"args"`
	Environment map[string]string `json:"environment"`
	InputFormat string            `json:"input_format" mapstructure:"input_format"`
	// Webhook-specific
	Endpoints []WebhookEndpointConfig `json:"endpoints"`
	Template  string                  `json:"template"` // Custom JSON template
}

// WebhookEndpointConfig configures a single webhook endpoint.
type WebhookEndpointConfig struct {
	URL     string            `json:"url"`
	Method  string            `json:"method"`                         // POST, PUT, PATCH (default: POST)
	Headers map[string]string `json:"headers"`                        // Custom HTTP headers
	Timeout Duration          `json:"timeout" mapstructure:"timeout"` // Per-endpoint timeout (default: use provider timeout)
	Auth    WebhookAuthConfig `json:"auth"`                           // Authentication configuration
}

// WebhookAuthConfig configures authentication for webhook requests.
// Supports multiple secret sources for security and flexibility:
//   - Direct values: token: "literal-value" (for development)
//   - Environment variables: token: "${TOKEN}" (recommended for Docker)
//   - File references: token_file: "/run/secrets/token" (for Kubernetes/Swarm)
//
// File fields take precedence over value fields when both are set.
type WebhookAuthConfig struct {
	Type string `json:"type"` // "none", "bearer", "basic", "custom"

	// Bearer authentication
	Token     string `json:"token"`      // Token value or ${ENV_VAR}
	TokenFile string `json:"token_file"` // Path to file containing token

	// Basic authentication
	User     string `json:"user"`      // Username value or ${ENV_VAR}
	UserFile string `json:"user_file"` // Path to file containing username
	Pass     string `json:"pass"`      // Password value or ${ENV_VAR}
	PassFile string `json:"pass_file"` // Path to file containing password

	// Custom header authentication
	Header    string `json:"header"`     // Header name
	Value     string `json:"value"`      // Header value or ${ENV_VAR}
	ValueFile string `json:"value_file"` // Path to file containing header value
}

// PushFilterConfig limits which notifications a provider receives.
type PushFilterConfig struct {
	Types           []string       `json:"types" mapstructure:"types"`
	Priorities      []string       `json:"priorities" mapstructure:"priorities"`
	Components      []string       `json:"components" mapstructure:"components"`
	MetadataFilters map[string]any `json:"metadata_filters" mapstructure:"metadata_filters"`
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

// StreamType constants for supported streaming protocols
const (
	StreamTypeRTSP = "rtsp" // RTSP/RTSPS - IP cameras
	StreamTypeHTTP = "http" // HTTP/HTTPS - Direct streams, Icecast
	StreamTypeHLS  = "hls"  // HLS - .m3u8 playlists
	StreamTypeRTMP = "rtmp" // RTMP/RTMPS - OBS, push streams
	StreamTypeUDP  = "udp"  // UDP/RTP - Low-latency LAN
)

// StreamConfig represents a single audio stream source
type StreamConfig struct {
	Name      string `yaml:"name" json:"name" mapstructure:"name"`                // Required: descriptive name like "Front Yard"
	URL       string `yaml:"url" json:"url" mapstructure:"url"`                   // Required: stream URL
	Type      string `yaml:"type" json:"type" mapstructure:"type"`                // Stream type: rtsp, http, hls, rtmp, udp
	Transport string `yaml:"transport" json:"transport" mapstructure:"transport"` // Transport: tcp or udp (for RTSP/RTMP)
}

// RTSPSettings contains settings for audio streaming (supports multiple protocols).
// Note: Struct name kept for backward compatibility with existing code.
type RTSPSettings struct {
	Streams          []StreamConfig     `yaml:"streams" json:"streams" mapstructure:"streams"`                            // Stream configurations
	URLs             []string           `yaml:"urls,omitempty" json:"urls,omitempty" mapstructure:"urls"`                 // Legacy: accepts old format, migrated on load
	Transport        string             `yaml:"transport,omitempty" json:"transport,omitempty" mapstructure:"transport"`  // Legacy: global default, migrated on load
	Health           RTSPHealthSettings `yaml:"health" json:"health" mapstructure:"health"`                               // Health monitoring settings
	FFmpegParameters []string           `yaml:"ffmpegParameters" json:"ffmpegParameters" mapstructure:"ffmpegParameters"` // Custom FFmpeg parameters
}

// CRITICAL: Legacy fields (URLs, Transport) MUST include json tags to accept
// payloads from older frontends. Without this, saving from a cached old frontend
// would wipe the user's stream configuration. The migration runs on Load().

// MQTTSettings contains settings for MQTT integration.
type MQTTSettings struct {
	Enabled       bool                  `json:"enabled"`                                                         // true to enable MQTT
	Debug         bool                  `json:"debug"`                                                           // true to enable MQTT debug
	Broker        string                `json:"broker"`                                                          // MQTT broker URL
	Topic         string                `json:"topic"`                                                           // MQTT topic
	Username      string                `json:"username"`                                                        // MQTT username
	Password      string                `json:"password"`                                                        // MQTT password
	Retain        bool                  `json:"retain"`                                                          // true to retain messages
	RetrySettings RetrySettings         `json:"retrySettings"`                                                   // settings for retry mechanism
	TLS           MQTTTLSSettings       `json:"tls"`                                                             // TLS/SSL configuration
	HomeAssistant HomeAssistantSettings `yaml:"homeassistant" mapstructure:"homeassistant" json:"homeAssistant"` // Home Assistant auto-discovery settings
}

// MQTTTLSSettings contains TLS/SSL configuration for secure MQTT connections
type MQTTTLSSettings struct {
	Enabled            bool   `json:"enabled"`                                          // true to enable TLS (auto-detected from broker URL)
	InsecureSkipVerify bool   `json:"insecureSkipVerify"`                               // true to skip certificate verification (for self-signed certs)
	CACert             string `yaml:"cacert,omitempty" json:"caCert,omitempty"`         // path to CA certificate file (managed internally)
	ClientCert         string `yaml:"clientcert,omitempty" json:"clientCert,omitempty"` // path to client certificate file (managed internally)
	ClientKey          string `yaml:"clientkey,omitempty" json:"clientKey,omitempty"`   // path to client key file (managed internally)
}

// HomeAssistantSettings contains settings for Home Assistant MQTT auto-discovery.
type HomeAssistantSettings struct {
	Enabled         bool   `yaml:"enabled" mapstructure:"enabled" json:"enabled"`                           // true to enable HA auto-discovery
	DiscoveryPrefix string `yaml:"discovery_prefix" mapstructure:"discovery_prefix" json:"discoveryPrefix"` // HA discovery topic prefix (default: homeassistant)
	DeviceName      string `yaml:"device_name" mapstructure:"device_name" json:"deviceName"`                // base name for devices (default: BirdNET-Go)
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

// FalsePositiveFilterSettings contains settings for false positive filtering aggressivity levels.
// The filtering system requires multiple confirmations of a detection within overlapping analyses
// to filter out false positives (wind, cars, etc.). Higher levels require more confirmations
// but need faster hardware and higher overlap settings.
type FalsePositiveFilterSettings struct {
	Level int `json:"level"` // Filtering aggressivity level (0-5): 0=Off, 1=Lenient, 2=Moderate, 3=Balanced, 4=Strict, 5=Maximum
}

// Validate checks if the filter level is within the valid range (0-5).
func (f *FalsePositiveFilterSettings) Validate() error {
	if f.Level < 0 || f.Level > 5 {
		return fmt.Errorf("invalid false positive filter level %d: must be 0-5 (0=Off, 1=Lenient, 2=Moderate, 3=Balanced, 4=Strict, 5=Maximum)", f.Level)
	}
	return nil
}

// RealtimeSettings contains all settings related to realtime processing.
type RealtimeSettings struct {
	Interval            int                         `json:"interval"`            // minimum interval between log messages in seconds
	ProcessingTime      bool                        `json:"processingTime"`      // true to report processing time for each prediction
	Audio               AudioSettings               `json:"audio"`               // Audio processing settings
	Dashboard           Dashboard                   `json:"dashboard"`           // Dashboard settings
	DynamicThreshold    DynamicThresholdSettings    `json:"dynamicThreshold"`    // Dynamic threshold settings
	FalsePositiveFilter FalsePositiveFilterSettings `json:"falsePositiveFilter"` // False positive filtering aggressivity settings
	Log                 struct {
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
	Threshold float64         `yaml:"threshold" json:"threshold"` // Confidence threshold
	Interval  int             `yaml:"interval" json:"interval"`   // Custom interval in seconds (0 = use default)
	Actions   []SpeciesAction `yaml:"actions" json:"actions"`     // List of actions to execute
}

// SpeciesSettings contains all species-specific settings.
// Note: Config map keys are normalized to lowercase during config load
// and API updates to ensure case-insensitive matching. Users can enter
// species names in any case (e.g., "American Robin", "american robin")
// and they will all resolve to the same lowercase key.
type SpeciesSettings struct {
	Include []string                 `yaml:"include" json:"include"` // Always include these species
	Exclude []string                 `yaml:"exclude" json:"exclude"` // Always exclude these species
	Config  map[string]SpeciesConfig `yaml:"config" json:"config"`   // Per-species configuration (keys normalized to lowercase)
}

// LogDeduplicationSettings contains settings for log deduplication
type LogDeduplicationSettings struct {
	Enabled                    bool `json:"enabled"`                    // true to enable log deduplication
	HealthCheckIntervalSeconds int  `json:"healthCheckIntervalSeconds"` // Health check interval in seconds (default: 60)
}

// SpeciesTrackingSettings contains settings for tracking new species
type SpeciesTrackingSettings struct {
	Enabled                      bool                     `json:"enabled"`                      // true to enable new species tracking
	NewSpeciesWindowDays         int                      `json:"newSpeciesWindowDays"`         // Days to consider a species "new" (default: 14)
	SyncIntervalMinutes          int                      `json:"syncIntervalMinutes"`          // Interval to sync with database (default: 60)
	NotificationSuppressionHours int                      `json:"notificationSuppressionHours"` // Hours to suppress duplicate notifications (default: 168)
	YearlyTracking               YearlyTrackingSettings   `json:"yearlyTracking"`               // Settings for yearly species tracking
	SeasonalTracking             SeasonalTrackingSettings `json:"seasonalTracking"`             // Settings for seasonal species tracking
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
	if latitude > NorthernHemisphereThreshold {
		return "northern"
	} else if latitude < SouthernHemisphereThreshold {
		return "southern"
	}
	return "equatorial"
}

// GetSeasonalTrackingWithHemisphere returns seasonal tracking configuration adjusted for hemisphere
// For southern hemisphere, seasons are shifted by 6 months
//
// This function handles three scenarios:
// 1. No seasons defined (len == 0): Uses defaults for detected hemisphere
// 2. Default seasons from wrong hemisphere: Updates to correct hemisphere
// 3. Custom seasons (non-default names): Preserves user customizations
//
// Issue #1524 fix: Previously only updated when len(Seasons) == 0, which caused
// users with pre-existing Northern hemisphere defaults to keep wrong seasons
// even when their latitude indicated Southern hemisphere.
func GetSeasonalTrackingWithHemisphere(settings SeasonalTrackingSettings, latitude float64) SeasonalTrackingSettings {
	// If no seasons defined, use defaults based on hemisphere
	if len(settings.Seasons) == 0 {
		settings.Seasons = GetDefaultSeasons(latitude)
		return settings
	}

	// Check if current seasons are default seasons (not custom)
	// If they are default seasons, update them to match the user's hemisphere
	if isDefaultSeasonConfiguration(settings.Seasons) {
		settings.Seasons = GetDefaultSeasons(latitude)
	}

	return settings
}

// isDefaultSeasonConfiguration checks if the given seasons map contains
// default season names (spring/summer/fall/winter or wet1/dry1/wet2/dry2).
// This helps distinguish between:
// - Default seasons that should be updated based on hemisphere
// - Custom seasons that should be preserved
//
// Returns true if the seasons appear to be a default configuration,
// false if they appear to be custom user-defined seasons.
func isDefaultSeasonConfiguration(seasons map[string]Season) bool {
	if len(seasons) != 4 {
		return false
	}

	// Check for traditional season names (Northern/Southern hemisphere)
	traditionalSeasons := []string{"spring", "summer", "fall", "winter"}
	hasTraditional := true
	for _, name := range traditionalSeasons {
		if _, exists := seasons[name]; !exists {
			hasTraditional = false
			break
		}
	}
	if hasTraditional {
		return true
	}

	// Check for equatorial season names
	equatorialSeasons := []string{"wet1", "dry1", "wet2", "dry2"}
	hasEquatorial := true
	for _, name := range equatorialSeasons {
		if _, exists := seasons[name]; !exists {
			hasEquatorial = false
			break
		}
	}

	return hasEquatorial
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
		// Validate each season's date
		for name, season := range s.Seasons {
			if err := season.Validate(name); err != nil {
				return err
			}
		}

		// Check that we have a complete set of seasons (either traditional or equatorial)
		traditionalSeasons := []string{"spring", "summer", "fall", "winter"}
		equatorialSeasons := []string{"wet1", "dry1", "wet2", "dry2"}

		hasAllTraditional := true
		hasAllEquatorial := true

		// Check for traditional seasons
		for _, required := range traditionalSeasons {
			if _, exists := s.Seasons[required]; !exists {
				hasAllTraditional = false
				break
			}
		}

		// Check for equatorial seasons
		for _, required := range equatorialSeasons {
			if _, exists := s.Seasons[required]; !exists {
				hasAllEquatorial = false
				break
			}
		}

		// Must have either all traditional or all equatorial seasons
		if !hasAllTraditional && !hasAllEquatorial {
			// Check if we at least have minimum number of seasons
			if len(s.Seasons) < 2 {
				return errors.Newf("at least 2 seasons must be defined, got %d", len(s.Seasons)).
					Component("config").
					Category(errors.CategoryValidation).
					Build()
			}
			// If not a complete set, warn but allow (for custom season configurations)
			// This allows flexibility while ensuring data integrity
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
	Debug       bool                `json:"debug"`                                          // true to enable debug mode
	Sensitivity float64             `json:"sensitivity"`                                    // birdnet analysis sigmoid sensitivity
	Threshold   float64             `json:"threshold"`                                      // threshold for prediction confidence to report
	Overlap     float64             `json:"overlap"`                                        // birdnet analysis overlap between chunks
	Longitude   float64             `json:"longitude"`                                      // longitude of recording location for prediction filtering
	Latitude    float64             `json:"latitude"`                                       // latitude of recording location for prediction filtering
	Threads     int                 `json:"threads"`                                        // number of CPU threads to use for analysis
	Locale      string              `json:"locale"`                                         // language to use for labels
	RangeFilter RangeFilterSettings `json:"rangeFilter"`                                    // range filter settings
	ModelPath   string              `json:"modelPath,omitempty" yaml:"modelPath,omitempty"` // path to external model file (empty for embedded)
	LabelPath   string              `json:"labelPath,omitempty" yaml:"labelPath,omitempty"` // path to external label file (empty for embedded)
	Labels      []string            `yaml:"-" json:"-"`                                     // list of available species labels, runtime value
	UseXNNPACK  bool                `json:"useXnnpack"`                                     // true to use XNNPACK delegate for inference acceleration
}

// RangeFilterSettings contains settings for the range filter
type RangeFilterSettings struct {
	Debug       bool      `json:"debug"`                      // true to enable debug mode
	Model       string    `json:"model"`                      // range filter model version: "legacy" for v1, or empty/default for v2
	ModelPath   string    `json:"modelPath"`                  // path to external meta model file (empty for embedded)
	Threshold   float32   `json:"threshold"`                  // rangefilter species occurrence threshold
	Species     []string  `yaml:"-" json:"species,omitempty"` // list of included species, runtime value
	LastUpdated time.Time `yaml:"-" json:"lastUpdated"`       // last time the species list was updated, runtime value
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

// OAuthProviderConfig holds settings for a single OAuth2 provider in the new array-based format.
// This replaces the individual GoogleAuth, GithubAuth, MicrosoftAuth fields.
type OAuthProviderConfig struct {
	Provider     string `yaml:"provider" json:"provider"`                 // Provider ID: "google", "github", "microsoft"
	Enabled      bool   `yaml:"enabled" json:"enabled"`                   // true to enable this provider
	ClientID     string `yaml:"clientId" json:"clientId"`                 // OAuth2 client ID
	ClientSecret string `yaml:"clientSecret" json:"clientSecret"`         // OAuth2 client secret
	RedirectURI  string `yaml:"redirectUri,omitempty" json:"redirectUri"` // OAuth2 redirect URI (optional, auto-generated if empty)
	UserID       string `yaml:"userId,omitempty" json:"userId"`           // Allowed user ID/email for this provider
}

// SecurityConfig handles all security-related settings and validations
// for the application, including authentication, TLS, and access control.
type Security struct {
	Debug bool `json:"debug"` // true to enable debug mode

	// BaseURL is the complete external URL for this instance, including
	// scheme, host, and optional port (e.g., "https://birdnet.example.com:5500").
	// Used for generating OAuth redirect URLs and notification links.
	// Takes precedence over Host when set.
	// Can be overridden with BIRDNET_URL environment variable.
	// NOTE: This field is prepared for future implementation (issue #1462)
	BaseURL string `json:"baseUrl"`

	// Host is the primary hostname used for TLS certificates,
	// OAuth redirect URLs, and notification link generation.
	// Required when using AutoTLS or authentication providers.
	// Also used to generate URLs in push notifications - set this
	// to your public hostname when using a reverse proxy.
	// Can be overridden with BIRDNET_HOST environment variable.
	Host string `json:"host"`

	// AutoTLS enables automatic TLS certificate management using
	// Let's Encrypt. Requires Host to be set and port 80/443 access.
	AutoTLS bool `json:"autoTls"`

	RedirectToHTTPS   bool              `json:"redirectToHttps"`   // true to redirect to HTTPS
	AllowSubnetBypass AllowSubnetBypass `json:"allowSubnetBypass"` // subnet bypass configuration
	BasicAuth         BasicAuth         `json:"basicAuth"`         // password authentication configuration

	// OAuthProviders is the new array-based OAuth configuration.
	// This is the preferred format for configuring OAuth providers.
	OAuthProviders []OAuthProviderConfig `yaml:"oauthProviders,omitempty" json:"oauthProviders"`

	// Legacy OAuth fields - kept for backwards compatibility.
	// These are migrated to OAuthProviders on startup and ignored thereafter.
	// Will be removed in a future version.
	GoogleAuth    SocialProvider `yaml:"googleAuth,omitempty" json:"googleAuth,omitempty"`       //nolint:modernize // Deprecated: use OAuthProviders, yaml.v3 doesn't support omitzero
	GithubAuth    SocialProvider `yaml:"githubAuth,omitempty" json:"githubAuth,omitempty"`       //nolint:modernize // Deprecated: use OAuthProviders, yaml.v3 doesn't support omitzero
	MicrosoftAuth SocialProvider `yaml:"microsoftAuth,omitempty" json:"microsoftAuth,omitempty"` //nolint:modernize // Deprecated: use OAuthProviders, yaml.v3 doesn't support omitzero

	SessionSecret   string        `json:"sessionSecret"`   // secret for session cookie
	SessionDuration time.Duration `json:"sessionDuration"` // duration for browser session cookies
}

type WebServerSettings struct {
	Debug          bool               `json:"debug"`          // true to enable debug mode
	Enabled        bool               `json:"enabled"`        // true to enable web server
	Port           string             `json:"port"`           // port for web server
	BasePath       string             `json:"basePath"`       // reverse proxy subpath prefix (e.g., "/birdnet")
	LiveStream     LiveStreamSettings `json:"liveStream"`     // live stream configuration
	EnableTerminal bool               `json:"enableTerminal"` // Enable browser terminal (security risk)
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

	// Logging configuration
	Logging logger.LoggingConfig `json:"logging"` // centralized logging configuration

	Main struct {
		Name      string `json:"name"`      // name of BirdNET-Go node, can be used to identify source of notes
		TimeAs24h bool   `json:"timeAs24h"` // true 24-hour time format, false 12-hour time format
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

	Notification NotificationConfig `json:"notification"` // Configuration for push notifications

	Alerting AlertSettings `json:"alerting"` // Alerting rules engine settings
}

// AlertSettings configures the alerting rules engine.
type AlertSettings struct {
	HistoryRetentionDays int `json:"historyRetentionDays" yaml:"history_retention_days" mapstructure:"history_retention_days"` // Days to retain alert history (0 = unlimited)
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
		return nil, errors.New(err).
			Category(errors.CategoryConfiguration).
			Context("operation", "init-viper").
			Build()
	}

	// Unmarshal the config into settings, with custom Duration decode hook
	if err := viper.Unmarshal(settings, viper.DecodeHook(DurationDecodeHook())); err != nil {
		return nil, errors.New(err).
			Category(errors.CategoryConfiguration).
			Context("operation", "unmarshal-config").
			Build()
	}

	// Normalize species config keys to lowercase for case-insensitive matching
	// This ensures that config keys like "American Robin" are converted to "american robin"
	// to match the lowercase species names used in detection lookup (fixes #1701)
	if settings.Realtime.Species.Config != nil {
		settings.Realtime.Species.Config = NormalizeSpeciesConfigKeys(settings.Realtime.Species.Config)
	}

	// Migrate legacy OAuth configuration to new array format
	// This must happen before validation and saving
	if settings.MigrateOAuthConfig() {
		// Save the migrated config back to file
		configFile := viper.ConfigFileUsed()
		if configFile != "" {
			if err := SaveYAMLConfig(configFile, settings); err != nil {
				GetLogger().Warn("Failed to save migrated OAuth config", logger.Error(err))
			} else {
				GetLogger().Info("Saved migrated OAuth configuration", logger.String("path", configFile))
			}
		}
	}

	// Migrate legacy RTSP URLs to new streams format
	if settings.MigrateRTSPConfig() {
		configFile := viper.ConfigFileUsed()
		if configFile != "" {
			if err := SaveYAMLConfig(configFile, settings); err != nil {
				GetLogger().Warn("Failed to save migrated RTSP config", logger.Error(err))
			} else {
				GetLogger().Info("Saved migrated RTSP configuration", logger.String("path", configFile))
			}
		}
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
		GetLogger().Info("Generated new SessionSecret for existing configuration")

		// Save the updated config back to file to persist the generated secret
		// This ensures the secret remains the same across restarts
		configFile := viper.ConfigFileUsed()
		if configFile != "" {
			if err := SaveYAMLConfig(configFile, settings); err != nil {
				// Log the error but don't fail - the generated secret will work for this session
				GetLogger().Warn("Failed to save generated SessionSecret to config file", logger.Error(err))
			} else {
				// Set secure file permissions after saving
				if err := os.Chmod(configFile, 0o600); err != nil {
					GetLogger().Warn("Failed to set secure permissions on config file", logger.Error(err))
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
					GetLogger().Warn("Configuration warning", logger.String("message", errMsg))
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

	// Save settings instance
	settingsInstance = settings
	return settingsInstance, nil
}

// initViper initializes viper with default values and reads the configuration file.
func initViper() error {
	viper.SetConfigName("config")
	viper.SetConfigType("yaml")

	// Configure environment variable support
	if err := configureEnvironmentVariables(); err != nil {
		// Log any validation warnings but don't fail startup
		// This allows the application to continue with config file/default values
		GetLogger().Warn("Environment variable configuration warning", logger.Error(err))
	}

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
	if err := os.MkdirAll(filepath.Dir(configPath), 0o750); err != nil {
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
		GetLogger().Error("Error reading config file", logger.Error(err))
		os.Exit(1)
	}
	return string(data)
}

// GetSettings returns the current settings instance
func GetSettings() *Settings {
	settingsMutex.RLock()
	defer settingsMutex.RUnlock()
	return settingsInstance
}

// migrateLegacyProvider converts a legacy SocialProvider to the new OAuthProviderConfig format.
// Returns nil if the legacy provider is not configured (no ClientID).
func migrateLegacyProvider(providerName string, legacy SocialProvider) *OAuthProviderConfig {
	if legacy.ClientID == "" {
		return nil
	}
	return &OAuthProviderConfig{
		Provider:     providerName,
		Enabled:      legacy.Enabled,
		ClientID:     legacy.ClientID,
		ClientSecret: legacy.ClientSecret,
		RedirectURI:  legacy.RedirectURI,
		UserID:       legacy.UserId,
	}
}

// MigrateOAuthConfig migrates legacy OAuth configuration (GoogleAuth, GithubAuth, MicrosoftAuth)
// to the new OAuthProviders array format. This migration:
// - Skips if OAuthProviders already has entries (already migrated)
// - Only migrates providers that have a ClientID configured
// - Preserves all settings from the legacy format
// - Returns true if migration occurred, false if skipped
func (s *Settings) MigrateOAuthConfig() bool {
	// Skip if already migrated (new array has entries)
	if len(s.Security.OAuthProviders) > 0 {
		return false
	}

	// Define legacy providers to migrate
	legacyProviders := []struct {
		name   string
		config SocialProvider
	}{
		{"google", s.Security.GoogleAuth},
		{"github", s.Security.GithubAuth},
		{"microsoft", s.Security.MicrosoftAuth},
	}

	var migrated bool
	for _, legacy := range legacyProviders {
		if cfg := migrateLegacyProvider(legacy.name, legacy.config); cfg != nil {
			s.Security.OAuthProviders = append(s.Security.OAuthProviders, *cfg)
			migrated = true
			GetLogger().Info("Migrated OAuth configuration to new format", logger.String("provider", legacy.name))
		}
	}

	if migrated {
		GetLogger().Info("OAuth configuration migration complete", logger.String("note", "Legacy fields will be ignored"))
	}

	return migrated
}

// inferStreamType detects the stream type from URL scheme.
// Returns StreamTypeRTSP as default for unknown schemes.
func inferStreamType(url string) string {
	urlLower := strings.ToLower(url)

	switch {
	case strings.HasPrefix(urlLower, "rtsp://"), strings.HasPrefix(urlLower, "rtsps://"):
		return StreamTypeRTSP
	case strings.HasPrefix(urlLower, "rtmp://"), strings.HasPrefix(urlLower, "rtmps://"):
		return StreamTypeRTMP
	case strings.HasPrefix(urlLower, "udp://"), strings.HasPrefix(urlLower, "rtp://"):
		return StreamTypeUDP
	case strings.HasPrefix(urlLower, "http://"), strings.HasPrefix(urlLower, "https://"):
		// Check for HLS (.m3u8) vs generic HTTP
		if strings.Contains(urlLower, ".m3u8") {
			return StreamTypeHLS
		}
		return StreamTypeHTTP
	default:
		return StreamTypeRTSP // Default to RTSP for unknown schemes
	}
}

// MigrateRTSPConfig migrates legacy URLs []string to Streams []StreamConfig.
// This migration:
// - Skips if Streams already has entries (already migrated)
// - Only migrates if URLs has data
// - Trims whitespace and skips empty URLs
// - Infers stream type from URL scheme
// - Preserves the global Transport setting for RTSP/RTMP streams
// - Returns true if migration occurred, false if skipped
func (s *Settings) MigrateRTSPConfig() bool {
	rtsp := &s.Realtime.RTSP

	// Skip if already migrated (new format has streams)
	if len(rtsp.Streams) > 0 {
		return false
	}

	// Skip if no legacy URLs to migrate
	if len(rtsp.URLs) == 0 {
		return false
	}

	// Get global transport, default to tcp
	globalTransport := rtsp.Transport
	if globalTransport == "" {
		globalTransport = "tcp"
	}

	// Preallocate streams slice with capacity and track seen URLs for deduplication
	rtsp.Streams = make([]StreamConfig, 0, len(rtsp.URLs))
	seenURLs := make(map[string]bool)
	streamIndex := 0

	// Migrate each URL to StreamConfig
	for _, rawURL := range rtsp.URLs {
		// Trim whitespace and skip empty URLs
		url := strings.TrimSpace(rawURL)
		if url == "" {
			continue
		}

		// Skip duplicate URLs to ensure valid configuration
		if seenURLs[url] {
			continue
		}
		seenURLs[url] = true
		streamIndex++

		// Infer stream type from URL scheme
		streamType := inferStreamType(url)

		// Only apply transport setting to RTSP/RTMP types where it makes sense
		transport := ""
		if streamType == StreamTypeRTSP || streamType == StreamTypeRTMP {
			transport = globalTransport
		}

		stream := StreamConfig{
			Name:      fmt.Sprintf("Stream %d", streamIndex),
			URL:       url,
			Type:      streamType,
			Transport: transport,
		}
		rtsp.Streams = append(rtsp.Streams, stream)
	}

	// If no valid URLs were found, don't mark as migrated
	if len(rtsp.Streams) == 0 {
		return false
	}

	// Clear legacy fields
	rtsp.URLs = nil
	rtsp.Transport = ""

	GetLogger().Info("Migrated RTSP configuration to new streams format",
		logger.Int("stream_count", len(rtsp.Streams)))

	return true
}

// GetOAuthProvider returns the OAuth provider configuration for the given provider ID.
// Returns nil if the provider is not configured.
func (s *Settings) GetOAuthProvider(providerID string) *OAuthProviderConfig {
	for i := range s.Security.OAuthProviders {
		if s.Security.OAuthProviders[i].Provider == providerID {
			return &s.Security.OAuthProviders[i]
		}
	}
	return nil
}

// IsOAuthProviderEnabled returns true if the specified OAuth provider is enabled.
func (s *Settings) IsOAuthProviderEnabled(providerID string) bool {
	provider := s.GetOAuthProvider(providerID)
	return provider != nil && provider.Enabled && provider.ClientID != "" && provider.ClientSecret != ""
}

// GetEnabledOAuthProviders returns a list of provider IDs that are enabled.
func (s *Settings) GetEnabledOAuthProviders() []string {
	var enabled []string
	for _, p := range s.Security.OAuthProviders {
		if p.Enabled && p.ClientID != "" && p.ClientSecret != "" {
			enabled = append(enabled, p.Provider)
		}
	}
	return enabled
}

// prepareSettingsForSave applies data transformations to settings before saving.
// This function is separated from SaveSettings to enable unit testing without filesystem I/O.
//
// Current transformations:
//   - Auto-populates seasonal tracking seasons based on latitude if not already set
//
// Note: This is a pure function that only transforms data. It does not handle:
//   - Mutex locking (handled by SaveSettings caller)
//   - File I/O operations (handled by SaveSettings)
//   - Species list synchronization (handled separately in SaveSettings)
func prepareSettingsForSave(s *Settings, latitude float64) Settings {
	settingsCopy := *s

	// Auto-update seasonal tracking dates based on latitude if seasonal tracking is enabled
	// and no custom seasons are already defined
	if settingsCopy.Realtime.SpeciesTracking.SeasonalTracking.Enabled &&
		len(settingsCopy.Realtime.SpeciesTracking.SeasonalTracking.Seasons) == 0 {
		// Get hemisphere-appropriate default seasons
		defaultSeasons := GetDefaultSeasons(latitude)
		settingsCopy.Realtime.SpeciesTracking.SeasonalTracking.Seasons = defaultSeasons
	}

	return settingsCopy
}

// SaveSettings saves the current settings to the configuration file.
// It uses UpdateYAMLConfig to handle the atomic write process.
func SaveSettings() error {
	settingsMutex.RLock()
	defer settingsMutex.RUnlock()

	// Create a deep copy of the settings
	settingsCopy := *settingsInstance

	// Create a separate copy of the species list with proper locking
	// Note: This MUST stay here to maintain correct mutex semantics
	speciesListMutex.RLock()
	settingsCopy.BirdNET.RangeFilter.Species = slices.Clone(settingsInstance.BirdNET.RangeFilter.Species)
	speciesListMutex.RUnlock()

	// Apply data transformations (seasonal tracking, etc.)
	settingsCopy = prepareSettingsForSave(&settingsCopy, settingsInstance.BirdNET.Latitude)

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

	GetLogger().Info("Settings saved successfully", logger.String("path", configPath))
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
				GetLogger().Error("Error loading settings", logger.Error(enhancedErr))
				os.Exit(1)
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

// SettingsBuilder provides a fluent interface for constructing test settings.
// It simplifies test setup by providing convenient methods for common configuration patterns.
//
// Example usage:
//
//	settings := conf.NewTestSettings().
//	    WithBirdNET(0.9, 45.0, -122.0).
//	    WithMQTT("tcp://localhost:1883", "test").
//	    Build()
type SettingsBuilder struct {
	settings *Settings
}

// NewTestSettings creates a new SettingsBuilder initialized with default test settings.
func NewTestSettings() *SettingsBuilder {
	return &SettingsBuilder{
		settings: GetTestSettings(),
	}
}

// WithBirdNET configures BirdNET-specific settings.
func (b *SettingsBuilder) WithBirdNET(threshold, latitude, longitude float64) *SettingsBuilder {
	b.settings.BirdNET.Threshold = threshold
	b.settings.BirdNET.Latitude = latitude
	b.settings.BirdNET.Longitude = longitude
	return b
}

// WithMQTT configures MQTT settings and enables MQTT.
func (b *SettingsBuilder) WithMQTT(broker, topic string) *SettingsBuilder {
	b.settings.Realtime.MQTT.Enabled = true
	b.settings.Realtime.MQTT.Broker = broker
	b.settings.Realtime.MQTT.Topic = topic
	return b
}

// WithAudioExport configures audio export settings and enables audio export.
func (b *SettingsBuilder) WithAudioExport(path, exportType, bitrate string) *SettingsBuilder {
	b.settings.Realtime.Audio.Export.Enabled = true
	b.settings.Realtime.Audio.Export.Path = path
	b.settings.Realtime.Audio.Export.Type = exportType
	b.settings.Realtime.Audio.Export.Bitrate = bitrate
	return b
}

// WithSpeciesTracking configures species tracking settings and enables species tracking.
func (b *SettingsBuilder) WithSpeciesTracking(windowDays, syncInterval int) *SettingsBuilder {
	b.settings.Realtime.SpeciesTracking.Enabled = true
	b.settings.Realtime.SpeciesTracking.NewSpeciesWindowDays = windowDays
	b.settings.Realtime.SpeciesTracking.SyncIntervalMinutes = syncInterval
	return b
}

// WithRTSPHealthThreshold configures RTSP health monitoring threshold.
func (b *SettingsBuilder) WithRTSPHealthThreshold(seconds int) *SettingsBuilder {
	b.settings.Realtime.RTSP.Health.HealthyDataThreshold = seconds
	return b
}

// WithImageProvider configures thumbnail image provider settings.
func (b *SettingsBuilder) WithImageProvider(provider, fallbackPolicy string) *SettingsBuilder {
	b.settings.Realtime.Dashboard.Thumbnails.ImageProvider = provider
	b.settings.Realtime.Dashboard.Thumbnails.FallbackPolicy = fallbackPolicy
	return b
}

// WithSecurity configures security settings.
func (b *SettingsBuilder) WithSecurity(host string, autoTLS bool) *SettingsBuilder {
	b.settings.Security.Host = host
	b.settings.Security.AutoTLS = autoTLS
	return b
}

// WithWebServer configures web server settings.
func (b *SettingsBuilder) WithWebServer(port string, enabled bool) *SettingsBuilder {
	b.settings.WebServer.Port = port
	b.settings.WebServer.Enabled = enabled
	return b
}

// Build returns the constructed settings without modifying global state.
// Use this when you need the settings object for manual manipulation.
func (b *SettingsBuilder) Build() *Settings {
	return b.settings
}

// Apply sets the built settings as the global test settings.
// This is equivalent to calling SetTestSettings() with the built settings.
func (b *SettingsBuilder) Apply() *Settings {
	SetTestSettings(b.settings)
	return b.settings
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
			GetLogger().Warn("Failed to remove temporary file", logger.Error(err), logger.String("file", tempFileName))
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
		GetLogger().Error("Failed to generate random secret", logger.Error(enhancedErr))
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
