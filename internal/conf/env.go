// env.go - Environment variable configuration and validation for BirdNET-Go
package conf

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/spf13/viper"
	"github.com/tphakala/birdnet-go/internal/errors"
)

// BirdNET core configuration keys for viper config paths.
const (
	// BirdNET Core Configuration
	ConfigKeyLocale      = "birdnet.locale"
	ConfigKeyLatitude    = "birdnet.latitude"
	ConfigKeyLongitude   = "birdnet.longitude"
	ConfigKeySensitivity = "birdnet.sensitivity"
	ConfigKeyThreshold   = "birdnet.threshold"
	ConfigKeyOverlap     = "birdnet.overlap"
	ConfigKeyThreads     = "birdnet.threads"
	ConfigKeyDebug       = "birdnet.debug"
	ConfigKeyUseXNNPACK  = "birdnet.usexnnpack"

	// Model Paths
	ConfigKeyModelPath = "birdnet.modelpath"
	ConfigKeyLabelPath = "birdnet.labelpath"

	// Range Filter Configuration
	ConfigKeyRangeFilterModel     = "birdnet.rangefilter.model"
	ConfigKeyRangeFilterThreshold = "birdnet.rangefilter.threshold"
	ConfigKeyRangeFilterModelPath = "birdnet.rangefilter.modelpath"
	ConfigKeyRangeFilterDebug     = "birdnet.rangefilter.debug"
)

// Environment variable names that map to configuration keys.
const (
	// BirdNET Core Configuration
	EnvVarLocale      = "BIRDNET_LOCALE"
	EnvVarLatitude    = "BIRDNET_LATITUDE"
	EnvVarLongitude   = "BIRDNET_LONGITUDE"
	EnvVarSensitivity = "BIRDNET_SENSITIVITY"
	EnvVarThreshold   = "BIRDNET_THRESHOLD"
	EnvVarOverlap     = "BIRDNET_OVERLAP"
	EnvVarThreads     = "BIRDNET_THREADS"
	EnvVarDebug       = "BIRDNET_DEBUG"
	EnvVarUseXNNPACK  = "BIRDNET_USEXNNPACK"

	// Model Paths
	EnvVarModelPath = "BIRDNET_MODELPATH"
	EnvVarLabelPath = "BIRDNET_LABELPATH"

	// Range Filter Configuration
	EnvVarRangeFilterModel     = "BIRDNET_RANGEFILTER_MODEL"
	EnvVarRangeFilterThreshold = "BIRDNET_RANGEFILTER_THRESHOLD"
	EnvVarRangeFilterModelPath = "BIRDNET_RANGEFILTER_MODELPATH"
	EnvVarRangeFilterDebug     = "BIRDNET_RANGEFILTER_DEBUG"
)

// Validation constraint constants for environment variable ranges.
const (
	// Latitude/Longitude ranges
	LatitudeMin  = -90.0
	LatitudeMax  = 90.0
	LongitudeMin = -180.0
	LongitudeMax = 180.0

	// BirdNET sensitivity range
	SensitivityMin = 0.1
	SensitivityMax = 1.5

	// Detection threshold range (both regular and range filter)
	ThresholdMin = 0.0
	ThresholdMax = 1.0

	// Audio overlap range
	OverlapMin = 0.0
	OverlapMax = 2.9

	// Thread count minimum (no maximum enforced)
	ThreadsMin = 0
)

// envBinding holds metadata for environment variable bindings (internal use)
type envBinding struct {
	ConfigKey string             // Viper config key
	EnvVar    string             // Environment variable name
	Validate  func(string) error // Optional validation function
}

