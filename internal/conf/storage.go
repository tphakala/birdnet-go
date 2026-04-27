package conf

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/spf13/viper"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
	"gopkg.in/yaml.v3"
)

// ConfigPath is the explicit path to the configuration file, set by CLI flags
// before Load() is called.
var ConfigPath string

// settingsInstance holds the current *Settings snapshot. Published via
// atomic.Pointer.Store and never mutated in place; writers produce a new
// snapshot via CloneSettings + StoreSettings.
//
// loadMu guards the on-demand Load() that Setting() performs when
// settingsInstance is nil. A plain sync.Mutex is used rather than sync.Once
// so that cleanup paths which call SetTestSettings(nil) can trigger a fresh
// Load on the next Setting() call; sync.Once cannot be reset safely from a
// parallel test without racing on the Once value itself.
var (
	settingsInstance atomic.Pointer[Settings]
	loadMu           sync.Mutex
)

// Load reads the configuration file and environment variables into GlobalConfig.
//
//nolint:gocognit // Config loading is inherently complex; splitting adds indirection without clarity.
func Load() (*Settings, error) {
	// Create a new settings struct
	settings := &Settings{}

	// Initialize viper and read config
	if err := initViper(); err != nil {
		return nil, errors.New(err).
			Category(errors.CategoryConfiguration).
			Context("operation", "init-viper").
			Build()
	}

	// Legacy stream entries omitted the enabled field entirely. Materialize the
	// missing field before unmarshal so the runtime struct can use a plain bool.
	streamEnabledMigrated := migrateStreamEnabledDefaults()

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

	// Migrate legacy AutoTLS boolean to new TLSMode field
	if settings.MigrateTLSConfig() {
		persistMigration(settings, "TLS")
	}

	// Migrate legacy OAuth configuration to new array format
	if settings.MigrateOAuthConfig() {
		persistMigration(settings, "OAuth")
	}

	// Migrate legacy RTSP URLs to new streams format
	if settings.MigrateRTSPConfig() {
		persistMigration(settings, "RTSP")
	}

	// Migrate legacy single audio source to new multi-source format
	if settings.MigrateAudioSourceConfig() {
		persistMigration(settings, "audio source")
	}

	// Migrate per-source model field (model -> models)
	if settings.MigrateSourceModels() {
		persistMigration(settings, "source models")
	}

	if streamEnabledMigrated {
		persistMigration(settings, "stream enabled defaults")
	}

	// Validate multi-model configuration
	if err := settings.applyModelValidation(); err != nil {
		return nil, err
	}

	// Migrate dashboard layout for existing installations
	if settings.MigrateDashboardLayout() {
		persistMigration(settings, "dashboard layout")
	}

	// Apply default transport to RTSP/RTMP streams that don't specify one
	settings.Realtime.RTSP.ApplyStreamDefaults()

	// Migrate LocationConfigured for backward compatibility with existing configs.
	if settings.MigrateLocationConfigured() {
		persistMigration(settings, "LocationConfigured flag")
	}

	// Auto-generate SessionSecret if not set (for backward compatibility)
	if err := ensureSessionSecret(settings); err != nil {
		return nil, err
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

	// Publish the loaded settings atomically. Readers calling GetSettings
	// immediately after this point see this snapshot.
	settingsInstance.Store(settings)
	return settings, nil
}

// initViper initializes viper with default values and reads the configuration file.
func initViper() error {
	viper.SetConfigType("yaml")

	// Configure environment variable support
	if err := configureEnvironmentVariables(); err != nil {
		// Log any validation warnings but don't fail startup
		// This allows the application to continue with config file/default values
		GetLogger().Warn("Environment variable configuration warning", logger.Error(err))
	}

	// Resolve effective config path. ConfigPath is the explicit override;
	// fall back to scanning os.Args if it wasn't set yet (e.g., called
	// before main parses args). Use a local variable to avoid mutating
	// package-global state from the fallback parser.
	effectiveConfigPath := ConfigPath
	configFlagPresent := ConfigPath != ""
	if !configFlagPresent {
		for i, arg := range os.Args {
			if (arg == "--config" || arg == "-c") && i+1 < len(os.Args) {
				configFlagPresent = true
				effectiveConfigPath = os.Args[i+1]
				break
			}
			if val, found := strings.CutPrefix(arg, "--config="); found {
				configFlagPresent = true
				effectiveConfigPath = val
				break
			}
			if val, found := strings.CutPrefix(arg, "-c="); found {
				configFlagPresent = true
				effectiveConfigPath = val
				break
			}
		}
	}

	// Reject empty config path when the flag was explicitly provided
	if configFlagPresent && effectiveConfigPath == "" {
		return fmt.Errorf("--config flag requires a non-empty file path")
	}

	// If a custom config path was specified via --config, use it directly
	if effectiveConfigPath != "" {
		viper.SetConfigFile(effectiveConfigPath)
	} else {
		viper.SetConfigName("config")

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
	}

	// Set default values for each configuration parameter
	// function defined in defaults.go
	setDefaultConfig()

	// Read configuration file
	err := viper.ReadInConfig()
	if err != nil {
		// When an explicit config path was given, any read error is fatal -
		// don't fall back to creating a default config elsewhere.
		if effectiveConfigPath != "" {
			return errors.New(err).
				Category(errors.CategoryConfiguration).
				Context("operation", "read-config-file").
				Context("config_path", effectiveConfigPath).
				Build()
		}

		// For default path search: ConfigFileNotFoundError means no config
		// exists yet, so create one with defaults.
		var configFileNotFoundError viper.ConfigFileNotFoundError
		if errors.As(err, &configFileNotFoundError) {
			return createDefaultConfig()
		}

		// Other errors (parse failures, permission issues) are fatal.
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
		clientSecret, err := GenerateRandomSecret()
		if err != nil {
			return errors.New(err).
				Component("conf").
				Category(errors.CategoryConfiguration).
				Context("operation", "generate_client_secret").
				Build()
		}
		viper.Set("security.basicauth.clientsecret", clientSecret)
	}
	// If the session secret is not set, generate a random one
	// This ensures backward compatibility for existing deployments
	if viper.GetString("security.sessionsecret") == "" {
		sessionSecret, err := GenerateRandomSecret()
		if err != nil {
			return errors.New(err).
				Component("conf").
				Category(errors.CategoryConfiguration).
				Context("operation", "generate_session_secret").
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

// GetSettings returns the current settings snapshot. Safe for concurrent use;
// the returned pointer is published via atomic.Pointer.Store and never mutated
// in place. Writers produce a new snapshot via CloneSettings + StoreSettings.
func GetSettings() *Settings {
	return settingsInstance.Load()
}

// CurrentOrFallback returns the latest published settings snapshot, or the
// supplied fallback when none has been published. It exists so long-lived
// services can pick up UI edits without a restart: capture the initial
// *Settings in a field, then call conf.CurrentOrFallback(s.settings) whenever
// the value is actually read. In production the fallback is effectively
// unreachable (settings are always loaded before services start); it's there
// so unit tests that inject a custom *Settings into a struct literal, without
// touching the package-global atomic pointer, keep working.
func CurrentOrFallback(fallback *Settings) *Settings {
	if latest := settingsInstance.Load(); latest != nil {
		return latest
	}
	return fallback
}

// StoreSettings publishes a new *Settings snapshot atomically. Callers must
// not mutate the pointee after calling StoreSettings; readers may observe the
// snapshot concurrently through GetSettings. The canonical writer pattern is:
//
//	current := conf.GetSettings()
//	updated := conf.CloneSettings(current)
//	// ... mutate updated ...
//	conf.StoreSettings(updated)
//
// Passing nil is valid and clears the stored settings (mostly useful in tests).
func StoreSettings(s *Settings) {
	settingsInstance.Store(s)
}

// Setting returns the current settings instance, initializing it if necessary
func Setting() *Settings {
	// Fast path: settings already published.
	if s := settingsInstance.Load(); s != nil {
		return s
	}
	// Slow path: lazy-load from disk. Serialise concurrent first-callers
	// through loadMu; the atomic check inside the lock collapses the extras.
	loadMu.Lock()
	if s := settingsInstance.Load(); s != nil {
		loadMu.Unlock()
		return s
	}
	if _, err := Load(); err != nil {
		// Fatal error loading settings - application cannot continue.
		// Release the lock explicitly because os.Exit does not run defers.
		enhancedErr := errors.New(err).
			Category(errors.CategoryConfiguration).
			Context("operation", "load-settings-init").
			Build()
		GetLogger().Error("Error loading settings", logger.Error(enhancedErr))
		loadMu.Unlock()
		os.Exit(1)
	}
	loadMu.Unlock()
	return settingsInstance.Load()
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
	// Load the current snapshot through the atomic pointer; the pointee is
	// immutable after publication so reading fields off it is race-free.
	current := settingsInstance.Load()
	if current == nil {
		return errors.Newf("settings not initialized").
			Category(errors.CategoryConfiguration).
			Context("operation", "save-settings").
			Build()
	}

	// Deep-clone the published snapshot before mutating it in
	// prepareSettingsForSave. The snapshot is immutable (range filter
	// writers use clone-mutate-publish), so CloneSettings captures a
	// consistent point-in-time copy without additional locking.
	settingsCopy := *CloneSettings(current)

	// Apply data transformations (seasonal tracking, etc.) on the clone.
	settingsCopy = prepareSettingsForSave(&settingsCopy, current.BirdNET.Latitude)

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
