package logger

// LoggingConfig represents logging configuration
type LoggingConfig struct {
	Level         string                  `yaml:"level" json:"level" mapstructure:"level"`                                               // debug, info, warn, error (deprecated, use DefaultLevel)
	Timezone      string                  `yaml:"timezone" json:"timezone" mapstructure:"timezone"`                                      // "Local", "UTC", or IANA timezone name like "Europe/Helsinki"
	File          string                  `yaml:"file" json:"file" mapstructure:"file"`                                                  // optional log file path (deprecated, use FileOutput)
	DebugWebhooks bool                    `yaml:"debug_webhooks" json:"debug_webhooks" mapstructure:"debug_webhooks"`                    // if true, logs full webhook details (headers, body, etc.)
	DefaultLevel  string                  `yaml:"default_level" json:"default_level" mapstructure:"default_level"`                       // default log level for all modules
	Console       *ConsoleOutput          `yaml:"console" json:"console" mapstructure:"console"`                                         // console output configuration
	FileOutput    *FileOutput             `yaml:"file_output" json:"file_output" mapstructure:"file_output"`                             // file output configuration
	ModuleOutputs map[string]ModuleOutput `yaml:"modules" json:"modules" mapstructure:"modules" jsonschema:"nullable"`                   // per-module output configuration
	ModuleLevels  map[string]string       `yaml:"module_levels" json:"module_levels" mapstructure:"module_levels" jsonschema:"nullable"` // per-module log levels
}

// ConsoleOutput represents console logging configuration.
// Console output uses human-readable text format without timestamps.
// Timestamps are omitted following the Twelve-Factor App methodology;
// the execution environment (journald, Docker) adds them automatically.
type ConsoleOutput struct {
	Enabled bool   `yaml:"enabled" json:"enabled" mapstructure:"enabled"` // enable console output
	Level   string `yaml:"level" json:"level" mapstructure:"level"`       // log level for console output
}

// FileOutput represents file logging configuration.
// File output uses JSON format with RFC3339 timestamps for machine parsing
// and log aggregation systems (ELK, Loki, Splunk, etc.).
type FileOutput struct {
	Enabled         bool   `yaml:"enabled" json:"enabled" mapstructure:"enabled"`                               // enable file output
	Path            string `yaml:"path" json:"path" mapstructure:"path"`                                        // log file path
	MaxSize         int    `yaml:"max_size" json:"max_size" mapstructure:"max_size"`                            // maximum size in MB before rotation (0 = disabled)
	MaxAge          int    `yaml:"max_age" json:"max_age" mapstructure:"max_age"`                               // maximum age in days to keep rotated logs (0 = no limit)
	MaxRotatedFiles int    `yaml:"max_rotated_files" json:"max_rotated_files" mapstructure:"max_rotated_files"` // maximum number of rotated log files to keep (0 = no limit)
	Compress        bool   `yaml:"compress" json:"compress" mapstructure:"compress"`                            // compress rotated logs with gzip
	Level           string `yaml:"level" json:"level" mapstructure:"level"`                                     // log level for file output
}

// ModuleOutput represents per-module output configuration
type ModuleOutput struct {
	Enabled         bool   `yaml:"enabled" json:"enabled" mapstructure:"enabled"`                               // enable module-specific output
	FilePath        string `yaml:"file_path" json:"file_path" mapstructure:"file_path"`                         // dedicated file path for this module
	Level           string `yaml:"level" json:"level" mapstructure:"level"`                                     // log level override for this module
	ConsoleAlso     bool   `yaml:"console_also" json:"console_also" mapstructure:"console_also"`                // also log to console
	MaxSize         int    `yaml:"max_size" json:"max_size" mapstructure:"max_size"`                            // maximum size in MB before rotation (0 = use FileOutput default)
	MaxAge          int    `yaml:"max_age" json:"max_age" mapstructure:"max_age"`                               // maximum age in days (0 = use FileOutput default)
	MaxRotatedFiles int    `yaml:"max_rotated_files" json:"max_rotated_files" mapstructure:"max_rotated_files"` // maximum number of rotated files (0 = use FileOutput default)
	Compress        *bool  `yaml:"compress,omitempty" json:"compress,omitempty" mapstructure:"compress"`        // compress rotated logs (nil = use FileOutput default)
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
	DefaultActionsLogPath       = "logs/actions.log"
	DefaultClassifierLogPath    = "logs/classifier.log"
	DefaultMaxSize              = 100   // MB before rotation
	DefaultMaxAge               = 30    // days to keep rotated files
	DefaultMaxRotatedFiles      = 10    // max number of rotated files
	DefaultCompressLogs         = false // compression disabled by default
	DefaultConsoleEnabled       = true
	DefaultFileEnabled          = true
)

// defaultModuleOutputs is the single source of truth for the per-module output
// defaults that applyConfigDefaults seeds for known modules. The two entries
// with ConsoleAlso: true keep bat-scheduler transitions and action-processing
// errors visible in journal/container environments where only stdout is
// captured; everything else is a plain per-module log file.
//
// Level is intentionally left empty so that ModuleLevels or DefaultLevel take
// precedence. Setting a non-empty Level here would silently override
// user-configured module_levels.
var defaultModuleOutputs = map[string]ModuleOutput{
	// HTTP request logs (access and API)
	"access": {Enabled: true, FilePath: DefaultAccessLogPath},
	"api":    {Enabled: true, FilePath: DefaultAccessLogPath}, // Same file as access logs

	// Security/authentication logs
	"security": {Enabled: true, FilePath: DefaultAuthLogPath},
	"auth":     {Enabled: true, FilePath: DefaultAuthLogPath},

	// Audio processing logs (high volume during operation)
	"audio":        {Enabled: true, FilePath: DefaultAudioLogPath},
	"audio.ffmpeg": {Enabled: true, FilePath: DefaultAudioLogPath},
	"audiocore":    {Enabled: true, FilePath: DefaultAudioLogPath},

	// External service integration logs
	"birdweather":   {Enabled: true, FilePath: DefaultBirdweatherLogPath},
	"weather":       {Enabled: true, FilePath: DefaultWeatherLogPath},
	"imageprovider": {Enabled: true, FilePath: DefaultImageproviderLogPath},

	// Spectrogram generation logs
	"spectrogram":             {Enabled: true, FilePath: DefaultSpectrogramLogPath},
	"spectrogram.prerenderer": {Enabled: true, FilePath: DefaultSpectrogramLogPath}, // Same file

	// Classifier orchestrator and scheduler logs
	"birdnet": {Enabled: true, FilePath: DefaultClassifierLogPath, ConsoleAlso: true},

	// Action processing logs (command execution, database saves, notifications)
	"analysis.processor": {Enabled: true, FilePath: DefaultActionsLogPath, ConsoleAlso: true},
}

// DefaultModuleOutput returns the built-in default ModuleOutput for a known
// module name, if one exists. It lets the config-loading layer seed a
// partial user entry with the same defaults applyConfigDefaults would have
// applied had the entry been absent altogether: dotted module names never
// get viper defaults of their own, so a partial entry must not silently
// zero the fields the user did not set.
func DefaultModuleOutput(module string) (ModuleOutput, bool) {
	out, ok := defaultModuleOutputs[module]
	return out, ok
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
	for module, def := range defaultModuleOutputs {
		if _, exists := cfg.ModuleOutputs[module]; !exists {
			cfg.ModuleOutputs[module] = def
		}
	}
}