// getEnvBindings returns all environment variable bindings with validation
func getEnvBindings() []envBinding {
	return []envBinding{
		// BirdNET Core Configuration
		{ConfigKeyLocale, EnvVarLocale, validateEnvLocale},
		{ConfigKeyLatitude, EnvVarLatitude, validateEnvLatitude},
		{ConfigKeyLongitude, EnvVarLongitude, validateEnvLongitude},
		{ConfigKeySensitivity, EnvVarSensitivity, validateEnvSensitivity},
		{ConfigKeyThreshold, EnvVarThreshold, validateEnvThreshold},
		{ConfigKeyOverlap, EnvVarOverlap, validateEnvOverlap},
		{ConfigKeyThreads, EnvVarThreads, validateEnvThreads},
		{ConfigKeyDebug, EnvVarDebug, validateEnvBool},
		{ConfigKeyUseXNNPACK, EnvVarUseXNNPACK, validateEnvBool},
		
		// Model Paths
		{ConfigKeyModelPath, EnvVarModelPath, validateEnvPath},
		{ConfigKeyLabelPath, EnvVarLabelPath, validateEnvPath},
		
		// Range Filter Configuration
		{ConfigKeyRangeFilterModel, EnvVarRangeFilterModel, validateEnvRangeFilterModel},
		{ConfigKeyRangeFilterThreshold, EnvVarRangeFilterThreshold, validateEnvRangeFilterThreshold},
		{ConfigKeyRangeFilterModelPath, EnvVarRangeFilterModelPath, validateEnvPath},
		{ConfigKeyRangeFilterDebug, EnvVarRangeFilterDebug, validateEnvBool},
	}
}

// bindEnvVars sets up environment variable bindings with validation (internal)
func bindEnvVars() error {
	bindings := getEnvBindings()
	var errs []error

	for _, binding := range bindings {
		// Bind the environment variable to the config key
		if err := viper.BindEnv(binding.ConfigKey, binding.EnvVar); err != nil {
			errs = append(errs, fmt.Errorf("failed to bind %s: %w", binding.EnvVar, err))
			continue
		}

		// Validate the value if it's set and validation function is provided
		if envValue, present := os.LookupEnv(binding.EnvVar); present {
			if binding.Validate != nil {
				if err := binding.Validate(envValue); err != nil {
					errs = append(errs, fmt.Errorf("%s=%q: %w", binding.EnvVar, envValue, err))
				} else {
					// Canonicalize values after successful validation
					canonicalizeValue(binding.ConfigKey, envValue)
				}
			}
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("environment variable issues: %w", errors.Join(errs...))
	}

	return nil
}

// Environment variable validation functions

// validateEnvBool validates boolean environment variables
func validateEnvBool(value string) error {
	value = strings.TrimSpace(value)
	_, err := strconv.ParseBool(value)
	if err != nil {
		return fmt.Errorf("invalid boolean value '%s': must be true/false, 1/0, t/f, TRUE/FALSE, T/F", value)
	}
	return nil
}

// localePattern matches locale patterns like "en" or "en-us"
var localePattern = regexp.MustCompile(`(?i)^[a-z]{2}(-[a-z]{2})?$`)

func validateEnvLocale(value string) error {
	value = strings.TrimSpace(value)
	if len(value) < 2 || len(value) > 10 {
		return fmt.Errorf("locale must be 2-10 characters (got %d), expected pattern: 'en' or 'en-us', actual: '%s'", len(value), value)
	}
	// Check pattern for valid locale format
	if !localePattern.MatchString(value) {
		return fmt.Errorf("locale must match pattern 'xx' or 'xx-xx' (e.g., 'en' or 'en-us'), got: '%s'", value)
	}
	return nil
}

func validateEnvLatitude(value string) error {
	value = strings.TrimSpace(value)
	lat, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return fmt.Errorf("invalid latitude: %w", err)
	}
	if lat < LatitudeMin || lat > LatitudeMax {
		return fmt.Errorf("latitude must be between %g and %g, got %g", LatitudeMin, LatitudeMax, lat)
	}
	return nil
}

func validateEnvLongitude(value string) error {
	value = strings.TrimSpace(value)
	lng, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return fmt.Errorf("invalid longitude: %w", err)
	}
	if lng < LongitudeMin || lng > LongitudeMax {
		return fmt.Errorf("longitude must be between %g and %g, got %g", LongitudeMin, LongitudeMax, lng)
	}
	return nil
}

func validateEnvSensitivity(value string) error {
	value = strings.TrimSpace(value)
	sensitivity, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return fmt.Errorf("invalid sensitivity: %w", err)
	}
	if sensitivity < SensitivityMin || sensitivity > SensitivityMax {
		return fmt.Errorf("sensitivity must be between %g and %g, got %g", SensitivityMin, SensitivityMax, sensitivity)
	}
	return nil
}

func validateEnvThreshold(value string) error {
	value = strings.TrimSpace(value)
	threshold, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return fmt.Errorf("invalid threshold: %w", err)
	}
	if threshold < ThresholdMin || threshold > ThresholdMax {
		return fmt.Errorf("threshold must be between %g and %g, got %g", ThresholdMin, ThresholdMax, threshold)
	}
	return nil
}

