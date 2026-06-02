// config.go defines the configuration types for the BirdNET-Go application.
package conf

import (
	"crypto/rand"
	"embed"
	"encoding/base64"
	"fmt"
	"iter"
	"time"

	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
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
	Type      string  `yaml:"type" json:"type"` // e.g., "LowPass", "HighPass", "BandPass", etc.
	Frequency float64 `yaml:"frequency" json:"frequency"`
	Q         float64 `yaml:"q" json:"q"`
	Gain      float64 `yaml:"gain" json:"gain"`     // Only used for certain filter types like Peaking
	Width     float64 `yaml:"width" json:"width"`   // Only used for certain filter types like BandPass and BandReject
	Passes    int     `yaml:"passes" json:"passes"` // Filter passes for added attenuation or gain
}

// EqualizerSettings is a struct for audio EQ settings
type EqualizerSettings struct {
	Enabled bool              `yaml:"enabled" json:"enabled"` // global flag to enable/disable equalizer filters
	Filters []EqualizerFilter `yaml:"filters" json:"filters"` // equalizer filter configuration
}

type ExportSettings struct {
	Debug         bool                  `yaml:"debug" json:"debug" mapstructure:"debug"`                         // true to enable audio export debug
	Enabled       bool                  `yaml:"enabled" json:"enabled" mapstructure:"enabled"`                   // export audio clips containing indentified bird calls
	Path          string                `yaml:"path" json:"path" mapstructure:"path"`                            // path to audio clip export directory
	Type          string                `yaml:"type" json:"type" mapstructure:"type"`                            // audio file type, wav, mp3 or flac
	Bitrate       string                `yaml:"bitrate" json:"bitrate" mapstructure:"bitrate"`                   // bitrate for audio export
	Retention     RetentionSettings     `yaml:"retention" json:"retention" mapstructure:"retention"`             // retention settings
	Length        int                   `yaml:"length" json:"length" mapstructure:"length"`                      // audio capture length in seconds
	PreCapture    int                   `yaml:"precapture" json:"preCapture" mapstructure:"preCapture"`          // pre-capture in seconds
	Gain          float64               `yaml:"gain" json:"gain" mapstructure:"gain"`                            // gain in dB for audio capture
	Normalization NormalizationSettings `yaml:"normalization" json:"normalization" mapstructure:"normalization"` // audio normalization settings (EBU R128)
}

// NormalizationSettings contains audio normalization configuration based on EBU R128 standard
type NormalizationSettings struct {
	Enabled       bool    `yaml:"enabled" json:"enabled" mapstructure:"enabled"`                   // true to enable loudness normalization
	TargetLUFS    float64 `yaml:"targetlufs" json:"targetLUFS" mapstructure:"targetLUFS"`          // target integrated loudness in LUFS (default: -23)
	LoudnessRange float64 `yaml:"loudnessrange" json:"loudnessRange" mapstructure:"loudnessRange"` // loudness range in LU (default: 7)
	TruePeak      float64 `yaml:"truepeak" json:"truePeak" mapstructure:"truePeak"`                // true peak limit in dBTP (default: -2)
}

type RetentionSettings struct {
	Debug            bool   `yaml:"debug" json:"debug"`                       // true to enable retention debug
	Policy           string `yaml:"policy" json:"policy"`                     // retention policy, "none", "age" or "usage"
	MaxAge           string `yaml:"maxage" json:"maxAge"`                     // maximum age of audio clips to keep
	MaxUsage         string `yaml:"maxusage" json:"maxUsage"`                 // maximum disk usage percentage before cleanup
	MinClips         int    `yaml:"minclips" json:"minClips"`                 // minimum number of clips per species to keep
	KeepSpectrograms bool   `yaml:"keepspectrograms" json:"keepSpectrograms"` // true to keep spectrograms
	CheckInterval    int    `yaml:"checkinterval" json:"checkInterval"`       // cleanup check interval in minutes (default: 15)
}

// AudioSettings contains settings for audio processing and export.
// SoundLevelSettings contains settings for sound level monitoring
type SoundLevelSettings struct {
	Enabled              bool `yaml:"enabled" mapstructure:"enabled" json:"enabled"`                                            // true to enable sound level monitoring
	Interval             int  `yaml:"interval" mapstructure:"interval" json:"interval"`                                         // measurement interval in seconds (default: 10)
	Debug                bool `yaml:"debug" mapstructure:"debug" json:"debug"`                                                  // true to enable debug logging for sound level monitoring
	DebugRealtimeLogging bool `yaml:"debug_realtime_logging" mapstructure:"debug_realtime_logging" json:"debugRealtimeLogging"` // true to log debug messages for every realtime update, false to log only at configured interval
}

// AudioSourceConfig represents a single audio capture device with per-source settings.
type AudioSourceConfig struct {
	Name       string             `yaml:"name" json:"name" mapstructure:"name"`                                       // Required: descriptive name like "Front Yard Mic"
	Device     string             `yaml:"device" json:"device" mapstructure:"device"`                                 // Required: ALSA device ID (e.g., "sysdefault", "hw:0,0", "Loopback")
	SampleRate int                `yaml:"samplerate,omitempty" json:"sampleRate,omitempty" mapstructure:"samplerate"` // Capture rate in Hz (0 = default 48000; set 256000 for bat detectors)
	Gain       float64            `yaml:"gain" json:"gain" mapstructure:"gain"`                                       // Input gain in dB (0 = no adjustment)
	Model      string             `yaml:"model,omitempty" json:"model,omitempty" mapstructure:"model"`                // AI model: "" or "birdnet" (default), "perch_v2", "bat" (future)
	Models     []string           `yaml:"models,omitempty" json:"models,omitempty" mapstructure:"models"`             // Model IDs for this source (e.g., ["birdnet", "perch_v2"])
	Equalizer  *EqualizerSettings `yaml:"equalizer,omitempty" json:"equalizer,omitempty" mapstructure:"equalizer"`    // Per-source EQ (nil = use global)
	QuietHours QuietHoursConfig   `yaml:"quietHours" json:"quietHours" mapstructure:"quietHours"`                     // Per-source quiet hours
}

type AudioSettings struct {
	Sources         []AudioSourceConfig `yaml:"sources" json:"sources" mapstructure:"sources"`                  // Audio capture devices
	Source          string              `yaml:"source,omitempty" json:"source,omitempty" mapstructure:"source"` // Legacy: migrated to Sources on load
	FfmpegPath      string              `yaml:"ffmpegpath" mapstructure:"ffmpegpath" json:"ffmpegPath"`         // path to ffmpeg, runtime value
	FfmpegVersion   string              `yaml:"-" json:"ffmpegVersion,omitempty"`                               // ffmpeg version string, runtime value
	FfmpegMajor     int                 `yaml:"-" json:"ffmpegMajor,omitempty"`                                 // ffmpeg major version number, runtime value
	FfmpegMinor     int                 `yaml:"-" json:"ffmpegMinor,omitempty"`                                 // ffmpeg minor version number, runtime value
	SoxPath         string              `yaml:"soxpath" mapstructure:"soxpath" json:"soxPath"`                  // path to sox, runtime value
	SoxAudioTypes   []string            `yaml:"-" json:"-"`                                                     // supported audio types of sox, runtime value
	FfprobePath     string              `yaml:"-" json:"-"`                                                     // path to ffprobe, derived from ffmpeg path at runtime
	StreamTransport string              `yaml:"streamtransport" json:"streamTransport"`                         // preferred transport for audio streaming: "auto", "sse", or "ws"
	Export          ExportSettings      `yaml:"export" json:"export"`                                           // export settings
	SoundLevel      SoundLevelSettings  `yaml:"soundlevel" json:"soundLevel"`                                   // sound level monitoring settings

	Equalizer  EqualizerSettings `yaml:"equalizer" json:"equalizer"`                             // equalizer settings (global default)
	QuietHours QuietHoursConfig  `yaml:"quietHours" json:"quietHours" mapstructure:"quietHours"` // quiet hours (global default, legacy)
	Watchdog   WatchdogSettings  `yaml:"watchdog" json:"watchdog" mapstructure:"watchdog"`       // liveness watchdog tuning (0 = use default)
}

// WatchdogSettings holds user-tunable parameters for the audio liveness watchdog.
// All fields are in seconds (except MaxRetries which is a count). Zero values
// mean "use production default", so existing configs work without changes.
type WatchdogSettings struct {
	CheckInterval     int `yaml:"checkInterval" json:"checkInterval" mapstructure:"checkInterval"`             // tick period (default 10s)
	SilenceThreshold  int `yaml:"silenceThreshold" json:"silenceThreshold" mapstructure:"silenceThreshold"`    // silence before alarm (default 30s)
	MaxRetries        int `yaml:"maxRetries" json:"maxRetries" mapstructure:"maxRetries"`                      // restart attempts before escalation (default 3)
	RetryBackoff      int `yaml:"retryBackoff" json:"retryBackoff" mapstructure:"retryBackoff"`                // wait between retries (default 5s)
	Cooldown          int `yaml:"cooldown" json:"cooldown" mapstructure:"cooldown"`                            // alarm suppression after recovery (default 60s)
	EscalationTimeout int `yaml:"escalationTimeout" json:"escalationTimeout" mapstructure:"escalationTimeout"` // time in ESCALATED before FAILED (default 60s)
}

