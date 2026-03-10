package conf

import (
	"os"
	"strings"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// TestViperUnmarshalLoggingConfig verifies that Viper correctly unmarshals
// logging configuration including module outputs with underscore-separated
// keys. This is a regression test for missing mapstructure tags that caused
// module-level debug settings to be silently ignored.
func TestViperUnmarshalLoggingConfig(t *testing.T) {
	t.Parallel()

	configContent := `
logging:
  default_level: debug
  debug_webhooks: true
  timezone: UTC
  console:
    enabled: true
    level: warn
  file_output:
    enabled: true
    path: logs/app.log
    level: debug
    max_size: 50
    max_age: 7
    max_rotated_files: 5
    compress: true
  modules:
    imageprovider:
      enabled: true
      file_path: logs/imageprovider.log
      level: debug
      console_also: true
      max_size: 25
      max_age: 14
      max_rotated_files: 3
    audio:
      enabled: true
      file_path: logs/audio.log
      level: trace
  module_levels:
    analysis: warn
    ebird: error
`

	v := viper.New()
	v.SetConfigType("yaml")

	err := v.ReadConfig(strings.NewReader(configContent))
	require.NoError(t, err)

	var settings Settings
	err = v.Unmarshal(&settings, viper.DecodeHook(DurationDecodeHook()))
	require.NoError(t, err)

	cfg := settings.Logging

	// Verify top-level fields
	assert.Equal(t, "debug", cfg.DefaultLevel, "DefaultLevel should be unmarshaled from default_level")
	assert.True(t, cfg.DebugWebhooks, "DebugWebhooks should be unmarshaled from debug_webhooks")
	assert.Equal(t, "UTC", cfg.Timezone)

	// Verify console config
	require.NotNil(t, cfg.Console, "Console should not be nil")
	assert.True(t, cfg.Console.Enabled)
	assert.Equal(t, "warn", cfg.Console.Level)

	// Verify file output config (underscore keys)
	require.NotNil(t, cfg.FileOutput, "FileOutput should not be nil (mapped from file_output)")
	assert.True(t, cfg.FileOutput.Enabled)
	assert.Equal(t, "logs/app.log", cfg.FileOutput.Path)
	assert.Equal(t, "debug", cfg.FileOutput.Level)
	assert.Equal(t, 50, cfg.FileOutput.MaxSize, "MaxSize should be unmarshaled from max_size")
	assert.Equal(t, 7, cfg.FileOutput.MaxAge, "MaxAge should be unmarshaled from max_age")
	assert.Equal(t, 5, cfg.FileOutput.MaxRotatedFiles, "MaxRotatedFiles should be unmarshaled from max_rotated_files")
	assert.True(t, cfg.FileOutput.Compress)

	// Verify module outputs (the critical fix — "modules" maps to ModuleOutputs)
	require.NotNil(t, cfg.ModuleOutputs, "ModuleOutputs should not be nil (mapped from modules)")
	require.Contains(t, cfg.ModuleOutputs, "imageprovider")
	require.Contains(t, cfg.ModuleOutputs, "audio")

	imgMod := cfg.ModuleOutputs["imageprovider"]
	assert.True(t, imgMod.Enabled)
	assert.Equal(t, "logs/imageprovider.log", imgMod.FilePath, "FilePath should be unmarshaled from file_path")
	assert.Equal(t, "debug", imgMod.Level, "Module level should be 'debug' — this was the root cause of missing debug logs")
	assert.True(t, imgMod.ConsoleAlso, "ConsoleAlso should be unmarshaled from console_also")
	assert.Equal(t, 25, imgMod.MaxSize)
	assert.Equal(t, 14, imgMod.MaxAge)
	assert.Equal(t, 3, imgMod.MaxRotatedFiles)

	audioMod := cfg.ModuleOutputs["audio"]
	assert.True(t, audioMod.Enabled)
	assert.Equal(t, "logs/audio.log", audioMod.FilePath)
	assert.Equal(t, "trace", audioMod.Level)

	// Verify module levels (separate from module outputs)
	require.NotNil(t, cfg.ModuleLevels, "ModuleLevels should not be nil (mapped from module_levels)")
	assert.Equal(t, "warn", cfg.ModuleLevels["analysis"])
	assert.Equal(t, "error", cfg.ModuleLevels["ebird"])
}

// TestViperUnmarshalLoggingDefaults verifies that Viper SetDefault values
// for logging modules are correctly unmarshaled with mapstructure tags.
func TestViperUnmarshalLoggingDefaults(t *testing.T) {
	t.Parallel()

	v := viper.New()

	// Set defaults the same way setDefaultConfig does
	v.SetDefault("logging.default_level", "info")
	v.SetDefault("logging.console.enabled", true)
	v.SetDefault("logging.console.level", "info")
	v.SetDefault("logging.file_output.enabled", true)
	v.SetDefault("logging.file_output.path", "logs/application.log")
	v.SetDefault("logging.file_output.level", "info")
	v.SetDefault("logging.file_output.max_size", 100)
	v.SetDefault("logging.file_output.max_age", 30)
	v.SetDefault("logging.file_output.max_rotated_files", 10)
	v.SetDefault("logging.file_output.compress", false)
	v.SetDefault("logging.modules.imageprovider.enabled", true)
	v.SetDefault("logging.modules.imageprovider.file_path", "logs/imageprovider.log")
	v.SetDefault("logging.modules.imageprovider.level", "debug")
	v.SetDefault("logging.module_levels.analysis", "warn")

	var settings Settings
	err := v.Unmarshal(&settings, viper.DecodeHook(DurationDecodeHook()))
	require.NoError(t, err)

	cfg := settings.Logging

	assert.Equal(t, "info", cfg.DefaultLevel)

	require.NotNil(t, cfg.FileOutput)
	assert.Equal(t, "logs/application.log", cfg.FileOutput.Path)
	assert.Equal(t, 100, cfg.FileOutput.MaxSize)
	assert.Equal(t, 30, cfg.FileOutput.MaxAge, "MaxAge should be unmarshaled from max_age default")
	assert.Equal(t, 10, cfg.FileOutput.MaxRotatedFiles, "MaxRotatedFiles should be unmarshaled from max_rotated_files default")
	assert.False(t, cfg.FileOutput.Compress, "Compress should default to false")

	require.NotNil(t, cfg.ModuleOutputs, "ModuleOutputs must be populated from logging.modules.* defaults")
	require.Contains(t, cfg.ModuleOutputs, "imageprovider")
	assert.Equal(t, "debug", cfg.ModuleOutputs["imageprovider"].Level,
		"Module level from defaults should be 'debug'")
	assert.Equal(t, "logs/imageprovider.log", cfg.ModuleOutputs["imageprovider"].FilePath)

	require.NotNil(t, cfg.ModuleLevels, "ModuleLevels must be populated from logging.module_levels.* defaults")
	assert.Equal(t, "warn", cfg.ModuleLevels["analysis"])
}

// TestLoggingModuleLevelAppliedToLogger verifies the end-to-end flow:
// module level from config reaches the CentralLogger and produces a
// logger that actually enables debug messages.
func TestLoggingModuleLevelAppliedToLogger(t *testing.T) {
	t.Parallel()

	logPath := t.TempDir() + "/test.log"
	cfg := &logger.LoggingConfig{
		DefaultLevel: "info",
		Timezone:     "UTC",
		Console: &logger.ConsoleOutput{
			Enabled: false, // disable console to avoid noise
		},
		ModuleOutputs: map[string]logger.ModuleOutput{
			"testmod": {
				Enabled:  true,
				FilePath: logPath,
				Level:    "debug",
			},
		},
	}

	cl, err := logger.NewCentralLogger(cfg)
	require.NoError(t, err)
	defer func() { require.NoError(t, cl.Close()) }()

	log := cl.Module("testmod")
	require.NotNil(t, log)

	// Debug should not be filtered — the module is set to debug level
	log.Debug("test debug message", logger.String("key", "value"))
	log.Info("test info message")

	// Flush and read the log file to verify debug entries appear
	require.NoError(t, cl.Flush())

	content, err := os.ReadFile(logPath)
	require.NoError(t, err)

	assert.Contains(t, string(content), "DEBUG", "Log file should contain DEBUG entries when module level is debug")
	assert.Contains(t, string(content), "test debug message")
	assert.Contains(t, string(content), "INFO")
	assert.Contains(t, string(content), "test info message")
}

// TestLoggingModuleLevelInfoFiltersDebug verifies that info-level modules
// correctly filter out debug messages.
func TestLoggingModuleLevelInfoFiltersDebug(t *testing.T) {
	t.Parallel()

	logPath := t.TempDir() + "/test.log"
	cfg := &logger.LoggingConfig{
		DefaultLevel: "info",
		Timezone:     "UTC",
		Console: &logger.ConsoleOutput{
			Enabled: false,
		},
		ModuleOutputs: map[string]logger.ModuleOutput{
			"testmod": {
				Enabled:  true,
				FilePath: logPath,
				Level:    "info",
			},
		},
	}

	cl, err := logger.NewCentralLogger(cfg)
	require.NoError(t, err)
	defer func() { require.NoError(t, cl.Close()) }()

	log := cl.Module("testmod")
	log.Debug("should not appear")
	log.Info("should appear")

	require.NoError(t, cl.Flush())

	content, err := os.ReadFile(logPath)
	require.NoError(t, err)

	assert.NotContains(t, string(content), "should not appear", "Debug messages should be filtered at info level")
	assert.Contains(t, string(content), "should appear")
}
