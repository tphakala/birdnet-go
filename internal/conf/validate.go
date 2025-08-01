// conf/validate.go

package conf

import (
	"fmt"
	"log"
	"net"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"github.com/tphakala/birdnet-go/internal/errors"
)

// MinSoundLevelInterval is the minimum sound level interval in seconds to prevent excessive CPU usage
const MinSoundLevelInterval = 5

// ValidationError represents a collection of validation errors
type ValidationError struct {
	Errors []string
}

// Error returns a string representation of the validation errors
func (ve ValidationError) Error() string {
	return fmt.Sprintf("Validation errors: %v", ve.Errors)
}

// logValidationWarning logs a validation warning for telemetry purposes without returning an error
func logValidationWarning(err error, validationType, warningType string) {
	// Create an enhanced error for telemetry tracking
	_ = errors.New(err).
		Category(errors.CategoryValidation).
		Context("validation_type", validationType).
		Context("warning", warningType).
		Build()
}

// ValidateSettings validates the entire Settings struct
func ValidateSettings(settings *Settings) error {
	ve := ValidationError{}

	// Validate BirdNET settings
	if err := validateBirdNETSettings(&settings.BirdNET, settings); err != nil {
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

	// Validate Species Tracking settings
	if err := validateSpeciesTrackingSettings(&settings.Realtime.SpeciesTracking); err != nil {
		ve.Errors = append(ve.Errors, err.Error())
	}

	// If there are any errors, return the ValidationError
	if len(ve.Errors) > 0 {
		return ve
	}
	return nil
}

// validateBirdNETSettings validates the BirdNET-specific settings
func validateBirdNETSettings(birdnetSettings *BirdNETConfig, settings *Settings) error {
	var errs []string

	// Check if sensitivity is within valid range
	if birdnetSettings.Sensitivity < 0 || birdnetSettings.Sensitivity > 1.5 {
		errs = append(errs, "BirdNET sensitivity must be between 0 and 1.5")
	}

	// Check if threshold is within valid range
	if birdnetSettings.Threshold < 0 || birdnetSettings.Threshold > 1 {
		errs = append(errs, "BirdNET threshold must be between 0 and 1")
	}

	// Check if overlap is within valid range
	if birdnetSettings.Overlap < 0 || birdnetSettings.Overlap > 2.99 {
		errs = append(errs, "BirdNET overlap value must be between 0 and 2.99 seconds")
	}

	// Check if longitude is within valid range
	if birdnetSettings.Longitude < -180 || birdnetSettings.Longitude > 180 {
		errs = append(errs, "BirdNET longitude must be between -180 and 180")
	}

	// Check if latitude is within valid range
	if birdnetSettings.Latitude < -90 || birdnetSettings.Latitude > 90 {
		errs = append(errs, "BirdNET latitude must be between -90 and 90")
	}

	// Check if threads is non-negative
	if birdnetSettings.Threads < 0 {
		errs = append(errs, "BirdNET threads must be at least 0")
	}

	// Validate RangeFilter settings
	if birdnetSettings.RangeFilter.Model == "" {
		errs = append(errs, "RangeFilter model must not be empty")
	}

	// Check if RangeFilter threshold is within valid range
	if birdnetSettings.RangeFilter.Threshold < 0 || birdnetSettings.RangeFilter.Threshold > 1 {
		errs = append(errs, "RangeFilter threshold must be between 0 and 1")
	}

	// Validate locale setting
	if birdnetSettings.Locale != "" {
		normalizedLocale, err := NormalizeLocale(birdnetSettings.Locale)
		if err != nil {
			// This means locale normalization fell back to default
			message := fmt.Sprintf("BirdNET locale '%s' is not supported, will use fallback '%s'", birdnetSettings.Locale, normalizedLocale)
			errs = append(errs, message)

			// Store the validation warning for telemetry reporting
			// We can't call telemetry directly here due to import cycles
			// This will be handled by the calling code in main.go
			if settings.ValidationWarnings == nil {
				settings.ValidationWarnings = make([]string, 0)
			}
			settings.ValidationWarnings = append(settings.ValidationWarnings,
				fmt.Sprintf("config-locale-validation: %s", message))
		}
		// Update the settings with the normalized locale
		birdnetSettings.Locale = normalizedLocale
	}

	// If there are any errors, return them as a single error
	if len(errs) > 0 {
		return errors.New(fmt.Errorf("birdnet settings errors: %v", errs)).
			Category(errors.CategoryValidation).
			Context("validation_type", "birdnet-settings-collection").
			Build()
	}

	return nil
}

// validateWebServerSettings validates the WebServer-specific settings
func validateWebServerSettings(settings *WebServerSettings) error {
	if settings.Enabled {
		// Check if port is provided when enabled
		if settings.Port == "" {
			return errors.New(fmt.Errorf("WebServer port is required when enabled")).
				Category(errors.CategoryValidation).
				Context("validation_type", "webserver-port-required").
				Build()
		}
		// You might want to add more specific port validation here
	}

	// Validate LiveStream settings
	if settings.LiveStream.BitRate < 16 || settings.LiveStream.BitRate > 320 {
		return errors.New(fmt.Errorf("LiveStream bitrate must be between 16 and 320 kbps, got %d", settings.LiveStream.BitRate)).
			Category(errors.CategoryValidation).
			Context("validation_type", "livestream-bitrate").
			Context("bitrate", settings.LiveStream.BitRate).
			Build()
	}

	if settings.LiveStream.SegmentLength < 1 || settings.LiveStream.SegmentLength > 30 {
		return errors.New(fmt.Errorf("LiveStream segment length must be between 1 and 30 seconds, got %d", settings.LiveStream.SegmentLength)).
			Category(errors.CategoryValidation).
			Context("validation_type", "livestream-segment-length").
			Context("segment_length", settings.LiveStream.SegmentLength).
			Build()
	}

	return nil
}

// validateSecuritySettings validates the security-specific settings
func validateSecuritySettings(settings *Security) error {
	// Check if any OAuth provider is enabled (OAuth providers require host for redirect URLs)
	// Note: BasicAuth doesn't require host as it doesn't use OAuth redirects
	if (settings.GoogleAuth.Enabled || settings.GithubAuth.Enabled) && settings.Host == "" {
		// Log warning but don't fail startup - users can fix this in web UI settings
		logValidationWarning(
			fmt.Errorf("security.host must be set when using OAuth authentication providers (Google or GitHub)"),
			"security-oauth-host",
			"oauth-missing-host")
		
		// Store this as a warning to be shown in notifications after startup
		// This is handled by the calling code which will add it to settings.ValidationWarnings
		return &ValidationError{
			Errors: []string{
				"OAuth authentication warning: security.host must be set when using Google or GitHub authentication. Please configure this in Settings > Security.",
			},
		}
	}

	// AutoTLS validation
	if settings.AutoTLS {
		// Host is required for AutoTLS
		if settings.Host == "" {
			return errors.New(fmt.Errorf("security.host must be set when AutoTLS is enabled")).
				Category(errors.CategoryValidation).
				Context("validation_type", "security-autotls-host").
				Build()
		}

		// Warning about port requirements when running in container
		if RunningInContainer() {
			log.Println("WARNING: AutoTLS requires ports 80 and 443 to be exposed.")
			log.Println("Ensure your Docker configuration maps these ports:")
			log.Println("  ports:")
			log.Println("    - \"80:80\"    # Required for ACME HTTP-01 challenge")
			log.Println("    - \"443:443\"  # Required for HTTPS/AutoTLS")
			log.Println("Consider using docker-compose.autotls.yml for proper AutoTLS configuration.")
		}
	}

	// Validate the subnet bypass setting against the allowed pattern
	if settings.AllowSubnetBypass.Enabled {
		subnets := strings.Split(settings.AllowSubnetBypass.Subnet, ",")
		for _, subnet := range subnets {
			_, _, err := net.ParseCIDR(strings.TrimSpace(subnet))
			if err != nil {
				return errors.New(err).
					Category(errors.CategoryValidation).
					Context("validation_type", "security-subnet-format").
					Context("subnet", subnet).
					Build()
			}
		}
	}

	// Validate session duration
	if settings.SessionDuration <= 0 {
		return errors.New(fmt.Errorf("security.sessionduration must be a positive duration")).
			Category(errors.CategoryValidation).
			Context("validation_type", "security-session-duration").
			Build()
	}

	return nil
}

// validateRealtimeSettings validates the Realtime-specific settings
func validateRealtimeSettings(settings *RealtimeSettings) error {
	// Check if interval is non-negative
	if settings.Interval < 0 {
		return errors.New(fmt.Errorf("realtime interval must be non-negative")).
			Category(errors.CategoryValidation).
			Context("validation_type", "realtime-interval").
			Build()
	}

	// Validate MQTT settings
	if err := validateMQTTSettings(&settings.MQTT); err != nil {
		return err
	}

	// Validate sound level settings
	if err := validateSoundLevelSettings(&settings.Audio.SoundLevel); err != nil {
		return err
	}

	// Add more realtime settings validation as needed
	return nil
}

// validateMQTTSettings validates the MQTT-specific settings
func validateMQTTSettings(settings *MQTTSettings) error {
	if settings.Enabled {
		// Check if broker is provided when enabled
		if settings.Broker == "" {
			return errors.New(fmt.Errorf("MQTT broker URL is required when MQTT is enabled")).
				Category(errors.CategoryValidation).
				Context("validation_type", "mqtt-broker-required").
				Build()
		}

		// Check if topic is provided when enabled
		if settings.Topic == "" {
			return errors.New(fmt.Errorf("MQTT topic is required when MQTT is enabled")).
				Category(errors.CategoryValidation).
				Context("validation_type", "mqtt-topic-required").
				Build()
		}

		// Explicitly support anonymous connections (empty username and password)
		// No validation required for username/password - they can be empty for anonymous connections

		// Validate retry settings if enabled
		if settings.RetrySettings.Enabled {
			if settings.RetrySettings.MaxRetries < 0 {
				return errors.New(fmt.Errorf("MQTT max retries must be non-negative")).
					Category(errors.CategoryValidation).
					Context("validation_type", "mqtt-max-retries").
					Build()
			}
			if settings.RetrySettings.InitialDelay < 0 {
				return errors.New(fmt.Errorf("MQTT initial delay must be non-negative")).
					Category(errors.CategoryValidation).
					Context("validation_type", "mqtt-initial-delay").
					Build()
			}
			if settings.RetrySettings.MaxDelay < settings.RetrySettings.InitialDelay {
				return errors.New(fmt.Errorf("MQTT max delay must be greater than or equal to initial delay")).
					Category(errors.CategoryValidation).
					Context("validation_type", "mqtt-max-delay").
					Build()
			}
			if settings.RetrySettings.BackoffMultiplier <= 0 {
				return errors.New(fmt.Errorf("MQTT backoff multiplier must be positive")).
					Category(errors.CategoryValidation).
					Context("validation_type", "mqtt-backoff-multiplier").
					Build()
			}
		}
	}
	return nil
}

// validateSoundLevelSettings validates the SoundLevel-specific settings
func validateSoundLevelSettings(settings *SoundLevelSettings) error {
	// Sound level settings are optional, only validate if enabled
	if settings.Enabled {
		// Check if interval is at least the minimum to avoid excessive CPU usage
		if settings.Interval < MinSoundLevelInterval {
			return errors.New(fmt.Errorf("sound level interval must be at least %d seconds to avoid excessive CPU usage, got %d", MinSoundLevelInterval, settings.Interval)).
				Category(errors.CategoryValidation).
				Context("validation_type", "sound-level-interval").
				Context("interval", settings.Interval).
				Context("minimum_interval", MinSoundLevelInterval).
				Build()
		}
	}
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
			return errors.New(fmt.Errorf("birdweather threshold must be between 0 and 1")).
				Category(errors.CategoryValidation).
				Context("validation_type", "birdweather-threshold").
				Build()
		}

		// Check if location accuracy is non-negative
		if settings.LocationAccuracy < 0 {
			return errors.New(fmt.Errorf("birdweather location accuracy must be non-negative")).
				Category(errors.CategoryValidation).
				Context("validation_type", "birdweather-location-accuracy").
				Build()
		}
	}
	return nil
}

