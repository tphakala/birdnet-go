package api

import "time"

// Cache duration constants for HTTP responses
const (
	// ImageCacheDuration is the cache duration for species images (30 days)
	// These are external images from wikimedia/flickr that are stable
	ImageCacheDuration = 30 * 24 * time.Hour

	// NotFoundCacheDuration is the cache duration for 404 responses (24 hours)
	// This prevents repeated lookups for missing resources
	NotFoundCacheDuration = 24 * time.Hour

	// SpectrogramCacheDuration is the cache duration for spectrograms (30 days)
	// Once generated, spectrograms don't change
	SpectrogramCacheDuration = 30 * 24 * time.Hour
)

// Cache duration in seconds for HTTP headers
const (
	// ImageCacheSeconds is the cache duration for species images in seconds
	ImageCacheSeconds = 2592000 // 30 days

	// NotFoundCacheSeconds is the cache duration for 404 responses in seconds
	NotFoundCacheSeconds = 86400 // 24 hours

	// SpectrogramCacheSeconds is the cache duration for spectrograms in seconds
	SpectrogramCacheSeconds = 2592000 // 30 days
)

// Log level constants
const (
	LogLevelError   = "error"
	LogLevelWarning = "warning"
	LogLevelInfo    = "info"
)

// Notification type constants (for mapping to notification.Type)
const (
	NotificationTypeError     = "error"
	NotificationTypeWarning   = "warning"
	NotificationTypeInfo      = "info"
	NotificationTypeDetection = "detection"
	NotificationTypeSystem    = "system"
)

// Weather provider constants
const (
	WeatherProviderOpenWeather  = "openweather"
	WeatherProviderWunderground = "wunderground"
	WeatherProviderYrno         = "yrno"
	WeatherUnitMetric           = "metric"
)

// Verification status constants for detections
const (
	VerificationStatusCorrect       = "correct"
	VerificationStatusFalsePositive = "false_positive"
	VerificationStatusUnverified    = "unverified"
)

// Settings section name constants
const (
	SettingsSectionBirdnet   = "birdnet"
	SettingsSectionWebserver = "webserver"
	SettingsSectionRealtime  = "realtime"
	SettingsSectionAudio     = "audio"
	SettingsSectionSpecies   = "species"
)

// SSE connection status constants
const (
	SSEStatusConnected    = "connected"
	SSEStatusDisconnected = "disconnected"
)

// Toast notification type constants
const (
	ToastTypeSuccess = "success"
	ToastTypeError   = "error"
	ToastTypeWarning = "warning"
	ToastTypeInfo    = "info"
)

// Query parameter value constants
const (
	QueryValueAny  = "any"
	QueryValueTrue = "true"
)

// Operating system constants
const (
	OSLinux   = "linux"
	OSWindows = "windows"
	OSDarwin  = "darwin"
)

// Generic fallback value constants
const (
	ValueUnknown = "unknown"
)

// Numeric constants for calculations
const (
	// PercentageMultiplier is used for converting fractions to percentages
	PercentageMultiplier = 100.0

	// HoursPerDay is the number of hours in a day
	HoursPerDay = 24

	// SecondsPerMinute is the number of seconds in a minute
	SecondsPerMinute = 60

	// SecondsPerHour is the number of seconds in an hour
	SecondsPerHour = 3600

	// MillisecondsPerSecond converts seconds to milliseconds
	MillisecondsPerSecond = 1000
)

// Timeout and duration constants
const (
	// DefaultTimeoutSeconds is the default timeout for operations (5 seconds)
	DefaultTimeoutSeconds = 5

	// DefaultCacheDurationMinutes is the default cache duration (30 minutes)
	DefaultCacheDurationMinutes = 30
)

// File permission constants
const (
	// FilePermReadWrite is the permission for read/write files (0o644)
	FilePermReadWrite = 0o644

	// FilePermExecutable is the permission for executable files (0o755)
	FilePermExecutable = 0o755

	// FilePermOwnerOnly is the permission for owner-only read/write (0o600)
	FilePermOwnerOnly = 0o600
)

// Buffer and channel size constants
const (
	// DefaultChannelBufferSize is the default buffer size for channels
	DefaultChannelBufferSize = 256

	// DefaultReadBufferSize is the default buffer size for reading (1KB)
	DefaultReadBufferSize = 1024
)