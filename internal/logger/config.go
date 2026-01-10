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
	Enabled         bool   `yaml:"enabled" json:"enabled"`                     // enable file output
	Path            string `yaml:"path" json:"path"`                           // log file path
	MaxSize         int    `yaml:"max_size" json:"max_size"`                   // maximum size in MB before rotation (0 = disabled)
	MaxAge          int    `yaml:"max_age" json:"max_age"`                     // maximum age in days to keep rotated logs (0 = no limit)
	MaxRotatedFiles int    `yaml:"max_rotated_files" json:"max_rotated_files"` // maximum number of rotated log files to keep (0 = no limit)
	Compress        bool   `yaml:"compress" json:"compress"`                   // compress rotated logs with gzip
	Level           string `yaml:"level" json:"level"`                         // log level for file output
}

// ModuleOutput represents per-module output configuration
type ModuleOutput struct {
	Enabled         bool   `yaml:"enabled" json:"enabled"`                       // enable module-specific output
	FilePath        string `yaml:"file_path" json:"file_path"`                   // dedicated file path for this module
	Level           string `yaml:"level" json:"level"`                           // log level override for this module
	ConsoleAlso     bool   `yaml:"console_also" json:"console_also"`             // also log to console
	MaxSize         int    `yaml:"max_size" json:"max_size"`                     // maximum size in MB before rotation (0 = use FileOutput default)
	MaxAge          int    `yaml:"max_age" json:"max_age"`                       // maximum age in days (0 = use FileOutput default)
	MaxRotatedFiles int    `yaml:"max_rotated_files" json:"max_rotated_files"`   // maximum number of rotated files (0 = use FileOutput default)
	Compress        *bool  `yaml:"compress,omitempty" json:"compress,omitempty"` // compress rotated logs (nil = use FileOutput default)
}

// Default values for logging configuration.
// These match the defaults in conf/defaults.go to ensure consistency.
const (
	DefaultLogLevel             = "info"
	DefaultLogPath              = "logs/application.log"
	DefaultAccessLogPath        = "logs/access.log"
	DefaultAuthLogPath          = "logs/auth.log"
	DefaultAudioLogPath         = "logs/audio.log"
	DefaultBirdweatherLogPath   = "logs/birdweather.log"
	DefaultWeatherLogPath       = "logs/weather.log"
	DefaultImageproviderLogPath = "logs/imageprovider.log"
	DefaultSpectrogramLogPath   = "logs/spectrogram.log"
	DefaultMaxSize              = 100   // MB before rotation
	DefaultMaxAge               = 30    // days to keep rotated files
	DefaultMaxRotatedFiles      = 10    // max number of rotated files
	DefaultCompressLogs         = false // compression disabled by default
	DefaultConsoleEnabled       = true
	DefaultFileEnabled          = true
)

// ensureModuleOutput adds a default module output configuration if not already present.
// This helper reduces repetition when setting up default module configurations.
func ensureModuleOutput(cfg *LoggingConfig, module, filePath string) {
	if _, exists := cfg.ModuleOutputs[module]; !exists {
		cfg.ModuleOutputs[module] = ModuleOutput{
			Enabled:     true,
			FilePath:    filePath,
			Level:       DefaultLogLevel,
			ConsoleAlso: false,
		}
	}
}

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
			Enabled:         DefaultFileEnabled,
			Path:            DefaultLogPath,
			Level:           DefaultLogLevel,
			MaxSize:         DefaultMaxSize,
			MaxAge:          DefaultMaxAge,
			MaxRotatedFiles: DefaultMaxRotatedFiles,
			Compress:        DefaultCompressLogs,
		}
	}

	// Initialize module outputs map if nil
	if cfg.ModuleOutputs == nil {
		cfg.ModuleOutputs = make(map[string]ModuleOutput)
	}

	// Apply default module outputs for critical modules
	// Each module gets its own log file for separation of concerns:
	// - Security auditing (access, auth, security)
	// - Log rotation management
	// - Easier analysis and debugging

	// HTTP request logs (access and API)
	ensureModuleOutput(cfg, "access", DefaultAccessLogPath)
	ensureModuleOutput(cfg, "api", DefaultAccessLogPath) // Same file as access logs

	// Security/authentication logs
	ensureModuleOutput(cfg, "security", DefaultAuthLogPath)
	ensureModuleOutput(cfg, "auth", DefaultAuthLogPath)

	// Audio processing logs (high volume during operation)
	ensureModuleOutput(cfg, "audio", DefaultAudioLogPath)
	ensureModuleOutput(cfg, "audio.ffmpeg", DefaultAudioLogPath)

	// External service integration logs
	ensureModuleOutput(cfg, "birdweather", DefaultBirdweatherLogPath)
	ensureModuleOutput(cfg, "weather", DefaultWeatherLogPath)
	ensureModuleOutput(cfg, "imageprovider", DefaultImageproviderLogPath)

	// Spectrogram generation logs
	ensureModuleOutput(cfg, "spectrogram", DefaultSpectrogramLogPath)
	ensureModuleOutput(cfg, "spectrogram.prerenderer", DefaultSpectrogramLogPath) // Same file
}
