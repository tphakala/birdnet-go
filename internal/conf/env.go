// env.go - Environment variable configuration and validation for BirdNET-Go
package conf

import (
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/viper"
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
		{"birdnet.locale", "BIRDNET_LOCALE", validateEnvLocale},
		{"birdnet.latitude", "BIRDNET_LATITUDE", validateEnvLatitude},
		{"birdnet.longitude", "BIRDNET_LONGITUDE", validateEnvLongitude},
		{"birdnet.sensitivity", "BIRDNET_SENSITIVITY", validateEnvSensitivity},
		{"birdnet.threshold", "BIRDNET_THRESHOLD", validateEnvThreshold},
		{"birdnet.overlap", "BIRDNET_OVERLAP", validateEnvOverlap},
		{"birdnet.threads", "BIRDNET_THREADS", validateEnvThreads},
		{"birdnet.debug", "BIRDNET_DEBUG", nil}, // Bool validation handled by viper
		{"birdnet.usexnnpack", "BIRDNET_USEXNNPACK", nil}, // Bool validation handled by viper
		
		// Model Paths
		{"birdnet.modelpath", "BIRDNET_MODELPATH", validateEnvPath},
		{"birdnet.labelpath", "BIRDNET_LABELPATH", validateEnvPath},
		
		// Range Filter Configuration
		{"birdnet.rangefilter.model", "BIRDNET_RANGEFILTER_MODEL", validateEnvRangeFilterModel},
		{"birdnet.rangefilter.threshold", "BIRDNET_RANGEFILTER_THRESHOLD", validateEnvRangeFilterThreshold},
		{"birdnet.rangefilter.modelpath", "BIRDNET_RANGEFILTER_MODELPATH", validateEnvPath},
		{"birdnet.rangefilter.debug", "BIRDNET_RANGEFILTER_DEBUG", nil}, // Bool validation handled by viper
	}
}

// bindEnvVars sets up environment variable bindings with validation (internal)
func bindEnvVars() error {
	bindings := getEnvBindings()
	var warnings []string

	for _, binding := range bindings {
		// Bind the environment variable to the config key
		if err := viper.BindEnv(binding.ConfigKey, binding.EnvVar); err != nil {
			warnings = append(warnings, fmt.Sprintf("Failed to bind %s: %v", binding.EnvVar, err))
			continue
		}

		// Validate the value if it's set and validation function is provided
		if binding.Validate != nil {
			if envValue := os.Getenv(binding.EnvVar); envValue != "" {
				if err := binding.Validate(envValue); err != nil {
					warnings = append(warnings, fmt.Sprintf("Invalid %s value '%s': %v", binding.EnvVar, envValue, err))
				}
			}
		}
	}

	if len(warnings) > 0 {
		return fmt.Errorf("environment variable issues:\n  - %s", strings.Join(warnings, "\n  - "))
	}

	return nil
}

// Environment variable validation functions

func validateEnvLocale(value string) error {
	if len(value) < 2 || len(value) > 10 {
		return fmt.Errorf("locale must be 2-10 characters, got %d", len(value))
	}
	return nil
}

func validateEnvLatitude(value string) error {
	lat, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return fmt.Errorf("invalid latitude: %w", err)
	}
	if lat < -90 || lat > 90 {
		return fmt.Errorf("latitude must be between -90 and 90, got %g", lat)
	}
	return nil
}

func validateEnvLongitude(value string) error {
	lng, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return fmt.Errorf("invalid longitude: %w", err)
	}
	if lng < -180 || lng > 180 {
		return fmt.Errorf("longitude must be between -180 and 180, got %g", lng)
	}
	return nil
}

func validateEnvSensitivity(value string) error {
	sensitivity, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return fmt.Errorf("invalid sensitivity: %w", err)
	}
	if sensitivity < 0.1 || sensitivity > 1.5 {
		return fmt.Errorf("sensitivity must be between 0.1 and 1.5, got %g", sensitivity)
	}
	return nil
}

func validateEnvThreshold(value string) error {
	threshold, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return fmt.Errorf("invalid threshold: %w", err)
	}
	if threshold < 0.0 || threshold > 1.0 {
		return fmt.Errorf("threshold must be between 0.0 and 1.0, got %g", threshold)
	}
	return nil
}

func validateEnvOverlap(value string) error {
	overlap, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return fmt.Errorf("invalid overlap: %w", err)
	}
	if overlap < 0.0 || overlap > 2.9 {
		return fmt.Errorf("overlap must be between 0.0 and 2.9, got %g", overlap)
	}
	return nil
}

func validateEnvThreads(value string) error {
	threads, err := strconv.Atoi(value)
	if err != nil {
		return fmt.Errorf("invalid threads: %w", err)
	}
	if threads < 0 {
		return fmt.Errorf("threads must be non-negative, got %d", threads)
	}
	return nil
}

func validateEnvRangeFilterModel(value string) error {
	validModels := []string{"latest", "legacy"}
	for _, valid := range validModels {
		if value == valid {
			return nil
		}
	}
	return fmt.Errorf("must be one of: %s", strings.Join(validModels, ", "))
}

func validateEnvRangeFilterThreshold(value string) error {
	threshold, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return fmt.Errorf("invalid range filter threshold: %w", err)
	}
	if threshold < 0.0 || threshold > 1.0 {
		return fmt.Errorf("range filter threshold must be between 0.0 and 1.0, got %g", threshold)
	}
	return nil
}

func validateEnvPath(value string) error {
	// Basic path traversal protection
	if strings.Contains(value, "..") {
		return fmt.Errorf("path traversal not allowed")
	}
	// Could add more path validation here (check if file exists, etc.)
	return nil
}

// configureEnvironmentVariables sets up environment variable support for Viper
func configureEnvironmentVariables() error {
	// Enable automatic environment variable reading
	viper.AutomaticEnv()
	viper.SetEnvPrefix("BIRDNET")
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	
	// Bind specific environment variables with validation
	if err := bindEnvVars(); err != nil {
		// Log warnings but don't fail startup
		// This allows the application to continue with config file/default values
		log.Printf("Environment variable validation warnings: %v", err)
	}
	
	return nil
}