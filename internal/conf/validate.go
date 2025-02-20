// conf/validate.go

package conf

import (
	"errors"
	"fmt"
	"log"
	"net"
	"regexp"
	"strconv"
	"strings"
)

// ValidationError represents a collection of validation errors
type ValidationError struct {
	Errors []string
}

// Error returns a string representation of the validation errors
func (ve ValidationError) Error() string {
	return fmt.Sprintf("Validation errors: %v", ve.Errors)
}

// ValidateSettings validates the entire Settings struct
func ValidateSettings(settings *Settings) error {
	ve := ValidationError{}

	// Validate BirdNET settings
	if err := validateBirdNETSettings(&settings.BirdNET); err != nil {
		ve.Errors = append(ve.Errors, err.Error())
	}

	// Validate WebServer settings
	if err := validateWebServerSettings(&settings.WebServer); err != nil {
		ve.Errors = append(ve.Errors, err.Error())
	}

	// Validate Security settings
	if err := validateSecuritySettings(&settings.Security); err != nil {
		ve.Errors = append(ve.Errors, err.Error())
	}

	// Validate Realtime settings
	if err := validateRealtimeSettings(&settings.Realtime); err != nil {
		ve.Errors = append(ve.Errors, err.Error())
	}

	// Validate Birdweather settings
	if err := validateBirdweatherSettings(&settings.Realtime.Birdweather); err != nil {
		ve.Errors = append(ve.Errors, err.Error())
	}

	// Validate Audio settings
	if err := validateAudioSettings(&settings.Realtime.Audio); err != nil {
		ve.Errors = append(ve.Errors, err.Error())
	}

	// Validate Dashboard settings
	if err := validateDashboardSettings(&settings.Realtime.Dashboard); err != nil {
		ve.Errors = append(ve.Errors, err.Error())
	}

	// Validate Weather settings
	if err := validateWeatherSettings(&settings.Realtime.Weather); err != nil {
		ve.Errors = append(ve.Errors, err.Error())
	}

	// Validate Backup settings if enabled
	if settings.Backup.Enabled {
		if err := validateBackupConfig(&settings.Backup); err != nil {
			ve.Errors = append(ve.Errors, err.Error())
		}
	}

	// If there are any errors, return the ValidationError
	if len(ve.Errors) > 0 {
		return ve
	}
	return nil
}

// validateBirdNETSettings validates the BirdNET-specific settings
func validateBirdNETSettings(settings *BirdNETConfig) error {
	var errs []string

	// Check if sensitivity is within valid range
	if settings.Sensitivity < 0 || settings.Sensitivity > 1.5 {
		errs = append(errs, "BirdNET sensitivity must be between 0 and 1.5")
	}

	// Check if threshold is within valid range
	if settings.Threshold < 0 || settings.Threshold > 1 {
		errs = append(errs, "BirdNET threshold must be between 0 and 1")
	}

	// Check if overlap is within valid range
	if settings.Overlap < 0 || settings.Overlap > 2.99 {
		errs = append(errs, "BirdNET overlap value must be between 0 and 2.99 seconds")
	}

	// Check if longitude is within valid range
	if settings.Longitude < -180 || settings.Longitude > 180 {
		errs = append(errs, "BirdNET longitude must be between -180 and 180")
	}

	// Check if latitude is within valid range
	if settings.Latitude < -90 || settings.Latitude > 90 {
		errs = append(errs, "BirdNET latitude must be between -90 and 90")
	}

	// Check if threads is non-negative
	if settings.Threads < 0 {
		errs = append(errs, "BirdNET threads must be at least 0")
	}

	// Validate RangeFilter settings
	if settings.RangeFilter.Model == "" {
		errs = append(errs, "RangeFilter model must not be empty")
	}

	// Check if RangeFilter threshold is within valid range
	if settings.RangeFilter.Threshold < 0 || settings.RangeFilter.Threshold > 1 {
		errs = append(errs, "RangeFilter threshold must be between 0 and 1")
	}

	// If there are any errors, return them as a single error
	if len(errs) > 0 {
		return fmt.Errorf("BirdNET settings errors: %v", errs)
	}

	return nil
}