// validateAudioSettings validates the audio settings and sets ffmpeg and sox paths
func validateAudioSettings(settings *AudioSettings) error {
	// Validate and determine the effective FFmpeg path
	validatedFfmpegPath, ffmpegErr := ValidateToolPath(settings.FfmpegPath, GetFfmpegBinaryName())
	if ffmpegErr != nil {
		log.Printf("FFmpeg validation failed: %v. Audio export/conversion requiring FFmpeg might be disabled or use defaults.", ffmpegErr)
		// Log validation warning for telemetry
		logValidationWarning(ffmpegErr, "audio-tool-ffmpeg", "ffmpeg-not-available")
		settings.FfmpegPath = "" // Ensure path is empty if validation failed
	} else {
		settings.FfmpegPath = validatedFfmpegPath // Store the validated path (explicit or from PATH)
	}

	// Validate and determine the effective SoX path
	// We only need to know if it's available and its formats, so LookPath is sufficient here.
	soxPath, soxLookPathErr := exec.LookPath(GetSoxBinaryName())
	if soxLookPathErr != nil {
		settings.SoxPath = ""
		settings.SoxAudioTypes = nil
		log.Println("SoX not found in system PATH. Audio source processing requiring SoX might be disabled.")
	} else {
		settings.SoxPath = soxPath
		// Get supported formats if SoX is found
		_, formats := IsSoxAvailable() // We already know it's available from LookPath
		settings.SoxAudioTypes = formats
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
					return errors.New(fmt.Errorf("invalid bitrate format for %s: %s. Must end with 'k' (e.g., '64k')", settings.Export.Type, settings.Export.Bitrate)).
						Category(errors.CategoryValidation).
						Context("validation_type", "audio-export-bitrate-format").
						Context("export_type", settings.Export.Type).
						Context("bitrate", settings.Export.Bitrate).
						Build()
				}
				bitrateValue, err := strconv.Atoi(strings.TrimSuffix(settings.Export.Bitrate, "k"))
				if err != nil {
					return errors.New(fmt.Errorf("invalid bitrate value for %s: %s", settings.Export.Type, settings.Export.Bitrate)).
						Category(errors.CategoryValidation).
						Context("validation_type", "audio-export-bitrate-value").
						Context("export_type", settings.Export.Type).
						Context("bitrate", settings.Export.Bitrate).
						Build()
				}
				if bitrateValue < 32 || bitrateValue > 320 {
					return errors.New(fmt.Errorf("bitrate for %s must be between 32k and 320k", settings.Export.Type)).
						Category(errors.CategoryValidation).
						Context("validation_type", "audio-export-bitrate-range").
						Context("export_type", settings.Export.Type).
						Build()
				}
			case "wav", "flac":
				// These formats don't use bitrate, so we'll ignore the bitrate setting
			default:
				return errors.New(fmt.Errorf("unsupported audio export type: %s", settings.Export.Type)).
					Category(errors.CategoryValidation).
					Context("validation_type", "audio-export-type").
					Context("export_type", settings.Export.Type).
					Build()
			}
		}
	}

	return nil
}