// FindSourceByID returns a pointer to the AudioSourceConfig matching the given
// source ID by Name or Device. Returns nil if no match is found.
func (a *AudioSettings) FindSourceByID(sourceID string) *AudioSourceConfig {
	for i := range a.Sources {
		if a.Sources[i].Name == sourceID || a.Sources[i].Device == sourceID {
			return &a.Sources[i]
		}
	}
	return nil
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
	Debug          bool   `yaml:"debug" json:"debug"`                   // true to enable debug mode
	Summary        bool   `yaml:"summary" json:"summary"`               // show thumbnails on summary table
	Recent         bool   `yaml:"recent" json:"recent"`                 // show thumbnails on recent table
	ImageProvider  string `yaml:"imageprovider" json:"imageProvider"`   // preferred image provider: "auto", "wikimedia", "avicommons"
	FallbackPolicy string `yaml:"fallbackpolicy" json:"fallbackPolicy"` // fallback policy: "none", "all" - try all available providers if preferred fails
}

// Temperature unit constants for display preference
const (
	TemperatureUnitCelsius    = "celsius"
	TemperatureUnitFahrenheit = "fahrenheit"
)

// CustomColors holds the user-defined primary and accent hex colors for the "custom" color scheme.
type CustomColors struct {
	Primary string `yaml:"primary,omitempty" json:"primary,omitempty"` // primary hex color, e.g. "#2563eb"
	Accent  string `yaml:"accent,omitempty" json:"accent,omitempty"`   // accent hex color, e.g. "#0284c7"
}

// Dashboard contains settings for the web dashboard.
type Dashboard struct {
	Thumbnails       Thumbnails           `yaml:"thumbnails" json:"thumbnails"`                         // thumbnails settings
	SummaryLimit     int                  `yaml:"summarylimit" json:"summaryLimit"`                     // limit for the number of species shown in the summary table
	Locale           string               `yaml:"locale,omitempty" json:"locale,omitempty"`             // UI locale setting
	Spectrogram      SpectrogramPreRender `yaml:"spectrogram" json:"spectrogram"`                       // Spectrogram pre-rendering settings
	TemperatureUnit  string               `yaml:"temperatureunit" json:"temperatureUnit"`               // display unit for temperature: "celsius" or "fahrenheit"
	ColorScheme      string               `yaml:"colorscheme,omitempty" json:"colorScheme,omitempty"`   // color scheme: "blue", "forest", "amber", "violet", "rose", "custom"
	CustomColors     *CustomColors        `yaml:"customcolors,omitempty" json:"customColors,omitempty"` // custom scheme colors (used when colorScheme is "custom")
	LogoStyle        string               `yaml:"logostyle,omitempty" json:"logoStyle,omitempty"`       // logo display style: "gradient" or "solid"
	Layout           DashboardLayout      `yaml:"layout" json:"layout"`                                 // configurable dashboard element layout
	DefaultAudioGain float64              `yaml:"defaultaudiogain" json:"defaultAudioGain"`             // Default playback gain in dB (0-24)
	LiveSpectrogram  bool                 `yaml:"livespectrogram" json:"liveSpectrogram"`               // auto-start live spectrogram on dashboard
}

// DashboardLayout defines the ordered list of elements displayed on the dashboard.
// Element order is determined by array index position.
type DashboardLayout struct {
	Elements []DashboardElement `json:"elements" yaml:"elements"`
}

// DashboardElement represents a single configurable element on the dashboard.
// The Type field determines which optional config pointer is used.
// The ID field provides a stable unique identifier for each element instance.
type DashboardElement struct {
	ID      string                `json:"id,omitempty" yaml:"id,omitempty"`           // unique identifier (e.g., "daily-summary-0"); generated by migration if empty
	Type    string                `json:"type" yaml:"type"`                           // "banner", "daily-summary", "currently-hearing", "detections-grid", "live-spectrogram", "video-embed"
	Enabled bool                  `json:"enabled" yaml:"enabled"`                     // whether this element is visible
	Width   string                `json:"width,omitempty" yaml:"width,omitempty"`     // "full" or "half"; defaults to "full" if empty
	Banner  *BannerConfig         `json:"banner,omitempty" yaml:"banner,omitempty"`   // config for banner element
	Video   *VideoEmbedConfig     `json:"video,omitempty" yaml:"video,omitempty"`     // config for video embed element
	Summary *DailySummaryConfig   `json:"summary,omitempty" yaml:"summary,omitempty"` // config for daily summary element
	Grid    *DetectionsGridConfig `json:"grid,omitempty" yaml:"grid,omitempty"`       // config for detections grid element
}

// BannerConfig holds configuration for the dashboard banner element.
type BannerConfig struct {
	ShowImage       bool   `yaml:"showimage" json:"showImage"`                            // whether to show a custom image
	ImagePath       string `yaml:"imagepath" json:"imagePath"`                            // relative path or URL to the banner image
	Title           string `yaml:"title" json:"title"`                                    // station name or custom title
	Description     string `yaml:"description" json:"description"`                        // brief description text
	ShowLocationMap bool   `yaml:"showlocationmap" json:"showLocationMap"`                // whether to show the location map
	ShowWeather     bool   `yaml:"showweather" json:"showWeather"`                        // whether to show weather conditions
	MapZoom         int    `yaml:"mapzoom,omitempty" json:"mapZoom,omitempty"`            // zoom level for the location map (default 11)
	ShowPin         *bool  `yaml:"showpin,omitempty" json:"showPin,omitzero"`             // whether to show the location pin marker (default false)
	MapExpandable   *bool  `yaml:"mapexpandable,omitempty" json:"mapExpandable,omitzero"` // whether visitors can expand the map (default true)
}

// VideoEmbedConfig holds configuration for the YouTube video embed element.
type VideoEmbedConfig struct {
	URL   string `yaml:"url" json:"url"`     // YouTube URL or video ID
	Title string `yaml:"title" json:"title"` // optional display title
}

// DailySummaryConfig holds configuration for the daily summary element.
type DailySummaryConfig struct {
	SummaryLimit int `yaml:"summarylimit" json:"summaryLimit"` // number of species to show
}

// DetectionsGridConfig holds configuration for the detections grid element.
type DetectionsGridConfig struct {
	// Future: card display options
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
	Mode         string `yaml:"mode" json:"mode"                     mapstructure:"mode"`         // Generation mode: "auto" (default), "prerender", "user-requested"
	Enabled      bool   `yaml:"enabled" json:"enabled"               mapstructure:"enabled"`      // DEPRECATED: Use Mode instead. Kept for backward compatibility (true = "prerender", false = "auto")
	Size         string `yaml:"size" json:"size"                     mapstructure:"size"`         // Default size for all modes (see recommendations below)
	Raw          bool   `yaml:"raw" json:"raw"                       mapstructure:"raw"`          // Generate raw spectrogram without axes/legend (default: true)
	Style        string `yaml:"style" json:"style"                   mapstructure:"style"`        // Visual style preset: "default", "scientific_dark", "high_contrast_dark", "scientific"
	DynamicRange string `yaml:"dynamicrange" json:"dynamicRange"     mapstructure:"dynamicRange"` // Dynamic range in dB: "80" (high contrast), "100" (standard), "120" (extended)
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
	Enabled    bool    `yaml:"enabled" json:"enabled"`       // true to enable dynamic threshold
	Debug      bool    `yaml:"debug" json:"debug"`           // true to enable debug mode
	Trigger    float64 `yaml:"trigger" json:"trigger"`       // trigger threshold for dynamic threshold
	Min        float64 `yaml:"min" json:"min"`               // minimum threshold for dynamic threshold
	ValidHours int     `yaml:"validhours" json:"validHours"` // number of hours to consider for dynamic threshold
}

// RetrySettings contains common settings for retry mechanisms
type RetrySettings struct {
	Enabled           bool    `yaml:"enabled" json:"enabled"`                     // true to enable retry mechanism
	MaxRetries        int     `yaml:"maxretries" json:"maxRetries"`               // maximum number of retry attempts
	InitialDelay      int     `yaml:"initialdelay" json:"initialDelay"`           // initial delay before first retry in seconds
	MaxDelay          int     `yaml:"maxdelay" json:"maxDelay"`                   // maximum delay between retries in seconds
	BackoffMultiplier float64 `yaml:"backoffmultiplier" json:"backoffMultiplier"` // multiplier for exponential backoff
}