func validateEnvOverlap(value string) error {
	value = strings.TrimSpace(value)
	overlap, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return fmt.Errorf("invalid overlap: %w", err)
	}
	if overlap < OverlapMin || overlap > OverlapMax {
		return fmt.Errorf("overlap must be between %g and %g, got %g", OverlapMin, OverlapMax, overlap)
	}
	return nil
}

func validateEnvThreads(value string) error {
	value = strings.TrimSpace(value)
	threads, err := strconv.Atoi(value)
	if err != nil {
		return fmt.Errorf("invalid threads: %w", err)
	}
	if threads < ThreadsMin {
		return fmt.Errorf("threads must be >= %d, got %d", ThreadsMin, threads)
	}
	return nil
}

func validateEnvRangeFilterModel(value string) error {
	value = strings.TrimSpace(value)
	validModels := []string{"latest", "legacy"}
	if !slices.Contains(validModels, value) {
		return fmt.Errorf("must be one of: %s", strings.Join(validModels, ", "))
	}
	return nil
}

func validateEnvRangeFilterThreshold(value string) error {
	value = strings.TrimSpace(value)
	threshold, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return fmt.Errorf("invalid range filter threshold: %w", err)
	}
	if threshold < ThresholdMin || threshold > ThresholdMax {
		return fmt.Errorf("range filter threshold must be between %g and %g, got %g", ThresholdMin, ThresholdMax, threshold)
	}
	return nil
}

func validateEnvPath(value string) error {
	// Trim leading/trailing whitespace first
	value = strings.TrimSpace(value)
	
	// Explicitly reject empty input
	if value == "" {
		return fmt.Errorf("path must not be empty")
	}
	
	// Clean the path to normalize it
	cleanedPath := filepath.Clean(value)
	
	// Require absolute paths for security
	if !filepath.IsAbs(cleanedPath) {
		return fmt.Errorf("path must be absolute, got relative path: %s", cleanedPath)
	}
	
	// filepath.Clean already handles path traversal, so no need for additional checks
	// Only return fatal errors - let caller handle existence warnings
	return nil
}

// canonicalizeValue normalizes environment variable values to appropriate types after validation
func canonicalizeValue(configKey, envValue string) {
	trimmed := strings.TrimSpace(envValue)
	
	// Determine value type based on config key and canonicalize accordingly
	switch configKey {
	// Boolean values
	case ConfigKeyDebug, ConfigKeyUseXNNPACK, ConfigKeyRangeFilterDebug:
		if parsed, err := strconv.ParseBool(strings.ToLower(trimmed)); err == nil {
			viper.Set(configKey, parsed)
		} else {
			// Safe fallback: set trimmed string if parsing fails unexpectedly
			viper.Set(configKey, trimmed)
		}
		
	// Integer values
	case ConfigKeyThreads:
		if parsed, err := strconv.Atoi(trimmed); err == nil {
			viper.Set(configKey, parsed)
		} else {
			// Safe fallback: set trimmed string if parsing fails unexpectedly
			viper.Set(configKey, trimmed)
		}
		
	// Float64 values
	case ConfigKeyLatitude, ConfigKeyLongitude, ConfigKeySensitivity, 
		 ConfigKeyThreshold, ConfigKeyOverlap, ConfigKeyRangeFilterThreshold:
		if parsed, err := strconv.ParseFloat(trimmed, 64); err == nil {
			viper.Set(configKey, parsed)
		} else {
			// Safe fallback: set trimmed string if parsing fails unexpectedly
			viper.Set(configKey, trimmed)
		}
		
	// String values (including locale with special handling)
	case ConfigKeyLocale:
		// Canonicalize locale to lowercase
		viper.Set(configKey, strings.ToLower(trimmed))
		
	case ConfigKeyModelPath, ConfigKeyLabelPath, ConfigKeyRangeFilterModel, ConfigKeyRangeFilterModelPath:
		// Regular string values - just trim whitespace
		viper.Set(configKey, trimmed)
		
	default:
		// Safe fallback for any unhandled config keys - set trimmed string
		viper.Set(configKey, trimmed)
	}
}

// configureEnvironmentVariables sets up environment variable support for Viper
func configureEnvironmentVariables() error {
	// Set up key replacer for nested config keys
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	
	// Bind specific environment variables with validation
	// Return any errors to the caller for centralized handling
	return bindEnvVars()
}