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

// Audio gain limits in dB
const (
	MinAudioGain = -40.0 // Minimum allowed audio gain in dB
	MaxAudioGain = 40.0  // Maximum allowed audio gain in dB
)

// EBU R128 normalization limits
const (
	MinTargetLUFS    = -40.0 // Minimum target loudness in LUFS
	MaxTargetLUFS    = -10.0 // Maximum target loudness in LUFS
	MinLoudnessRange = 0.0   // Minimum loudness range in LU
	MaxLoudnessRange = 20.0  // Maximum loudness range in LU
	MinTruePeak      = -10.0 // Minimum true peak in dBTP
	MaxTruePeak      = 0.0   // Maximum true peak in dBTP
)

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
		return errors.New(fmt.Errorf("security.host must be set when using OAuth authentication providers (Google or GitHub)")).
			Category(errors.CategoryValidation).
			Context("validation_type", "security-oauth-host").
			Context("google_enabled", settings.GoogleAuth.Enabled).
			Context("github_enabled", settings.GithubAuth.Enabled).
			Build()
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

	// Validate species settings
	if err := validateSpeciesConfigSettings(&settings.Species); err != nil {
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

		// Detect FFmpeg version for runtime decisions (e.g., FFmpeg 5.x bug workarounds)
		version, major, minor := GetFfmpegVersion()
		settings.FfmpegVersion = version
		settings.FfmpegMajor = major
		settings.FfmpegMinor = minor

		if major > 0 {
			log.Printf("Detected FFmpeg version: %s (major: %d, minor: %d)", version, major, minor)
		} else {
			log.Printf("Warning: Could not detect FFmpeg version from: %s", version)
		}
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
		// Validate capture length (10-60 seconds)
		if settings.Export.Length < 10 || settings.Export.Length > 60 {
			return errors.New(fmt.Errorf("audio capture length must be between 10 and 60 seconds, got %d", settings.Export.Length)).
				Category(errors.CategoryValidation).
				Context("validation_type", "audio-export-capture-length").
				Context("capture_length", settings.Export.Length).
				Build()
		}

		// Validate pre-capture (max 1/2 of capture length)
		maxPreCapture := settings.Export.Length / 2
		if settings.Export.PreCapture < 0 || settings.Export.PreCapture > maxPreCapture {
			return errors.New(fmt.Errorf("audio pre-capture must be between 0 and %d seconds (1/2 of capture length), got %d", maxPreCapture, settings.Export.PreCapture)).
				Category(errors.CategoryValidation).
				Context("validation_type", "audio-export-precapture").
				Context("precapture", settings.Export.PreCapture).
				Context("max_precapture", maxPreCapture).
				Context("capture_length", settings.Export.Length).
				Build()
		}

		// Validate gain setting (reasonable range for audio processing)
		if settings.Export.Gain < MinAudioGain || settings.Export.Gain > MaxAudioGain {
			return errors.New(fmt.Errorf("audio gain must be between %.0f and +%.0f dB, got %.1f", MinAudioGain, MaxAudioGain, settings.Export.Gain)).
				Category(errors.CategoryValidation).
				Context("validation_type", "audio-export-gain").
				Context("gain", settings.Export.Gain).
				Context("min_gain", MinAudioGain).
				Context("max_gain", MaxAudioGain).
				Build()
		}

		// Validate normalization settings if enabled
		if settings.Export.Normalization.Enabled {
			// Validate target LUFS (reasonable range for EBU R128)
			if settings.Export.Normalization.TargetLUFS < MinTargetLUFS || settings.Export.Normalization.TargetLUFS > MaxTargetLUFS {
				return errors.New(fmt.Errorf("normalization target LUFS must be between %.0f and %.0f, got %.1f", MinTargetLUFS, MaxTargetLUFS, settings.Export.Normalization.TargetLUFS)).
					Category(errors.CategoryValidation).
					Context("validation_type", "audio-normalization-target").
					Context("target_lufs", settings.Export.Normalization.TargetLUFS).
					Context("min_target_lufs", MinTargetLUFS).
					Context("max_target_lufs", MaxTargetLUFS).
					Build()
			}

			// Validate loudness range (dynamic range)
			if settings.Export.Normalization.LoudnessRange < MinLoudnessRange || settings.Export.Normalization.LoudnessRange > MaxLoudnessRange {
				return errors.New(fmt.Errorf("normalization loudness range must be between %.0f and %.0f LU, got %.1f", MinLoudnessRange, MaxLoudnessRange, settings.Export.Normalization.LoudnessRange)).
					Category(errors.CategoryValidation).
					Context("validation_type", "audio-normalization-range").
					Context("loudness_range", settings.Export.Normalization.LoudnessRange).
					Context("min_loudness_range", MinLoudnessRange).
					Context("max_loudness_range", MaxLoudnessRange).
					Build()
			}

			// Validate true peak (headroom to prevent clipping)
			if settings.Export.Normalization.TruePeak < MinTruePeak || settings.Export.Normalization.TruePeak > MaxTruePeak {
				return errors.New(fmt.Errorf("normalization true peak must be between %.0f and %.0f dBTP, got %.1f", MinTruePeak, MaxTruePeak, settings.Export.Normalization.TruePeak)).
					Category(errors.CategoryValidation).
					Context("validation_type", "audio-normalization-peak").
					Context("true_peak", settings.Export.Normalization.TruePeak).
					Context("min_true_peak", MinTruePeak).
					Context("max_true_peak", MaxTruePeak).
					Build()
			}

			// Warn if gain is also set (normalization takes precedence)
			if settings.Export.Gain != 0 {
				log.Printf("WARNING: Both gain and normalization are configured. Normalization will take precedence, gain setting will be ignored.")
			}
		}

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

	// Validate Wunderground settings if it's the selected provider
	if settings.Provider == "wunderground" {
		if err := settings.Wunderground.ValidateWunderground(); err != nil {
			return errors.New(err).
				Category(errors.CategoryValidation).
				Context("validation_type", "wunderground-settings").
				Build()
		}
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

		// Validate notification suppression hours
		if settings.NotificationSuppressionHours < 0 || settings.NotificationSuppressionHours > 720 {
			return errors.New(fmt.Errorf("notification suppression hours must be between 0 and 720 (30 days), got %d", settings.NotificationSuppressionHours)).
				Category(errors.CategoryValidation).
				Context("validation_type", "notification-suppression-hours").
				Context("suppression_hours", settings.NotificationSuppressionHours).
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

// validateSpeciesConfigSettings validates the species-specific configuration settings
func validateSpeciesConfigSettings(settings *SpeciesSettings) error {
	// Validate each species configuration
	for speciesName, config := range settings.Config {
		// Check if interval is non-negative
		if config.Interval < 0 {
			return errors.New(fmt.Errorf("species config for '%s': interval must be non-negative, got %d", speciesName, config.Interval)).
				Category(errors.CategoryValidation).
				Context("validation_type", "species-config-interval").
				Context("species_name", speciesName).
				Context("interval", config.Interval).
				Build()
		}

		// Check if threshold is within valid range
		if config.Threshold < 0 || config.Threshold > 1 {
			return errors.New(fmt.Errorf("species config for '%s': threshold must be between 0 and 1, got %f", speciesName, config.Threshold)).
				Category(errors.CategoryValidation).
				Context("validation_type", "species-config-threshold").
				Context("species_name", speciesName).
				Context("threshold", config.Threshold).
				Build()
		}
	}
	return nil
}