// BirdweatherSettings contains settings for BirdWeather API integration.
type BirdweatherSettings struct {
	Enabled          bool          `yaml:"enabled" json:"enabled"`                   // true to enable birdweather uploads
	Debug            bool          `yaml:"debug" json:"debug"`                       // true to enable debug mode
	ID               string        `yaml:"id" json:"id"`                             // birdweather ID
	Threshold        float64       `yaml:"threshold" json:"threshold"`               // threshold for prediction confidence for uploads
	LocationAccuracy float64       `yaml:"locationaccuracy" json:"locationAccuracy"` // accuracy of location in meters
	RetrySettings    RetrySettings `yaml:"retrysettings" json:"retrySettings"`       // settings for retry mechanism
}

// EBirdSettings contains settings for eBird API integration.
type EBirdSettings struct {
	Enabled  bool   `yaml:"enabled" json:"enabled"`   // true to enable eBird integration
	APIKey   string `yaml:"apikey" json:"apiKey"`     // eBird API key
	CacheTTL int    `yaml:"cachettl" json:"cacheTTL"` // cache time-to-live in hours (default: 24)
	Locale   string `yaml:"locale" json:"locale"`     // locale for eBird data (e.g., "en", "es")
}

// WeatherSettings contains all weather-related settings
type WeatherSettings struct {
	Provider     string               `yaml:"provider" json:"provider"`         // "none", "yrno", "openweather", or "wunderground"
	PollInterval int                  `yaml:"pollinterval" json:"pollInterval"` // weather data polling interval in minutes
	Debug        bool                 `yaml:"debug" json:"debug"`               // true to enable debug mode
	OpenWeather  OpenWeatherSettings  `yaml:"openweather" json:"openWeather"`   // OpenWeather integration settings
	Wunderground WundergroundSettings `yaml:"wunderground" json:"wunderground"` // WeatherUnderground integration settings
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
	Enabled        bool                 `yaml:"enabled" json:"enabled"`
	DefaultTimeout Duration             `yaml:"default_timeout" json:"default_timeout" mapstructure:"default_timeout"`
	MaxRetries     int                  `yaml:"max_retries" json:"max_retries" mapstructure:"max_retries"`
	RetryDelay     Duration             `yaml:"retry_delay" json:"retry_delay" mapstructure:"retry_delay"`
	CircuitBreaker CircuitBreakerConfig `yaml:"circuit_breaker" json:"circuit_breaker" mapstructure:"circuit_breaker"`
	HealthCheck    HealthCheckConfig    `yaml:"health_check" json:"health_check" mapstructure:"health_check"`
	RateLimiting   RateLimitingConfig   `yaml:"rate_limiting" json:"rate_limiting" mapstructure:"rate_limiting"`
	Providers      []PushProviderConfig `yaml:"providers" json:"providers"`
	// Detection filtering settings
	MinConfidenceThreshold float64 `yaml:"min_confidence_threshold" json:"minConfidenceThreshold" mapstructure:"min_confidence_threshold"` // 0.0-1.0, 0 = disabled
	SpeciesCooldownMinutes int     `yaml:"species_cooldown_minutes" json:"speciesCooldownMinutes" mapstructure:"species_cooldown_minutes"` // 0 = disabled
}

// CircuitBreakerConfig holds circuit breaker configuration.
type CircuitBreakerConfig struct {
	Enabled             bool     `yaml:"enabled" json:"enabled" mapstructure:"enabled"`
	MaxFailures         int      `yaml:"max_failures" json:"max_failures" mapstructure:"max_failures"`
	Timeout             Duration `yaml:"timeout" json:"timeout" mapstructure:"timeout"`
	HalfOpenMaxRequests int      `yaml:"half_open_max_requests" json:"half_open_max_requests" mapstructure:"half_open_max_requests"`
}

// HealthCheckConfig holds health check configuration.
type HealthCheckConfig struct {
	Enabled  bool     `yaml:"enabled" json:"enabled" mapstructure:"enabled"`
	Interval Duration `yaml:"interval" json:"interval" mapstructure:"interval"`
	Timeout  Duration `yaml:"timeout" json:"timeout" mapstructure:"timeout"`
}

// RateLimitingConfig holds rate limiting configuration.
type RateLimitingConfig struct {
	Enabled           bool `yaml:"enabled" json:"enabled"`
	RequestsPerMinute int  `yaml:"requests_per_minute" json:"requests_per_minute" mapstructure:"requests_per_minute"`
	BurstSize         int  `yaml:"burst_size" json:"burst_size" mapstructure:"burst_size"`
}

// PushProviderConfig configures a single push provider instance.
type PushProviderConfig struct {
	Type    string           `yaml:"type" json:"type"`
	Enabled bool             `yaml:"enabled" json:"enabled"`
	Name    string           `yaml:"name" json:"name"`
	Filter  PushFilterConfig `yaml:"filter" json:"filter"`
	// Shoutrrr-specific
	URLs    []string `yaml:"urls" json:"urls"`
	Timeout Duration `yaml:"timeout" json:"timeout" mapstructure:"timeout"`
	// Script-specific
	Command     string            `yaml:"command" json:"command"`
	Args        []string          `yaml:"args" json:"args"`
	Environment map[string]string `yaml:"environment" json:"environment"`
	InputFormat string            `yaml:"input_format" json:"input_format" mapstructure:"input_format"`
	// Webhook-specific
	Endpoints []WebhookEndpointConfig `yaml:"endpoints" json:"endpoints"`
	Template  string                  `yaml:"template" json:"template"` // Custom JSON template
}

// WebhookEndpointConfig configures a single webhook endpoint.
type WebhookEndpointConfig struct {
	URL     string            `yaml:"url" json:"url"`
	Method  string            `yaml:"method" json:"method"`                          // POST, PUT, PATCH (default: POST)
	Headers map[string]string `yaml:"headers" json:"headers"`                        // Custom HTTP headers
	Timeout Duration          `yaml:"timeout" json:"timeout" mapstructure:"timeout"` // Per-endpoint timeout (default: use provider timeout)
	Auth    WebhookAuthConfig `yaml:"auth" json:"auth"`                              // Authentication configuration
}

// WebhookAuthConfig configures authentication for webhook requests.
// Supports multiple secret sources for security and flexibility:
//   - Direct values: token: "literal-value" (for development)
//   - Environment variables: token: "${TOKEN}" (recommended for Docker)
//   - File references: token_file: "/run/secrets/token" (for Kubernetes/Swarm)
//
// File fields take precedence over value fields when both are set.
type WebhookAuthConfig struct {
	Type string `yaml:"type" json:"type"` // "none", "bearer", "basic", "custom"

	// Bearer authentication
	Token     string `yaml:"token" json:"token"`          // Token value or ${ENV_VAR}
	TokenFile string `yaml:"tokenfile" json:"token_file"` // Path to file containing token

	// Basic authentication
	User     string `yaml:"user" json:"user"`          // Username value or ${ENV_VAR}
	UserFile string `yaml:"userfile" json:"user_file"` // Path to file containing username
	Pass     string `yaml:"pass" json:"pass"`          // Password value or ${ENV_VAR}
	PassFile string `yaml:"passfile" json:"pass_file"` // Path to file containing password

	// Custom header authentication
	Header    string `yaml:"header" json:"header"`        // Header name
	Value     string `yaml:"value" json:"value"`          // Header value or ${ENV_VAR}
	ValueFile string `yaml:"valuefile" json:"value_file"` // Path to file containing header value
}

// PushFilterConfig limits which notifications a provider receives.
type PushFilterConfig struct {
	Types           []string       `yaml:"types" json:"types" mapstructure:"types"`
	Priorities      []string       `yaml:"priorities" json:"priorities" mapstructure:"priorities"`
	Components      []string       `yaml:"components" json:"components" mapstructure:"components"`
	MetadataFilters map[string]any `yaml:"metadata_filters" json:"metadata_filters" mapstructure:"metadata_filters"`
}

// WundergroundSettings contains settings for WeatherUnderground integration.
type WundergroundSettings struct {
	APIKey    string `yaml:"apikey" json:"apiKey"`       // WeatherUnderground API key
	StationID string `yaml:"stationid" json:"stationId"` // WeatherUnderground station ID
	Endpoint  string `yaml:"endpoint" json:"endpoint"`   // WeatherUnderground API endpoint
	Units     string `yaml:"units" json:"units"`         // units of measurement: "e" (imperial), "m" (metric), "h" (UK hybrid)
}

// OpenWeatherSettings contains settings for OpenWeather integration.
type OpenWeatherSettings struct {
	Enabled  bool   `yaml:"enabled" json:"enabled"`   // true to enable OpenWeather integration, for legacy support
	APIKey   string `yaml:"apikey" json:"apiKey"`     // OpenWeather API key
	Endpoint string `yaml:"endpoint" json:"endpoint"` // OpenWeather API endpoint
	Units    string `yaml:"units" json:"units"`       // units of measurement: standard, metric, or imperial
	Language string `yaml:"language" json:"language"` // language code for the response
}