// validateWebServerSettings validates the WebServer-specific settings
func validateWebServerSettings(settings *struct {
	Debug   bool
	Enabled bool
	Port    string
	Log     LogConfig
}) error {
	if settings.Enabled {
		// Check if port is provided when enabled
		if settings.Port == "" {
			return errors.New("WebServer port is required when enabled")
		}
		// You might want to add more specific port validation here
	}

	return nil
}

// validateSecuritySettings validates the security-specific settings
func validateSecuritySettings(settings *Security) error {
	// Check if any OAuth provider is enabled
	if (settings.BasicAuth.Enabled || settings.GoogleAuth.Enabled || settings.GithubAuth.Enabled) && settings.Host == "" {
		return fmt.Errorf("security.host must be set when using authentication providers")
	}

	// Validate the subnet bypass setting against the allowed pattern
	if settings.AllowSubnetBypass.Enabled {
		subnets := strings.Split(settings.AllowSubnetBypass.Subnet, ",")
		for _, subnet := range subnets {
			_, _, err := net.ParseCIDR(strings.TrimSpace(subnet))
			if err != nil {
				return fmt.Errorf("invalid subnet format: %w", err)
			}
		}
	}

	return nil
}

// validateRealtimeSettings validates the Realtime-specific settings
func validateRealtimeSettings(settings *RealtimeSettings) error {
	// Check if interval is non-negative
	if settings.Interval < 0 {
		return errors.New("Realtime interval must be non-negative")
	}
	// Add more realtime settings validation as needed
	return nil
}

// validateBirdweatherSettings validates the Birdweather-specific settings
func validateBirdweatherSettings(settings *BirdweatherSettings) error {
	if settings.Enabled {
		// Check if ID is provided when enabled
		if settings.ID == "" {
			log.Println("Error: Birdweather ID is required when enabled. Disabling Birdweather.")
			settings.Enabled = false
			return nil
		}

		// Validate Birdweather ID format
		validIDPattern := regexp.MustCompile("^[a-zA-Z0-9]{24}$")
		if !validIDPattern.MatchString(settings.ID) {
			log.Println("Error: Invalid Birdweather ID format: must be 24 alphanumeric characters. Disabling Birdweather.")
			settings.Enabled = false
			return nil
		}

		// Check if threshold is within valid range
		if settings.Threshold < 0 || settings.Threshold > 1 {
			return errors.New("Birdweather threshold must be between 0 and 1")
		}

		// Check if location accuracy is non-negative
		if settings.LocationAccuracy < 0 {
			return errors.New("Birdweather location accuracy must be non-negative")
		}
	}
	return nil
}

// validateAudioSettings validates the audio settings and sets ffmpeg and sox paths
func validateAudioSettings(settings *AudioSettings) error {
	// Check if ffmpeg is available
	if IsFfmpegAvailable() {
		settings.FfmpegPath = GetFfmpegBinaryName()
	} else {
		settings.FfmpegPath = ""
		log.Println("FFmpeg not found in system PATH")
	}

	// Check if sox is available
	soxAvailable, soxFormats := IsSoxAvailable()
	if soxAvailable {
		settings.SoxPath = GetSoxBinaryName()
		settings.SoxAudioTypes = soxFormats
	} else {
		settings.SoxPath = ""
		log.Println("sox not found in system PATH")
	}

	// Validate audio export settings
	if settings.Export.Enabled {
		if settings.FfmpegPath == "" {
			settings.Export.Type = "wav"
			log.Printf("FFmpeg not available, using WAV format for audio export")
		} else {
			// Validate audio type and bitrate
			switch settings.Export.Type {
			case "aac", "opus", "mp3":
				if !strings.HasSuffix(settings.Export.Bitrate, "k") {
					return fmt.Errorf("invalid bitrate format for %s: %s. Must end with 'k' (e.g., '64k')", settings.Export.Type, settings.Export.Bitrate)
				}
				bitrateValue, err := strconv.Atoi(strings.TrimSuffix(settings.Export.Bitrate, "k"))
				if err != nil {
					return fmt.Errorf("invalid bitrate value for %s: %s", settings.Export.Type, settings.Export.Bitrate)
				}
				if bitrateValue < 32 || bitrateValue > 320 {
					return fmt.Errorf("bitrate for %s must be between 32k and 320k", settings.Export.Type)
				}
			case "wav", "flac":
				// These formats don't use bitrate, so we'll ignore the bitrate setting
			default:
				return fmt.Errorf("unsupported audio export type: %s", settings.Export.Type)
			}
		}
	}

	return nil
}