// Add this new function
func validateDashboardSettings(settings *Dashboard) error {
	// Validate SummaryLimit
	if settings.SummaryLimit < 10 || settings.SummaryLimit > 1000 {
		return errors.New(fmt.Errorf("Dashboard SummaryLimit must be between 10 and 1000")).
			Category(errors.CategoryValidation).
			Context("validation_type", "dashboard-summary-limit").
			Context("summary_limit", settings.SummaryLimit).
			Build()
	}

	// Validate UI locale if provided
	if settings.Locale != "" {
		validLocales := []string{"en", "de", "fr", "es", "fi", "pt"}
		isValid := false
		for _, validLocale := range validLocales {
			if settings.Locale == validLocale {
				isValid = true
				break
			}
		}
		if !isValid {
			// Log warning but don't fail - fallback to default
			log.Printf("WARNING: Invalid UI locale '%s', will use default 'en'", settings.Locale)
			settings.Locale = "en"
		}
	}

	return nil
}

// validateWeatherSettings validates weather-specific settings
func validateWeatherSettings(settings *WeatherSettings) error {
	// Validate poll interval (minimum 15 minutes)
	if settings.PollInterval < 15 {
		return errors.New(fmt.Errorf("weather poll interval must be at least 15 minutes, got %d", settings.PollInterval)).
			Category(errors.CategoryValidation).
			Context("validation_type", "weather-poll-interval").
			Context("poll_interval", settings.PollInterval).
			Build()
	}
	return nil
}

