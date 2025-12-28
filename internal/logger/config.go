package logger

// LoggingConfig represents logging configuration
type LoggingConfig struct {
	Level         string                  `yaml:"level" json:"level"`                   // debug, info, warn, error (deprecated, use DefaultLevel)
	Timezone      string                  `yaml:"timezone" json:"timezone"`             // "Local", "UTC", or IANA timezone name like "Europe/Helsinki"
	File          string                  `yaml:"file" json:"file"`                     // optional log file path (deprecated, use FileOutput)
	DebugWebhooks bool                    `yaml:"debug_webhooks" json:"debug_webhooks"` // if true, logs full webhook details (headers, body, etc.)
	DefaultLevel  string                  `yaml:"default_level" json:"default_level"`   // default log level for all modules
	Console       *ConsoleOutput          `yaml:"console" json:"console"`               // console output configuration
	FileOutput    *FileOutput             `yaml:"file_output" json:"file_output"`       // file output configuration
	ModuleOutputs map[string]ModuleOutput `yaml:"modules" json:"modules"`               // per-module output configuration
	ModuleLevels  map[string]string       `yaml:"module_levels" json:"module_levels"`   // per-module log levels
}

// ConsoleOutput represents console logging configuration.
// Console output uses human-readable text format without timestamps.
// Timestamps are omitted following the Twelve-Factor App methodology;
// the execution environment (journald, Docker) adds them automatically.
type ConsoleOutput struct {
	Enabled bool   `yaml:"enabled" json:"enabled"` // enable console output
	Level   string `yaml:"level" json:"level"`     // log level for console output
}

// FileOutput represents file logging configuration.
// File output uses JSON format with RFC3339 timestamps for machine parsing
// and log aggregation systems (ELK, Loki, Splunk, etc.).
type FileOutput struct {
	Enabled    bool   `yaml:"enabled" json:"enabled"`         // enable file output
	Path       string `yaml:"path" json:"path"`               // log file path
	MaxSize    int    `yaml:"max_size" json:"max_size"`       // maximum size in MB before rotation
	MaxAge     int    `yaml:"max_age" json:"max_age"`         // maximum age in days to keep old logs
	MaxBackups int    `yaml:"max_backups" json:"max_backups"` // maximum number of old log files to keep
	Compress   bool   `yaml:"compress" json:"compress"`       // compress rotated logs
	Level      string `yaml:"level" json:"level"`             // log level for file output
}

// ModuleOutput represents per-module output configuration
type ModuleOutput struct {
	Enabled     bool   `yaml:"enabled" json:"enabled"`           // enable module-specific output
	FilePath    string `yaml:"file_path" json:"file_path"`       // dedicated file path for this module
	Level       string `yaml:"level" json:"level"`               // log level override for this module
	ConsoleAlso bool   `yaml:"console_also" json:"console_also"` // also log to console
	MaxSize     int    `yaml:"max_size" json:"max_size"`         // maximum size in MB before rotation (0 = use FileOutput default)
	MaxAge      int    `yaml:"max_age" json:"max_age"`           // maximum age in days (0 = use FileOutput default)
	MaxBackups  int    `yaml:"max_backups" json:"max_backups"`   // maximum number of old files (0 = use FileOutput default)
	Compress    bool   `yaml:"compress" json:"compress"`         // compress rotated logs
}

// Default values for logging configuration.
// These match the defaults in conf/defaults.go to ensure consistency.
const (
	DefaultLogLevel           = "info"
	DefaultLogPath            = "logs/application.log"
	DefaultAccessLogPath      = "logs/access.log"
	DefaultAuthLogPath        = "logs/auth.log"
	DefaultAudioLogPath       = "logs/audio.log"
	DefaultBirdweatherLogPath   = "logs/birdweather.log"
	DefaultWeatherLogPath       = "logs/weather.log"
	DefaultImageproviderLogPath = "logs/imageprovider.log"
	DefaultMaxSize            = 100 // MB
	DefaultMaxAge             = 30  // days
	DefaultMaxBackups         = 10
	DefaultCompressLogs       = true
	DefaultConsoleEnabled     = true
	DefaultFileEnabled        = true
)

