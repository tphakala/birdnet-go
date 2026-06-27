package api

import (
	"time"

	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
)

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

// Settings section name constants
const (
	SettingsSectionBirdnet   = "birdnet"
	SettingsSectionWebserver = "webserver"
	SettingsSectionRealtime  = "realtime"
	SettingsSectionAudio     = "audio"
	SettingsSectionSpecies   = "species"
)

// SSE connection status constants are defined in apicore and re-exported via
// apicore_bridge.go (SSEStatusConnected / SSEStatusDisconnected).

// Toast notification type constants
const (
	ToastTypeSuccess = "success"
	ToastTypeError   = "error"
	ToastTypeWarning = "warning"
	ToastTypeInfo    = "info"
)

// Query parameter value constants
const (
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
	// PercentageMultiplier is used for converting fractions to percentages.
	// Aliased from apicore so the detections/search confidence parsing (now in
	// apicore) and the analytics percentage parsing here share one source.
	PercentageMultiplier = apicore.PercentageMultiplier

	// SecondsPerMinute is the number of seconds in a minute
	SecondsPerMinute = 60
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
)

// Buffer and channel size constants
const (
	// DefaultChannelBufferSize is the default buffer size for channels
	DefaultChannelBufferSize = 256

	// DefaultReadBufferSize is the default buffer size for reading (1KB)
	DefaultReadBufferSize = 1024
)