// validateSpeciesTrackingSettings validates the species tracking settings
func validateSpeciesTrackingSettings(settings *SpeciesTrackingSettings) error {
	if settings.Enabled {
		// Validate window days
		if settings.NewSpeciesWindowDays < 1 || settings.NewSpeciesWindowDays > 365 {
			return errors.New(fmt.Errorf("species tracking window days must be between 1 and 365, got %d", settings.NewSpeciesWindowDays)).
				Category(errors.CategoryValidation).
				Context("validation_type", "species-tracking-window-days").
				Context("window_days", settings.NewSpeciesWindowDays).
				Build()
		}

		// Validate sync interval
		if settings.SyncIntervalMinutes < 5 || settings.SyncIntervalMinutes > 1440 {
			return errors.New(fmt.Errorf("species tracking sync interval must be between 5 and 1440 minutes (24 hours), got %d", settings.SyncIntervalMinutes)).
				Category(errors.CategoryValidation).
				Context("validation_type", "species-tracking-sync-interval").
				Context("sync_interval", settings.SyncIntervalMinutes).
				Build()
		}

		// Validate yearly tracking settings
		if err := validateYearlyTrackingSettings(&settings.YearlyTracking); err != nil {
			return err
		}

		// Validate seasonal tracking settings
		if err := validateSeasonalTrackingSettings(&settings.SeasonalTracking); err != nil {
			return err
		}
	}
	return nil
}