// applyConfigDefaults applies sensible defaults for nil configuration sections.
// This ensures backwards compatibility when users upgrade - their existing configs
// without explicit file_output or console sections will get file and console logging
// enabled by default rather than silently disabled.
func applyConfigDefaults(cfg *LoggingConfig) {
	if cfg == nil {
		return
	}

	// Apply default level if not set
	if cfg.DefaultLevel == "" {
		cfg.DefaultLevel = DefaultLogLevel
	}

	// Apply console defaults if nil
	// Console should be enabled by default for user visibility
	if cfg.Console == nil {
		cfg.Console = &ConsoleOutput{
			Enabled: DefaultConsoleEnabled,
			Level:   DefaultLogLevel,
		}
	}

	// Apply file output defaults if nil
	// File logging should be enabled by default for troubleshooting and log aggregation
	if cfg.FileOutput == nil {
		cfg.FileOutput = &FileOutput{
			Enabled:    DefaultFileEnabled,
			Path:       DefaultLogPath,
			Level:      DefaultLogLevel,
			MaxSize:    DefaultMaxSize,
			MaxAge:     DefaultMaxAge,
			MaxBackups: DefaultMaxBackups,
			Compress:   DefaultCompressLogs,
		}
	}

	// Apply default module outputs for critical modules
	// HTTP access logs should always go to a separate file for security auditing,
	// log rotation, and easier analysis (separate from application logs)
	if cfg.ModuleOutputs == nil {
		cfg.ModuleOutputs = make(map[string]ModuleOutput)
	}

	// Add access log module if not explicitly configured
	// This ensures HTTP request/response logs are always separated from application logs
	if _, hasAccess := cfg.ModuleOutputs["access"]; !hasAccess {
		cfg.ModuleOutputs["access"] = ModuleOutput{
			Enabled:     true,
			FilePath:    DefaultAccessLogPath,
			Level:       DefaultLogLevel,
			ConsoleAlso: false, // Access logs are typically high volume, don't spam console
		}
	}

	// Consolidate API logs with access logs - they're both HTTP request logs
	if _, hasAPI := cfg.ModuleOutputs["api"]; !hasAPI {
		cfg.ModuleOutputs["api"] = ModuleOutput{
			Enabled:     true,
			FilePath:    DefaultAccessLogPath, // Same file as access logs
			Level:       DefaultLogLevel,
			ConsoleAlso: false,
		}
	}

	// Add security/auth log module if not explicitly configured
	// Authentication and authorization logs need separate file for security auditing
	if _, hasSecurity := cfg.ModuleOutputs["security"]; !hasSecurity {
		cfg.ModuleOutputs["security"] = ModuleOutput{
			Enabled:     true,
			FilePath:    DefaultAuthLogPath,
			Level:       DefaultLogLevel,
			ConsoleAlso: false,
		}
	}

	// Also route auth module to auth.log
	if _, hasAuth := cfg.ModuleOutputs["auth"]; !hasAuth {
		cfg.ModuleOutputs["auth"] = ModuleOutput{
			Enabled:     true,
			FilePath:    DefaultAuthLogPath,
			Level:       DefaultLogLevel,
			ConsoleAlso: false,
		}
	}

	// Add audio log module if not explicitly configured
	// Audio capture/processing logs are high volume during operation
	if _, hasAudio := cfg.ModuleOutputs["audio"]; !hasAudio {
		cfg.ModuleOutputs["audio"] = ModuleOutput{
			Enabled:     true,
			FilePath:    DefaultAudioLogPath,
			Level:       DefaultLogLevel,
			ConsoleAlso: false,
		}
	}

	// Route audio.ffmpeg module to audio.log as well
	if _, hasFFmpeg := cfg.ModuleOutputs["audio.ffmpeg"]; !hasFFmpeg {
		cfg.ModuleOutputs["audio.ffmpeg"] = ModuleOutput{
			Enabled:     true,
			FilePath:    DefaultAudioLogPath,
			Level:       DefaultLogLevel,
			ConsoleAlso: false,
		}
	}

	// Add birdweather log module if not explicitly configured
	// BirdWeather API interactions are logged separately for debugging integrations
	if _, hasBirdweather := cfg.ModuleOutputs["birdweather"]; !hasBirdweather {
		cfg.ModuleOutputs["birdweather"] = ModuleOutput{
			Enabled:     true,
			FilePath:    DefaultBirdweatherLogPath,
			Level:       DefaultLogLevel,
			ConsoleAlso: false,
		}
	}

	// Add weather log module if not explicitly configured
	// Weather provider interactions are logged separately
	if _, hasWeather := cfg.ModuleOutputs["weather"]; !hasWeather {
		cfg.ModuleOutputs["weather"] = ModuleOutput{
			Enabled:     true,
			FilePath:    DefaultWeatherLogPath,
			Level:       DefaultLogLevel,
			ConsoleAlso: false,
		}
	}

	// Add imageprovider log module if not explicitly configured
	// Image provider (Flickr, etc.) interactions are logged separately
	if _, hasImageprovider := cfg.ModuleOutputs["imageprovider"]; !hasImageprovider {
		cfg.ModuleOutputs["imageprovider"] = ModuleOutput{
			Enabled:     true,
			FilePath:    DefaultImageproviderLogPath,
			Level:       DefaultLogLevel,
			ConsoleAlso: false,
		}
	}
}
