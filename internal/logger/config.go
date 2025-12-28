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