// PrivacyFilterSettings contains settings for the privacy filter.
type PrivacyFilterSettings struct {
	Debug      bool    `yaml:"debug" json:"debug"`           // true to enable debug mode
	Enabled    bool    `yaml:"enabled" json:"enabled"`       // true to enable privacy filter
	Confidence float32 `yaml:"confidence" json:"confidence"` // confidence threshold for human detection
}

// DogBarkFilterSettings contains settings for the dog bark filter.
type DogBarkFilterSettings struct {
	Debug      bool     `yaml:"debug" json:"debug"`           // true to enable debug mode
	Enabled    bool     `yaml:"enabled" json:"enabled"`       // true to enable dog bark filter
	Confidence float32  `yaml:"confidence" json:"confidence"` // confidence threshold for dog bark detection
	Remember   int      `yaml:"remember" json:"remember"`     // how long we should remember bark for filtering?
	Species    []string `yaml:"species" json:"species"`       // species list for filtering
}

// DaylightFilterSettings contains settings for the daylight species filter.
// It discards detections of configured species (default: nocturnal birds) during daylight hours.
type DaylightFilterSettings struct {
	Debug   bool     `yaml:"debug" json:"debug"`     // true to enable debug logging
	Enabled bool     `yaml:"enabled" json:"enabled"` // true to enable daylight filter
	Offset  int      `yaml:"offset" json:"offset"`   // hours to adjust daylight window; positive = shrink (lenient), negative = expand (strict)
	Species []string `yaml:"species" json:"species"` // species, families, orders, or genera to filter during daylight
}

// RTSPHealthSettings contains settings for RTSP stream health monitoring.
type RTSPHealthSettings struct {
	HealthyDataThreshold int `yaml:"healthydatathreshold" json:"healthyDataThreshold"` // seconds before stream considered unhealthy (default: 60)
	MonitoringInterval   int `yaml:"monitoringinterval" json:"monitoringInterval"`     // health check interval in seconds (default: 30)
}

// QuietHoursConfig defines a time window during which an audio source is stopped
// to reduce CPU usage when birds are unlikely to be active.
// Mode can be "fixed" (clock times) or "solar" (relative to sunrise/sunset).
type QuietHoursConfig struct {
	Enabled     bool   `yaml:"enabled" json:"enabled" mapstructure:"enabled"`             // true to enable quiet hours
	Mode        string `yaml:"mode" json:"mode" mapstructure:"mode"`                      // "fixed" or "solar"
	StartTime   string `yaml:"startTime" json:"startTime" mapstructure:"startTime"`       // "HH:MM" format for fixed mode (e.g., "22:00")
	EndTime     string `yaml:"endTime" json:"endTime" mapstructure:"endTime"`             // "HH:MM" format for fixed mode (e.g., "06:00")
	StartEvent  string `yaml:"startEvent" json:"startEvent" mapstructure:"startEvent"`    // "sunset" or "sunrise" for solar mode
	StartOffset int    `yaml:"startOffset" json:"startOffset" mapstructure:"startOffset"` // minutes relative to start event (positive = after, negative = before)
	EndEvent    string `yaml:"endEvent" json:"endEvent" mapstructure:"endEvent"`          // "sunset" or "sunrise" for solar mode
	EndOffset   int    `yaml:"endOffset" json:"endOffset" mapstructure:"endOffset"`       // minutes relative to end event (positive = after, negative = before)
}

// StreamType constants for supported streaming protocols
const (
	StreamTypeRTSP = "rtsp" // RTSP/RTSPS - IP cameras
	StreamTypeHTTP = "http" // HTTP/HTTPS - Direct streams, Icecast
	StreamTypeHLS  = "hls"  // HLS - .m3u8 playlists
	StreamTypeRTMP = "rtmp" // RTMP/RTMPS - OBS, push streams
	StreamTypeUDP  = "udp"  // UDP/RTP - Low-latency LAN
)

// DefaultTransport is the default RTSP/RTMP transport protocol
const DefaultTransport = "tcp"

// ChannelMode controls how multi-channel audio is handled before analysis.
type ChannelMode string

const (
	ChannelModeDownmix ChannelMode = "downmix" // Mix all channels to mono (default)
	ChannelModeLeft    ChannelMode = "left"    // Use left (first) channel only
	ChannelModeRight   ChannelMode = "right"   // Use right (second) channel only
)

// DefaultChannelMode is used when no channel mode is specified.
const DefaultChannelMode = ChannelModeDownmix

// Canonical returns the effective channel mode, treating an empty value as the
// default (downmix). An unset mode and an explicit "downmix" produce identical
// FFmpeg arguments, so callers comparing modes (e.g. hot-reload change detection)
// should compare canonical values to avoid treating that no-op transition as a
// real change that would needlessly restart the stream.
func (m ChannelMode) Canonical() ChannelMode {
	if m == "" {
		return DefaultChannelMode
	}
	return m
}

// ValidChannelModes is the set of accepted channel mode values.
var ValidChannelModes = map[ChannelMode]bool{
	ChannelModeDownmix: true,
	ChannelModeLeft:    true,
	ChannelModeRight:   true,
}

// StreamConfig represents a single audio stream source
type StreamConfig struct {
	Name        string             `yaml:"name" json:"name" mapstructure:"name"`                                    // Required: descriptive name like "Front Yard"
	URL         string             `yaml:"url" json:"url" mapstructure:"url"`                                       // Required: stream URL
	Enabled     bool               `yaml:"enabled" json:"enabled" mapstructure:"enabled"`                           // true when the configured stream should be active
	Type        string             `yaml:"type" json:"type" mapstructure:"type"`                                    // Stream type: rtsp, http, hls, rtmp, udp
	Transport   string             `yaml:"transport" json:"transport" mapstructure:"transport"`                     // Transport: tcp or udp (for RTSP/RTMP)
	ChannelMode ChannelMode        `yaml:"channelMode,omitempty" json:"channelMode" mapstructure:"channelMode"`     // Channel handling: downmix, left, or right
	Equalizer   *EqualizerSettings `yaml:"equalizer,omitempty" json:"equalizer,omitempty" mapstructure:"equalizer"` // Per-stream EQ (nil = use global)
	QuietHours  QuietHoursConfig   `yaml:"quietHours" json:"quietHours" mapstructure:"quietHours"`                  // Quiet hours configuration
	Models      []string           `yaml:"models,omitempty" json:"models,omitempty" mapstructure:"models"`          // Model IDs for this stream (e.g., ["birdnet", "perch_v2"])
}