func validateYearlyTrackingSettings(settings *YearlyTrackingSettings) error {
	if settings.Enabled {
		// Validate reset month
		if settings.ResetMonth < 1 || settings.ResetMonth > 12 {
			return errors.New(fmt.Errorf("yearly tracking reset month must be between 1 and 12, got %d", settings.ResetMonth)).
				Category(errors.CategoryValidation).
				Context("validation_type", "yearly-tracking-reset-month").
				Context("reset_month", settings.ResetMonth).
				Build()
		}
		// Validate reset day - must be valid for the specified month
		maxDaysInMonth := getMaxDaysInMonth(settings.ResetMonth)
		if settings.ResetDay < 1 || settings.ResetDay > maxDaysInMonth {
			return errors.New(fmt.Errorf("yearly tracking reset day must be between 1 and %d for month %d, got %d", maxDaysInMonth, settings.ResetMonth, settings.ResetDay)).
				Category(errors.CategoryValidation).
				Context("validation_type", "yearly-tracking-reset-day").
				Context("reset_month", settings.ResetMonth).
				Context("reset_day", settings.ResetDay).
				Context("max_days_in_month", maxDaysInMonth).
				Build()
		}
		// Validate window days
		if settings.WindowDays < 1 || settings.WindowDays > 365 {
			return errors.New(fmt.Errorf("yearly tracking window days must be between 1 and 365, got %d", settings.WindowDays)).
				Category(errors.CategoryValidation).
				Context("validation_type", "yearly-tracking-window-days").
				Context("window_days", settings.WindowDays).
				Build()
		}
	}
	return nil
}

func validateSeasonalTrackingSettings(settings *SeasonalTrackingSettings) error {
	if settings.Enabled {
		// Validate window days
		if settings.WindowDays < 1 || settings.WindowDays > 365 {
			return errors.New(fmt.Errorf("seasonal tracking window days must be between 1 and 365, got %d", settings.WindowDays)).
				Category(errors.CategoryValidation).
				Context("validation_type", "seasonal-tracking-window-days").
				Context("window_days", settings.WindowDays).
				Build()
		}
		// Validate seasons
		if len(settings.Seasons) == 0 {
			return errors.New(fmt.Errorf("seasonal tracking requires at least one season to be defined")).
				Category(errors.CategoryValidation).
				Context("validation_type", "seasonal-tracking-seasons").
				Build()
		}
		for seasonName, season := range settings.Seasons {
			if season.StartMonth < 1 || season.StartMonth > 12 {
				return errors.New(fmt.Errorf("season %s start month must be between 1 and 12, got %d", seasonName, season.StartMonth)).
					Category(errors.CategoryValidation).
					Context("validation_type", "seasonal-tracking-season-month").
					Context("season", seasonName).
					Context("start_month", season.StartMonth).
					Build()
			}
			maxDaysInMonth := getMaxDaysInMonth(season.StartMonth)
			if season.StartDay < 1 || season.StartDay > maxDaysInMonth {
				return errors.New(fmt.Errorf("season %s start day must be between 1 and %d for month %d, got %d", seasonName, maxDaysInMonth, season.StartMonth, season.StartDay)).
					Category(errors.CategoryValidation).
					Context("validation_type", "seasonal-tracking-season-day").
					Context("season", seasonName).
					Context("start_month", season.StartMonth).
					Context("start_day", season.StartDay).
					Context("max_days_in_month", maxDaysInMonth).
					Build()
			}
		}
	}
	return nil
}

// getMaxDaysInMonth returns the maximum number of days for a given month (1-12)
func getMaxDaysInMonth(month int) int {
	switch month {
	case 2: // February
		return 29 // Return 29 to safely accommodate leap years, ensuring validation doesn't reject valid Feb 29 dates
	case 4, 6, 9, 11: // April, June, September, November
		return 30
	default: // January, March, May, July, August, October, December
		return 31
	}
}