// Add this new function
func validateDashboardSettings(settings *Dashboard) error {
	// Validate SummaryLimit
	if settings.SummaryLimit < 10 || settings.SummaryLimit > 1000 {
		return fmt.Errorf("Dashboard SummaryLimit must be between 10 and 1000")
	}

	return nil
}

// validateWeatherSettings validates weather-specific settings
func validateWeatherSettings(settings *WeatherSettings) error {
	// Validate poll interval (minimum 15 minutes)
	if settings.PollInterval < 15 {
		return fmt.Errorf("weather poll interval must be at least 15 minutes, got %d", settings.PollInterval)
	}
	return nil
}

// validateBackupConfig validates the backup configuration
func validateBackupConfig(config *BackupConfig) error {
	var errs []string

	// Validate schedules
	if len(config.Schedules) == 0 {
		errs = append(errs, "at least one backup schedule must be configured when backup is enabled")
	}

	// Validate each schedule
	for i, schedule := range config.Schedules {
		// Validate hour
		if schedule.Hour < 0 || schedule.Hour > 23 {
			errs = append(errs, fmt.Sprintf("schedule %d: hour must be between 0 and 23", i+1))
		}

		// Validate minute
		if schedule.Minute < 0 || schedule.Minute > 59 {
			errs = append(errs, fmt.Sprintf("schedule %d: minute must be between 0 and 59", i+1))
		}

		// Validate weekday for weekly backups
		if schedule.IsWeekly {
			if schedule.Weekday == "" {
				errs = append(errs, fmt.Sprintf("schedule %d: weekday must be specified for weekly backups", i+1))
			} else {
				_, err := ParseWeekday(schedule.Weekday)
				if err != nil {
					errs = append(errs, fmt.Sprintf("schedule %d: %v", i+1, err))
				}
			}
		}
	}

	// Validate retention settings
	if config.Retention.MaxBackups < config.Retention.MinBackups {
		errs = append(errs, "maxbackups must be greater than or equal to minbackups")
	}

	if config.Retention.MinBackups < 1 {
		errs = append(errs, "minbackups must be at least 1")
	}

	// Validate maxage format (e.g., "30d", "6m", "1y")
	if config.Retention.MaxAge != "" {
		if !regexp.MustCompile(`^\d+[dmy]$`).MatchString(config.Retention.MaxAge) {
			errs = append(errs, "maxage must be in format: <number>[d|m|y] (e.g., 30d, 6m, 1y)")
		}
	}

	// Validate targets
	if len(config.Targets) == 0 {
		errs = append(errs, "at least one backup target must be configured when backup is enabled")
	}

	for i, target := range config.Targets {
		if target.Type == "" {
			errs = append(errs, fmt.Sprintf("target %d: type must not be empty", i+1))
		}

		// Validate target-specific settings based on type
		switch target.Type {
		case "local":
			if path, ok := target.Settings["path"].(string); !ok || path == "" {
				errs = append(errs, fmt.Sprintf("target %d: local target requires 'path' setting", i+1))
			}
		case "ftp":
			if _, ok := target.Settings["host"].(string); !ok {
				errs = append(errs, fmt.Sprintf("target %d: FTP target requires 'host' setting", i+1))
			}
		case "sftp":
			if _, ok := target.Settings["host"].(string); !ok {
				errs = append(errs, fmt.Sprintf("target %d: SFTP target requires 'host' setting", i+1))
			}
		case "rsync":
			if _, ok := target.Settings["host"].(string); !ok {
				errs = append(errs, fmt.Sprintf("target %d: Rsync target requires 'host' setting", i+1))
			}
		case "gdrive":
			if _, ok := target.Settings["bucket"].(string); !ok {
				errs = append(errs, fmt.Sprintf("target %d: Google Drive target requires 'bucket' setting", i+1))
			}
			if _, ok := target.Settings["credentials"].(string); !ok {
				errs = append(errs, fmt.Sprintf("target %d: Google Drive target requires 'credentials' setting", i+1))
			}
		default:
			errs = append(errs, fmt.Sprintf("target %d: unsupported backup target type: %s", i+1, target.Type))
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("backup configuration validation failed: %s", strings.Join(errs, "; "))
	}

	return nil
}