// IsEnabled returns the effective enabled state for a stream.
func (s *StreamConfig) IsEnabled() bool {
	return s.Enabled
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

// AllStreams returns an iterator over all configured streams as addressable pointers,
// regardless of enabled state. Use this when every stream must be visited (e.g.
// migrations, validation, applying defaults). Mutations via the pointer are reflected
// in the original slice.
func (r *RTSPSettings) AllStreams() iter.Seq2[int, *StreamConfig] {
	return func(yield func(int, *StreamConfig) bool) {
		for i := range r.Streams {
			if !yield(i, &r.Streams[i]) {
				return
			}
		}
	}
}

// EnabledStreams returns an iterator over streams that are both enabled and have a
// non-empty URL. Call sites that process active streams should use this instead of
// ranging over Streams directly and checking IsEnabled(). Mutations via the pointer
// are reflected in the original slice.
func (r *RTSPSettings) EnabledStreams() iter.Seq2[int, *StreamConfig] {
	return func(yield func(int, *StreamConfig) bool) {
		for i := range r.Streams {
			s := &r.Streams[i]
			if s.URL == "" || !s.IsEnabled() {
				continue
			}
			if !yield(i, s) {
				return
			}
		}
	}
}

// FindStreamByName returns a pointer to the StreamConfig matching the given
// name. Returns nil if no match is found.
func (r *RTSPSettings) FindStreamByName(name string) *StreamConfig {
	for i := range r.Streams {
		if r.Streams[i].Name == name {
			return &r.Streams[i]
		}
	}
	return nil
}

// CRITICAL: Legacy fields (URLs, Transport) MUST include json tags to accept
// payloads from older frontends. Without this, saving from a cached old frontend
// would wipe the user's stream configuration. The migration runs on Load().

// MQTTSettings contains settings for MQTT integration.
type MQTTSettings struct {
	Enabled       bool                  `yaml:"enabled" json:"enabled"`                                          // true to enable MQTT
	Debug         bool                  `yaml:"debug" json:"debug"`                                              // true to enable MQTT debug
	Broker        string                `yaml:"broker" json:"broker"`                                            // MQTT broker URL
	Topic         string                `yaml:"topic" json:"topic"`                                              // MQTT topic
	Username      string                `yaml:"username" json:"username"`                                        // MQTT username
	Password      string                `yaml:"password" json:"password"`                                        // MQTT password
	Retain        bool                  `yaml:"retain" json:"retain"`                                            // true to retain messages
	RetrySettings RetrySettings         `yaml:"retrysettings" json:"retrySettings"`                              // settings for retry mechanism
	TLS           MQTTTLSSettings       `yaml:"tls" json:"tls"`                                                  // TLS/SSL configuration
	HomeAssistant HomeAssistantSettings `yaml:"homeassistant" mapstructure:"homeassistant" json:"homeAssistant"` // Home Assistant auto-discovery settings
}

// MQTTTLSSettings contains TLS/SSL configuration for secure MQTT connections
type MQTTTLSSettings struct {
	Enabled            bool   `yaml:"enabled" json:"enabled"`                           // true to enable TLS (auto-detected from broker URL)
	InsecureSkipVerify bool   `yaml:"insecureskipverify" json:"insecureSkipVerify"`     // true to skip certificate verification (for self-signed certs)
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
	Enabled bool   `yaml:"enabled" json:"enabled"` // true to enable Prometheus compatible telemetry endpoint
	Listen  string `yaml:"listen" json:"listen"`   // IP address and port to listen on
}

// MonitoringSettings controls system resource metric collection.
// Thresholds and notifications are managed by the alerting engine rules.
type MonitoringSettings struct {
	Enabled       bool              `yaml:"enabled" json:"enabled"`             // true to enable system resource monitoring
	CheckInterval int               `yaml:"checkinterval" json:"checkInterval"` // interval in seconds between resource checks
	CPU           ResourceEnabled   `yaml:"cpu" json:"cpu"`                     // CPU metric collection
	Memory        ResourceEnabled   `yaml:"memory" json:"memory"`               // Memory metric collection
	Disk          DiskMonitorConfig `yaml:"disk" json:"disk"`                   // Disk metric collection
}

// ResourceEnabled controls whether a resource type is monitored.
type ResourceEnabled struct {
	Enabled bool `yaml:"enabled" json:"enabled"` // true to enable monitoring for this resource
}

// DiskMonitorConfig controls disk monitoring and which paths to check.
type DiskMonitorConfig struct {
	Enabled bool     `yaml:"enabled" json:"enabled"` // true to enable disk monitoring
	Paths   []string `yaml:"paths" json:"paths"`     // filesystem paths to monitor
}

// SentrySettings contains settings for Sentry error tracking
type SentrySettings struct {
	Enabled bool `yaml:"enabled" json:"enabled"` // true to enable Sentry error tracking (opt-in)
	Debug   bool `yaml:"debug" json:"debug"`     // true to enable transparent telemetry logging
}

// FalsePositiveFilterSettings contains settings for false positive filtering aggressivity levels.
// The filtering system requires multiple confirmations of a detection within overlapping analyses
// to filter out false positives (wind, cars, etc.). Higher levels require more confirmations
// but need faster hardware and higher overlap settings.
type FalsePositiveFilterSettings struct {
	Level int `yaml:"level" json:"level"` // Filtering aggressivity level (0-5): 0=Off, 1=Lenient, 2=Moderate, 3=Balanced, 4=Strict, 5=Maximum
}

// Validate checks if the filter level is within the valid range (0-5).
func (f *FalsePositiveFilterSettings) Validate() error {
	if f.Level < 0 || f.Level > 5 {
		return fmt.Errorf("invalid false positive filter level %d: must be 0-5 (0=Off, 1=Lenient, 2=Moderate, 3=Balanced, 4=Strict, 5=Maximum)", f.Level)
	}
	return nil
}

// ExtendedCaptureSettings contains settings for extended capture mode.
// Extended capture produces a single audio clip for long continuous calling sessions.
type ExtendedCaptureSettings struct {
	Enabled              bool     `yaml:"enabled" json:"enabled" mapstructure:"enabled"`
	MaxDuration          int      `yaml:"maxduration" json:"maxDuration" mapstructure:"maxDuration"`
	CaptureBufferSeconds int      `yaml:"capturebufferseconds" json:"captureBufferSeconds" mapstructure:"captureBufferSeconds"`
	Species              []string `yaml:"species" json:"species" mapstructure:"species"`
}

// EffectiveCaptureBufferSeconds returns the capture buffer duration to use for
// buffer allocation. Unlike Validate, this is a pure read-only method that does
// not mutate settings. Returns DefaultCaptureBufferSeconds when extended capture
// is disabled or settings are invalid.
func (e *ExtendedCaptureSettings) EffectiveCaptureBufferSeconds(preCapture int) int {
	if !e.Enabled {
		return DefaultCaptureBufferSeconds
	}

	maxDuration := e.MaxDuration
	if maxDuration == 0 {
		maxDuration = DefaultExtendedCaptureMaxDuration
	}

	if maxDuration < 0 || maxDuration > MaxExtendedCaptureDuration {
		return DefaultCaptureBufferSeconds
	}

	bufferSeconds := e.CaptureBufferSeconds
	if bufferSeconds == 0 {
		bufferSeconds = maxDuration + preCapture + ExtendedCaptureBufferMargin
	}

	minBuffer := maxDuration + preCapture + ExtendedCaptureMinBufferMargin
	if bufferSeconds < minBuffer {
		return DefaultCaptureBufferSeconds
	}

	return bufferSeconds
}

// Validate checks ExtendedCaptureSettings for consistency.
func (e *ExtendedCaptureSettings) Validate(preCapture int) error {
	if !e.Enabled {
		return nil
	}

	if e.MaxDuration == 0 {
		e.MaxDuration = DefaultExtendedCaptureMaxDuration
	}

	if e.MaxDuration < 0 {
		return fmt.Errorf("maxDuration must be non-negative, got %d", e.MaxDuration)
	}

	if e.CaptureBufferSeconds < 0 {
		return fmt.Errorf("captureBufferSeconds must be non-negative, got %d", e.CaptureBufferSeconds)
	}

	if e.MaxDuration > MaxExtendedCaptureDuration {
		return fmt.Errorf("maxDuration %d exceeds maximum of %d (1200 seconds / 20 minutes)", e.MaxDuration, MaxExtendedCaptureDuration)
	}

	if e.CaptureBufferSeconds == 0 {
		e.CaptureBufferSeconds = e.MaxDuration + preCapture + ExtendedCaptureBufferMargin
	}

	minBuffer := e.MaxDuration + preCapture + ExtendedCaptureMinBufferMargin
	if e.CaptureBufferSeconds < minBuffer {
		return fmt.Errorf("capture buffer %ds too small: must be >= %d (maxDuration %d + preCapture %d + %d margin)",
			e.CaptureBufferSeconds, minBuffer, e.MaxDuration, preCapture, ExtendedCaptureMinBufferMargin)
	}

	return nil
}

// RealtimeSettings contains all settings related to realtime processing.
type RealtimeSettings struct {
	Interval            int                         `yaml:"interval" json:"interval"`                       // minimum interval between log messages in seconds
	ProcessingTime      bool                        `yaml:"processingtime" json:"processingTime"`           // true to report processing time for each prediction
	Audio               AudioSettings               `yaml:"audio" json:"audio"`                             // Audio processing settings
	Dashboard           Dashboard                   `yaml:"dashboard" json:"dashboard"`                     // Dashboard settings
	DynamicThreshold    DynamicThresholdSettings    `yaml:"dynamicthreshold" json:"dynamicThreshold"`       // Dynamic threshold settings
	FalsePositiveFilter FalsePositiveFilterSettings `yaml:"falsepositivefilter" json:"falsePositiveFilter"` // False positive filtering aggressivity settings
	Log                 struct {
		Enabled bool   `yaml:"enabled" json:"enabled"` // true to enable OBS chat log
		Path    string `yaml:"path" json:"path"`       // path to OBS chat log
	} `yaml:"log" json:"log"`
	LogDeduplication LogDeduplicationSettings `yaml:"logdeduplication" json:"logDeduplication"` // Log deduplication settings
	Birdweather      BirdweatherSettings      `yaml:"birdweather" json:"birdweather"`           // Birdweather integration settings
	EBird            EBirdSettings            `yaml:"ebird" json:"ebird"`                       // eBird integration settings
	OpenWeather      OpenWeatherSettings      `yaml:"-" json:"-"`                               // OpenWeather integration settings
	PrivacyFilter    PrivacyFilterSettings    `yaml:"privacyfilter" json:"privacyFilter"`       // Privacy filter settings
	DogBarkFilter    DogBarkFilterSettings    `yaml:"dogbarkfilter" json:"dogBarkFilter"`       // Dog bark filter settings
	DaylightFilter   DaylightFilterSettings   `yaml:"daylightfilter" json:"daylightFilter"`     // Daylight filter settings
	RTSP             RTSPSettings             `yaml:"rtsp" json:"rtsp"`                         // RTSP settings
	MQTT             MQTTSettings             `yaml:"mqtt" json:"mqtt"`                         // MQTT settings
	Telemetry        TelemetrySettings        `yaml:"telemetry" json:"telemetry"`               // Telemetry settings
	Monitoring       MonitoringSettings       `yaml:"monitoring" json:"monitoring"`             // System resource monitoring settings
	Species          SpeciesSettings          `yaml:"species" json:"species"`                   // Custom thresholds and actions for species
	Weather          WeatherSettings          `yaml:"weather" json:"weather"`                   // Weather provider related settings
	SpeciesTracking  SpeciesTrackingSettings  `yaml:"speciestracking" json:"speciesTracking"`   // New species tracking settings
	ExtendedCapture  ExtendedCaptureSettings  `yaml:"extendedcapture" json:"extendedCapture"`   // Extended capture for long calling species
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
	Enabled                    bool `yaml:"enabled" json:"enabled"`                                       // true to enable log deduplication
	HealthCheckIntervalSeconds int  `yaml:"healthcheckintervalseconds" json:"healthCheckIntervalSeconds"` // Health check interval in seconds (default: 60)
}

// SpeciesTrackingSettings contains settings for tracking new species
type SpeciesTrackingSettings struct {
	Enabled                      bool                     `yaml:"enabled" json:"enabled"`                                           // true to enable new species tracking
	NewSpeciesWindowDays         int                      `yaml:"newspecieswindowdays" json:"newSpeciesWindowDays"`                 // Days to consider a species "new" (default: 14)
	SyncIntervalMinutes          int                      `yaml:"syncintervalminutes" json:"syncIntervalMinutes"`                   // Interval to sync with database (default: 60)
	NotificationSuppressionHours int                      `yaml:"notificationsuppressionhours" json:"notificationSuppressionHours"` // Hours to suppress duplicate notifications (default: 168)
	YearlyTracking               YearlyTrackingSettings   `yaml:"yearlytracking" json:"yearlyTracking"`                             // Settings for yearly species tracking
	SeasonalTracking             SeasonalTrackingSettings `yaml:"seasonaltracking" json:"seasonalTracking"`                         // Settings for seasonal species tracking
}

// YearlyTrackingSettings contains settings for tracking first arrivals each year
type YearlyTrackingSettings struct {
	Enabled    bool `yaml:"enabled" json:"enabled"`       // true to enable yearly tracking
	ResetMonth int  `yaml:"resetmonth" json:"resetMonth"` // Month to reset yearly tracking (1=January, default: 1)
	ResetDay   int  `yaml:"resetday" json:"resetDay"`     // Day to reset yearly tracking (default: 1)
	WindowDays int  `yaml:"windowdays" json:"windowDays"` // Days to show "new this year" indicator (default: 30)
}

// SeasonalTrackingSettings contains settings for tracking first arrivals each season
type SeasonalTrackingSettings struct {
	Enabled    bool              `yaml:"enabled" json:"enabled"`       // true to enable seasonal tracking
	WindowDays int               `yaml:"windowdays" json:"windowDays"` // Days to show "new this season" indicator (default: 21)
	Seasons    map[string]Season `yaml:"seasons" json:"seasons"`       // Season definitions
}

// Season defines the start date for a season
type Season struct {
	StartMonth int `yaml:"startmonth" json:"startMonth"` // Month when season starts (1-12)
	StartDay   int `yaml:"startday" json:"startDay"`     // Day when season starts (1-31)
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

// isDefaultSeasonConfiguration checks if the given seasons map exactly matches
// one of the known default season configurations (NH, SH, or equatorial).
// Both season names AND start dates must match for the configuration to be
// considered default. This prevents overwriting user-customized dates when
// the user keeps the standard season names but changes the start dates.
func isDefaultSeasonConfiguration(seasons map[string]Season) bool {
	if len(seasons) != 4 {
		return false
	}

	// Check traditional season names with Northern Hemisphere dates
	if matchesSeasonSet(seasons, map[string]Season{
		"spring": {StartMonth: 3, StartDay: 20},
		"summer": {StartMonth: 6, StartDay: 21},
		"fall":   {StartMonth: 9, StartDay: 22},
		"winter": {StartMonth: 12, StartDay: 21},
	}) {
		return true
	}

	// Check traditional season names with Southern Hemisphere dates
	if matchesSeasonSet(seasons, map[string]Season{
		"spring": {StartMonth: 9, StartDay: 22},
		"summer": {StartMonth: 12, StartDay: 21},
		"fall":   {StartMonth: 3, StartDay: 20},
		"winter": {StartMonth: 6, StartDay: 21},
	}) {
		return true
	}

	// Check equatorial season names and dates
	return matchesSeasonSet(seasons, map[string]Season{
		"wet1": {StartMonth: 3, StartDay: 1},
		"dry1": {StartMonth: 6, StartDay: 1},
		"wet2": {StartMonth: 9, StartDay: 1},
		"dry2": {StartMonth: 12, StartDay: 1},
	})
}

// matchesSeasonSet returns true if seasons contains exactly the same entries
// (name, StartMonth, StartDay) as expected.
func matchesSeasonSet(seasons, expected map[string]Season) bool {
	for name, exp := range expected {
		got, ok := seasons[name]
		if !ok || got.StartMonth != exp.StartMonth || got.StartDay != exp.StartDay {
			return false
		}
	}
	return true
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
	Type       string   `yaml:"type" json:"type"`             // Type of the action (e.g. ExecuteScript which is only type for now)
	Parameters []string `yaml:"parameters" json:"parameters"` // List of parameters for the action
}

// InputConfig holds settings for file or directory analysis
type InputConfig struct {
	Path      string `yaml:"-" json:"-"` // path to input file or directory
	Recursive bool   `yaml:"-" json:"-"` // true for recursive directory analysis
	Watch     bool   `yaml:"-" json:"-"` // true to watch directory for new files
}

type BirdNETConfig struct {
	Version            string              `yaml:"version,omitempty" json:"version,omitempty"`                 // model version: "2.4", "3.0"
	Debug              bool                `yaml:"debug" json:"debug"`                                         // true to enable debug mode
	Sensitivity        float64             `yaml:"sensitivity" json:"sensitivity"`                             // birdnet analysis sigmoid sensitivity
	Threshold          float64             `yaml:"threshold" json:"threshold"`                                 // threshold for prediction confidence to report
	Overlap            float64             `yaml:"overlap" json:"overlap"`                                     // birdnet analysis overlap between chunks
	Longitude          float64             `yaml:"longitude" json:"longitude"`                                 // longitude of recording location for prediction filtering
	Latitude           float64             `yaml:"latitude" json:"latitude"`                                   // latitude of recording location for prediction filtering
	LocationConfigured bool                `yaml:"locationconfigured" json:"locationConfigured"`               // true when location has been explicitly configured by the user
	Threads            int                 `yaml:"threads" json:"threads"`                                     // number of CPU threads to use for analysis
	Locale             string              `yaml:"locale" json:"locale"`                                       // language to use for labels
	RangeFilter        RangeFilterSettings `yaml:"rangefilter" json:"rangeFilter"`                             // range filter settings
	ModelPath          string              `yaml:"modelpath,omitempty" json:"modelPath,omitempty"`             // path to external model file (empty for embedded)
	LabelPath          string              `yaml:"labelpath,omitempty" json:"labelPath,omitempty"`             // path to external label file (empty for embedded)
	Labels             []string            `yaml:"-" json:"-"`                                                 // list of available species labels, runtime value
	UseXNNPACK         bool                `yaml:"usexnnpack" json:"useXnnpack"`                               // true to use XNNPACK delegate for inference acceleration
	ONNXRuntimePath    string              `yaml:"onnxruntimepath,omitempty" json:"onnxRuntimePath,omitempty"` // path to ONNX Runtime shared library (required for ONNX models)
}

// RangeFilterSettings contains settings for the range filter
type RangeFilterSettings struct {
	Debug                   bool                `yaml:"debug" json:"debug"`                               // true to enable debug mode
	Model                   string              `yaml:"model" json:"model"`                               // range filter model version: "legacy" for v1, "v3" for geomodel v3.0, or empty/default for v2
	ModelPath               string              `yaml:"modelpath" json:"modelPath"`                       // path to external meta model file (empty for embedded)
	LabelsPath              string              `yaml:"labelspath,omitempty" json:"labelsPath,omitempty"` // path to geomodel labels file (required when geomodel differs from classifier labels)
	Threshold               float32             `yaml:"threshold" json:"threshold"`                       // rangefilter species occurrence threshold
	PassUnmappedSpecies     bool                `yaml:"passunmappedspecies" json:"passUnmappedSpecies"`   // true to pass through species absent from geomodel (score 1.0); false to filter them out (score 0.0)
	Species                 []string            `yaml:"-" json:"species,omitempty"`                       // list of included species, runtime value
	IncludedScientificNames map[string]struct{} `yaml:"-" json:"-"`                                       // O(1) lookup set of included scientific names (lowercase), runtime value
	LastUpdated             time.Time           `yaml:"-" json:"lastUpdated"`                             // last time the species list was updated, runtime value
}

// PerchConfig holds configuration for the Google Perch v2 model.
type PerchConfig struct {
	ModelPath string  `yaml:"modelpath,omitempty" json:"modelPath,omitempty"` // path to Perch v2 ONNX model file
	LabelPath string  `yaml:"labelpath,omitempty" json:"labelPath,omitempty"` // path to Perch v2 label CSV file
	Threshold float64 `yaml:"threshold" json:"threshold"`                     // confidence threshold for detections
	Locale    string  `yaml:"locale,omitempty" json:"locale,omitempty"`       // locale for species label translation
}

// BatConfig holds configuration for bat detection using BirdNET v2.4 embeddings.
type BatConfig struct {
	EmbeddingModel      string                      `yaml:"embeddingmodel,omitempty" json:"embeddingModel,omitempty"`   // path to BirdNET v2.4 embeddings ONNX model
	ClassifierModel     string                      `yaml:"classifiermodel,omitempty" json:"classifierModel,omitempty"` // path to bat species classifier ONNX model
	LabelPath           string                      `yaml:"labelpath,omitempty" json:"labelPath,omitempty"`             // path to bat species labels file
	Threshold           float64                     `yaml:"threshold" json:"threshold"`                                 // confidence threshold for bat detections
	Locale              string                      `yaml:"locale,omitempty" json:"locale,omitempty"`                   // locale for species label translation
	NighttimeOnly       bool                        `yaml:"nighttimeonly" json:"nighttimeOnly"`                         // restrict bat detection to nighttime (civil dusk to civil dawn)
	FalsePositiveFilter FalsePositiveFilterSettings `yaml:"falsepositivefilter" json:"falsePositiveFilter"`             // false positive filtering for bat detections (level 0-5)
	UltrasonicFilter    UltrasonicFilterConfig      `yaml:"ultrasonicfilter" json:"ultrasonicFilter"`                   // post-detection ultrasonic validation filter
}

// UltrasonicFilterConfig controls the post-detection ultrasonic validation filter for bat detections.
// The filter measures temporal variability of ultrasonic energy (US frame CV) in the source audio.
// Real bat echolocation produces bursts of ultrasonic energy (high CV), while false positives
// from audible-range sounds show flat ultrasonic energy at the noise floor (low CV).
type UltrasonicFilterConfig struct {
	Enabled          bool    `yaml:"enabled" json:"enabled"`                   // enable ultrasonic validation filter
	CVThreshold      float64 `yaml:"cvthreshold" json:"cvThreshold"`           // detections with US frame CV below this are tagged unlikely
	FFTSize          int     `yaml:"fftsize" json:"fftSize"`                   // FFT window size in samples (must be power of 2)
	HopSize          int     `yaml:"hopsize" json:"hopSize"`                   // STFT hop size in samples
	FrequencySplitHz int     `yaml:"frequencysplithz" json:"frequencySplitHz"` // boundary between audible and ultrasonic bands in Hz
}

// BSGConfig holds configuration for BSG regional bird models.
type BSGConfig struct {
	ModelPath string `yaml:"modelpath,omitempty" json:"modelPath,omitempty"` // path to BSG ONNX model file
	LabelPath string `yaml:"labelpath,omitempty" json:"labelPath,omitempty"` // path to BSG label file
	Locale    string `yaml:"locale,omitempty" json:"locale,omitempty"`       // locale for species label translation
}

// ModelsConfig holds global model enablement and management settings.
type ModelsConfig struct {
	Enabled   []string `yaml:"enabled" json:"enabled"`                         // list of model IDs to load (e.g., "birdnet", "perch_v2")
	Directory string   `yaml:"directory,omitempty" json:"directory,omitempty"` // base directory for downloaded model files
	Installed []string `yaml:"installed,omitempty" json:"installed,omitempty"` // list of installed model IDs managed by the model gallery
}

// BasicAuth holds settings for the password authentication
type BasicAuth struct {
	Enabled        bool          `yaml:"enabled" json:"enabled"`               // true to enable password authentication
	Password       string        `yaml:"password" json:"password"`             // password for admin interface
	ClientID       string        `yaml:"clientid" json:"clientId"`             // client id for OAuth2
	ClientSecret   string        `yaml:"clientsecret" json:"clientSecret"`     // client secret for OAuth2
	RedirectURI    string        `yaml:"redirecturi" json:"redirectUri"`       // redirect uri for OAuth2
	AuthCodeExp    time.Duration `yaml:"authcodeexp" json:"authCodeExp"`       // duration for authorization code
	AccessTokenExp time.Duration `yaml:"accesstokenexp" json:"accessTokenExp"` // duration for access token
}

// SocialProvider holds settings for an OAuth2 identity provider
type SocialProvider struct {
	Enabled      bool   `yaml:"enabled" json:"enabled"`           // true to enable social provider
	ClientID     string `yaml:"clientid" json:"clientId"`         // client id for OAuth2
	ClientSecret string `yaml:"clientsecret" json:"clientSecret"` // client secret for OAuth2
	RedirectURI  string `yaml:"redirecturi" json:"redirectUri"`   // redirect uri for OAuth2
	UserId       string `yaml:"userid" json:"userId"`             // valid user id for OAuth2
}

type AllowSubnetBypass struct {
	Enabled bool   `yaml:"enabled" json:"enabled"` // true to enable subnet bypass
	Subnet  string `yaml:"subnet" json:"subnet"`   // disable OAuth2 in subnet
}

// PublicAccess defines which features are accessible without authentication.
// Each field corresponds to a feature that can be individually made public.
type PublicAccess struct {
	LiveAudio bool `yaml:"liveaudio" json:"liveAudio"` // allow unauthenticated users to start/listen to live audio streams
}

// OAuthProviderConfig holds settings for a single OAuth2 provider in the new array-based format.
// This replaces the individual GoogleAuth, GithubAuth, MicrosoftAuth fields.
type OAuthProviderConfig struct {
	Provider     string   `yaml:"provider" json:"provider"`                 // Provider ID: "google", "github", "microsoft"
	Enabled      bool     `yaml:"enabled" json:"enabled"`                   // true to enable this provider
	ClientID     string   `yaml:"clientId" json:"clientId"`                 // OAuth2 client ID
	ClientSecret string   `yaml:"clientSecret" json:"clientSecret"`         // OAuth2 client secret
	RedirectURI  string   `yaml:"redirectUri,omitempty" json:"redirectUri"` // OAuth2 redirect URI (optional, auto-generated if empty)
	UserID       string   `yaml:"userId,omitempty" json:"userId"`           // Allowed user ID/email for this provider
	IssuerURL    string   `yaml:"issuerUrl,omitempty" json:"issuerUrl"`     // OIDC issuer/discovery URL (required when provider is "oidc")
	Scopes       []string `yaml:"scopes,omitempty" json:"scopes,omitempty"` // Custom OAuth scopes (OIDC default: openid, profile, email)
}

// TLSMode represents the TLS certificate management mode.
type TLSMode string

const (
	// TLSModeNone disables TLS certificate management.
	TLSModeNone TLSMode = ""
	// TLSModeAutoTLS enables automatic TLS via Let's Encrypt.
	TLSModeAutoTLS TLSMode = "autotls"
	// TLSModeManual uses user-provided TLS certificates.
	TLSModeManual TLSMode = "manual"
	// TLSModeSelfSigned generates self-signed TLS certificates.
	TLSModeSelfSigned TLSMode = "selfsigned"
)

// SecurityConfig handles all security-related settings and validations
// for the application, including authentication, TLS, and access control.
type Security struct {
	Debug bool `yaml:"debug" json:"debug"` // true to enable debug mode

	// BaseURL is the complete external URL for this instance, including
	// scheme, host, and optional port (e.g., "https://birdnet.example.com:5500").
	// Used for generating OAuth redirect URLs and notification links.
	// Takes precedence over Host when set.
	// Can be overridden with BIRDNET_URL environment variable.
	// NOTE: This field is prepared for future implementation (issue #1462)
	BaseURL string `yaml:"baseurl" json:"baseUrl"`

	// Host is the primary hostname used for TLS certificates,
	// OAuth redirect URLs, and notification link generation.
	// Required when using AutoTLS or authentication providers.
	// Also used to generate URLs in push notifications - set this
	// to your public hostname when using a reverse proxy.
	// Can be overridden with BIRDNET_HOST environment variable.
	Host string `yaml:"host" json:"host"`

	// Deprecated: AutoTLS is replaced by TLSMode. Kept for backward-compatible
	// config migration. Will be removed in a future version.
	AutoTLS bool `yaml:"autoTls,omitempty" json:"autoTls,omitempty"` //nolint:modernize // Deprecated: use TLSMode

	// TLSMode controls TLS certificate management. Valid values:
	//   "" (none)      - TLS disabled
	//   "autotls"      - automatic via Let's Encrypt
	//   "manual"       - user-provided certificates
	//   "selfsigned"   - auto-generated self-signed certificates
	TLSMode TLSMode `yaml:"tlsMode" json:"tlsMode"`

	// SelfSignedValidity is the validity duration for self-signed certificates.
	// Uses Go duration format with day/month suffixes (e.g., "365d", "1y").
	SelfSignedValidity string `yaml:"selfSignedValidity" json:"selfSignedValidity"`

	TLSPort           string            `yaml:"tlsport" json:"tlsPort"`                     // port for HTTPS (default: 8443)
	RedirectToHTTPS   bool              `yaml:"redirecttohttps" json:"redirectToHttps"`     // true to redirect to HTTPS
	AllowSubnetBypass AllowSubnetBypass `yaml:"allowsubnetbypass" json:"allowSubnetBypass"` // subnet bypass configuration
	PublicAccess      PublicAccess      `yaml:"publicaccess" json:"publicAccess"`           // features accessible without authentication
	BasicAuth         BasicAuth         `yaml:"basicauth" json:"basicAuth"`                 // password authentication configuration

	// OAuthProviders is the new array-based OAuth configuration.
	// This is the preferred format for configuring OAuth providers.
	OAuthProviders []OAuthProviderConfig `yaml:"oauthProviders,omitempty" json:"oauthProviders"`

	// Legacy OAuth fields - kept for backwards compatibility.
	// These are migrated to OAuthProviders on startup and ignored thereafter.
	// Will be removed in a future version.
	GoogleAuth    SocialProvider `yaml:"googleAuth,omitempty" json:"googleAuth,omitempty"`       //nolint:modernize // Deprecated: use OAuthProviders, yaml.v3 doesn't support omitzero
	GithubAuth    SocialProvider `yaml:"githubAuth,omitempty" json:"githubAuth,omitempty"`       //nolint:modernize // Deprecated: use OAuthProviders, yaml.v3 doesn't support omitzero
	MicrosoftAuth SocialProvider `yaml:"microsoftAuth,omitempty" json:"microsoftAuth,omitempty"` //nolint:modernize // Deprecated: use OAuthProviders, yaml.v3 doesn't support omitzero

	SessionSecret   string        `yaml:"sessionsecret" json:"sessionSecret"`     // secret for session cookie
	SessionDuration time.Duration `yaml:"sessionduration" json:"sessionDuration"` // duration for browser session cookies
}

type WebServerSettings struct {
	Debug          bool               `yaml:"debug" json:"debug"`                   // true to enable debug mode
	Enabled        bool               `yaml:"enabled" json:"enabled"`               // true to enable web server
	Port           string             `yaml:"port" json:"port"`                     // port for web server
	BasePath       string             `yaml:"basepath" json:"basePath"`             // reverse proxy subpath prefix (e.g., "/birdnet")
	AllowEmbedding bool               `yaml:"allowembedding" json:"allowEmbedding"` // true to allow embedding in iframes (e.g., Home Assistant)
	LiveStream     LiveStreamSettings `yaml:"livestream" json:"liveStream"`         // live stream configuration
	EnableTerminal bool               `yaml:"enableterminal" json:"enableTerminal"` // Enable browser terminal (security risk)
}

type LiveStreamSettings struct {
	Debug          bool   `yaml:"debug" json:"debug"`                   // true to enable debug mode
	BitRate        int    `yaml:"bitrate" json:"bitRate"`               // bitrate for live stream in kbps
	SampleRate     int    `yaml:"samplerate" json:"sampleRate"`         // sample rate for live stream in Hz
	SegmentLength  int    `yaml:"segmentlength" json:"segmentLength"`   // length of each segment in seconds
	FfmpegLogLevel string `yaml:"ffmpegloglevel" json:"ffmpegLogLevel"` // log level for ffmpeg
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
	} `yaml:"operationtimeouts" json:"operationTimeouts"`
}

// Settings contains all configuration options for the BirdNET-Go application.
type Settings struct {
	Debug bool `yaml:"debug" json:"debug"` // true to enable debug mode

	// Runtime values, not stored in config file
	Version            string   `yaml:"-" json:"version,omitempty"`            // Version from build
	BuildDate          string   `yaml:"-" json:"buildDate,omitempty"`          // Build date from build
	SystemID           string   `yaml:"-" json:"systemId,omitempty"`           // Unique system identifier for telemetry
	ValidationWarnings []string `yaml:"-" json:"validationWarnings,omitempty"` // Configuration validation warnings for telemetry

	// Logging configuration
	Logging logger.LoggingConfig `yaml:"logging" json:"logging" mapstructure:"logging"` // centralized logging configuration

	Main struct {
		Name      string `yaml:"name" json:"name"`           // name of BirdNET-Go node, can be used to identify source of notes
		TimeAs24h bool   `yaml:"timeas24h" json:"timeAs24h"` // true 24-hour time format, false 12-hour time format
	} `yaml:"main" json:"main"`

	BirdNET BirdNETConfig `yaml:"birdnet" json:"birdnet"` // BirdNET configuration
	Perch   PerchConfig   `yaml:"perch" json:"perch"`     // Perch v2 model configuration
	Bat     BatConfig     `yaml:"bat" json:"bat"`         // Bat detection configuration
	BSG     BSGConfig     `yaml:"bsg" json:"bsg"`         // BSG regional bird model configuration
	Models  ModelsConfig  `yaml:"models" json:"models"`   // Global model enablement and management

	TaxonomySynonyms map[string]string `yaml:"taxonomySynonyms" json:"taxonomySynonyms" mapstructure:"taxonomySynonyms"` // Optional scientific-name synonym overrides merged with built-ins

	Input InputConfig `yaml:"-" json:"-"` // Input configuration for file and directory analysis

	Realtime  RealtimeSettings  `yaml:"realtime" json:"realtime"`   // Realtime processing settings
	WebServer WebServerSettings `yaml:"webserver" json:"webServer"` // web server configuration
	Security  Security          `yaml:"security" json:"security"`   // security configuration
	Sentry    SentrySettings    `yaml:"sentry" json:"sentry"`       // Sentry error tracking configuration

	Output struct {
		File struct {
			Enabled bool   `yaml:"-" json:"-"` // true to enable file output
			Path    string `yaml:"-" json:"-"` // directory to output results
			Type    string `yaml:"-" json:"-"` // table, csv
		} `yaml:"file" json:"file"`

		SQLite struct {
			Enabled bool   `yaml:"enabled" json:"enabled"` // true to enable sqlite output
			Path    string `yaml:"path" json:"path"`       // path to sqlite database
		} `yaml:"sqlite" json:"sqlite"`

		MySQL struct {
			Enabled  bool   `yaml:"enabled" json:"enabled"`   // true to enable mysql output
			Username string `yaml:"username" json:"username"` // username for mysql database
			Password string `yaml:"password" json:"password"` // password for mysql database
			Database string `yaml:"database" json:"database"` // database name for mysql database
			Host     string `yaml:"host" json:"host"`         // host for mysql database
			Port     string `yaml:"port" json:"port"`         // port for mysql database
		} `yaml:"mysql" json:"mysql"`
	} `yaml:"output" json:"output"`

	Backup BackupConfig `yaml:"backup" json:"backup"` // Backup configuration

	Notification NotificationConfig `yaml:"notification" json:"notification"` // Configuration for push notifications

	Alerting AlertSettings `yaml:"alerting" json:"alerting"` // Alerting rules engine settings
}

// ResolveEQOverride returns the per-source or per-stream EQ override for the
// given source display name. Returns nil when no override exists (use global).
// Audio sources are checked first, then RTSP streams.
func (s *Settings) ResolveEQOverride(displayName string) *EqualizerSettings {
	for i := range s.Realtime.Audio.Sources {
		if s.Realtime.Audio.Sources[i].Name == displayName {
			return s.Realtime.Audio.Sources[i].Equalizer
		}
	}
	for i := range s.Realtime.RTSP.Streams {
		if s.Realtime.RTSP.Streams[i].Name == displayName {
			return s.Realtime.RTSP.Streams[i].Equalizer
		}
	}
	return nil
}

// AlertSettings configures the alerting rules engine.
type AlertSettings struct {
	HistoryRetentionDays int `json:"historyRetentionDays" yaml:"history_retention_days" mapstructure:"history_retention_days"` // Days to retain alert history (0 = unlimited)
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
	for i := range s.Security.OAuthProviders {
		p := &s.Security.OAuthProviders[i]
		if p.Enabled && p.ClientID != "" && p.ClientSecret != "" {
			enabled = append(enabled, p.Provider)
		}
	}
	return enabled
}

// GenerateRandomSecret generates a URL-safe base64 encoded random string
// suitable for use as a client secret. The output is 43 characters long,
// providing 256 bits of entropy. Returns an error if the system's
// cryptographic random number generator fails.
func GenerateRandomSecret() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("failed to generate random secret: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(bytes), nil
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
	return nil
}
