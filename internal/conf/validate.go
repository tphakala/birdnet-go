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
	"text/template"

	"github.com/tphakala/birdnet-go/internal/errors"
)

// MinSoundLevelInterval is the minimum sound level interval in seconds to prevent excessive CPU usage
const MinSoundLevelInterval = 5

// DefaultCleanupCheckInterval is the default disk cleanup check interval in minutes
const DefaultCleanupCheckInterval = 15

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

// ValidationResult captures validation outcomes without side effects.
// Used by pure validation functions to return validation state, errors, warnings,
// and normalized/transformed configuration.
type ValidationResult struct {
	Valid      bool     // Overall validation result
	Errors     []string // Validation errors (fatal)
	Warnings   []string // Non-fatal warnings
	Normalized any      // Normalized/transformed config (type matches input)
}

// ValidateBirdNETSettings performs BirdNET validation without side effects.
// Returns normalized settings and any errors/warnings.
// This pure function enables testing without log output or settings mutation.
//
// The private validateBirdNETSettings() calls this and handles side effects.
func ValidateBirdNETSettings(cfg *BirdNETConfig) ValidationResult {
	result := ValidationResult{Valid: true, Warnings: []string{}}
	normalized := *cfg

	// Sensitivity range check
	if cfg.Sensitivity < 0 || cfg.Sensitivity > 1.5 {
		result.Valid = false
		result.Errors = append(result.Errors, "BirdNET sensitivity must be between 0 and 1.5")
	}

	// Threshold range check
	if cfg.Threshold < 0 || cfg.Threshold > 1 {
		result.Valid = false
		result.Errors = append(result.Errors, "BirdNET threshold must be between 0 and 1")
	}

	// Overlap range check
	if cfg.Overlap < 0 || cfg.Overlap > 2.99 {
		result.Valid = false
		result.Errors = append(result.Errors, "BirdNET overlap value must be between 0 and 2.99 seconds")
	}

	// Longitude range check
	if cfg.Longitude < -180 || cfg.Longitude > 180 {
		result.Valid = false
		result.Errors = append(result.Errors, "BirdNET longitude must be between -180 and 180")
	}

	// Latitude range check
	if cfg.Latitude < -90 || cfg.Latitude > 90 {
		result.Valid = false
		result.Errors = append(result.Errors, "BirdNET latitude must be between -90 and 90")
	}

	// Threads check
	if cfg.Threads < 0 {
		result.Valid = false
		result.Errors = append(result.Errors, "BirdNET threads must be at least 0")
	}

	// RangeFilter model check
	if cfg.RangeFilter.Model == "" {
		result.Valid = false
		result.Errors = append(result.Errors, "RangeFilter model must not be empty")
	}

	// RangeFilter threshold check
	if cfg.RangeFilter.Threshold < 0 || cfg.RangeFilter.Threshold > 1 {
		result.Valid = false
		result.Errors = append(result.Errors, "RangeFilter threshold must be between 0 and 1")
	}

	// Locale validation and normalization (pure transformation)
	if cfg.Locale != "" {
		normalizedLocale, err := NormalizeLocale(cfg.Locale)
		if err != nil {
			// Locale normalization fell back to default - this is a warning, not an error
			message := fmt.Sprintf("BirdNET locale '%s' is not supported, will use fallback '%s'", cfg.Locale, normalizedLocale)
			result.Warnings = append(result.Warnings, message)
		}
		// Update the normalized locale
		normalized.Locale = normalizedLocale
	}

	result.Normalized = &normalized
	return result
}

// ValidateBirdweatherSettings performs Birdweather validation without side effects.
// Returns validation result with normalized settings.
func ValidateBirdweatherSettings(settings *BirdweatherSettings) ValidationResult {
	result := ValidationResult{Valid: true, Warnings: []string{}}
	normalized := *settings

	if settings.Enabled {
		// Check if ID is provided when enabled
		if settings.ID == "" {
			result.Valid = false
			result.Errors = append(result.Errors, "Birdweather ID is required when enabled")
			// Suggest disabling
			normalized.Enabled = false
			result.Warnings = append(result.Warnings, "Birdweather will be disabled due to missing ID")
		} else {
			// Validate Birdweather ID format
			validIDPattern := regexp.MustCompile("^[a-zA-Z0-9]{24}$")
			if !validIDPattern.MatchString(settings.ID) {
				result.Valid = false
				result.Errors = append(result.Errors, "Invalid Birdweather ID format: must be 24 alphanumeric characters")
				normalized.Enabled = false
				result.Warnings = append(result.Warnings, "Birdweather will be disabled due to invalid ID format")
			}
		}

		// Check threshold range
		if settings.Threshold < 0 || settings.Threshold > 1 {
			result.Valid = false
			result.Errors = append(result.Errors, "birdweather threshold must be between 0 and 1")
		}

		// Check location accuracy
		if settings.LocationAccuracy < 0 {
			result.Valid = false
			result.Errors = append(result.Errors, "birdweather location accuracy must be non-negative")
		}
	}

	result.Normalized = &normalized
	return result
}

// ValidateWebhookProvider performs webhook provider validation without side effects.
// Returns validation result with errors.
func ValidateWebhookProvider(p *PushProviderConfig) ValidationResult {
	result := ValidationResult{Valid: true}

	if !p.Enabled {
		result.Normalized = p
		return result
	}

	// Webhook requires at least one endpoint
	if len(p.Endpoints) == 0 {
		result.Valid = false
		result.Errors = append(result.Errors,
			fmt.Sprintf("webhook provider '%s' requires at least one endpoint when enabled", p.Name))
		result.Normalized = p
		return result
	}

	// Validate custom template if specified
	if p.Template != "" {
		if _, err := template.New("validation").Parse(p.Template); err != nil {
			result.Valid = false
			result.Errors = append(result.Errors,
				fmt.Sprintf("webhook provider '%s': invalid template syntax: %v", p.Name, err))
		}
	}

	// Validate each endpoint
	for i := range p.Endpoints {
		endpoint := &p.Endpoints[i]

		// URL is required
		if strings.TrimSpace(endpoint.URL) == "" {
			result.Valid = false
			result.Errors = append(result.Errors,
				fmt.Sprintf("webhook provider '%s' endpoint %d: URL is required", p.Name, i))
			continue
		}

		// URL must start with http:// or https://
		if !strings.HasPrefix(endpoint.URL, "http://") && !strings.HasPrefix(endpoint.URL, "https://") {
			result.Valid = false
			result.Errors = append(result.Errors,
				fmt.Sprintf("webhook provider '%s' endpoint %d: URL must start with http:// or https://", p.Name, i))
		}

		// Validate HTTP method if specified
		if endpoint.Method != "" {
			method := strings.ToUpper(endpoint.Method)
			if method != "POST" && method != "PUT" && method != "PATCH" {
				result.Valid = false
				result.Errors = append(result.Errors,
					fmt.Sprintf("webhook provider '%s' endpoint %d: method must be POST, PUT, or PATCH, got %s", p.Name, i, endpoint.Method))
			}
		}

		// Validate timeout
		if endpoint.Timeout < 0 {
			result.Valid = false
			result.Errors = append(result.Errors,
				fmt.Sprintf("webhook provider '%s' endpoint %d: timeout must be non-negative", p.Name, i))
		}
	}

	result.Normalized = p
	return result
}

// ValidateMQTTSettings performs MQTT validation without side effects.
// Returns validation result with errors.
func ValidateMQTTSettings(settings *MQTTSettings) ValidationResult {
	result := ValidationResult{Valid: true}

	if !settings.Enabled {
		result.Normalized = settings
		return result
	}

	// Check if broker is provided when enabled
	if settings.Broker == "" {
		result.Valid = false
		result.Errors = append(result.Errors, "MQTT broker URL is required when MQTT is enabled")
	}

	// Check if topic is provided when enabled
	if settings.Topic == "" {
		result.Valid = false
		result.Errors = append(result.Errors, "MQTT topic is required when MQTT is enabled")
	}

	// Validate retry settings if enabled
	if settings.RetrySettings.Enabled {
		if settings.RetrySettings.MaxRetries < 0 {
			result.Valid = false
			result.Errors = append(result.Errors, "MQTT max retries must be non-negative")
		}
		if settings.RetrySettings.InitialDelay < 0 {
			result.Valid = false
			result.Errors = append(result.Errors, "MQTT initial delay must be non-negative")
		}
		if settings.RetrySettings.MaxDelay < settings.RetrySettings.InitialDelay {
			result.Valid = false
			result.Errors = append(result.Errors, "MQTT max delay must be greater than or equal to initial delay")
		}
		if settings.RetrySettings.BackoffMultiplier <= 0 {
			result.Valid = false
			result.Errors = append(result.Errors, "MQTT backoff multiplier must be positive")
		}
	}

	result.Normalized = settings
	return result
}

// ValidateWebServerSettings performs WebServer validation without side effects.
// Returns validation result with errors.
func ValidateWebServerSettings(settings *WebServerSettings) ValidationResult {
	result := ValidationResult{Valid: true}

	if settings.Enabled {
		// Check if port is provided when enabled
		if settings.Port == "" {
			result.Valid = false
			result.Errors = append(result.Errors, "WebServer port is required when enabled")
		}
	}

	// Validate LiveStream settings
	if settings.LiveStream.BitRate < 16 || settings.LiveStream.BitRate > 320 {
		result.Valid = false
		result.Errors = append(result.Errors,
			fmt.Sprintf("LiveStream bitrate must be between 16 and 320 kbps, got %d", settings.LiveStream.BitRate))
	}

	if settings.LiveStream.SegmentLength < 1 || settings.LiveStream.SegmentLength > 30 {
		result.Valid = false
		result.Errors = append(result.Errors,
			fmt.Sprintf("LiveStream segment length must be between 1 and 30 seconds, got %d", settings.LiveStream.SegmentLength))
	}

	result.Normalized = settings
	return result
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

	// Validate Notification settings
	if err := validateNotificationSettings(&settings.Notification); err != nil {
		ve.Errors = append(ve.Errors, err.Error())
	}

	// If there are any errors, return the ValidationError
	if len(ve.Errors) > 0 {
		return ve
	}
	return nil
}

// validateBirdNETSettings validates the BirdNET-specific settings.
// This function uses ValidateBirdNETSettings internally and handles side effects
// (logging, mutation) to maintain backward compatibility.
func validateBirdNETSettings(birdnetSettings *BirdNETConfig, settings *Settings) error {
	// Call the pure validation function
	result := ValidateBirdNETSettings(birdnetSettings)

	// Apply normalized configuration (side effect: mutation)
	if normalized, ok := result.Normalized.(*BirdNETConfig); ok {
		*birdnetSettings = *normalized
	}

	// Handle warnings (side effects: logging + storing in settings)
	for _, warning := range result.Warnings {
		log.Printf("Configuration warning: %s", warning)

		// Store the validation warning for telemetry reporting
		if settings.ValidationWarnings == nil {
			settings.ValidationWarnings = make([]string, 0)
		}
		settings.ValidationWarnings = append(settings.ValidationWarnings,
			fmt.Sprintf("config-locale-validation: %s", warning))
	}

	// Return errors if validation failed
	if !result.Valid {
		return errors.New(fmt.Errorf("birdnet settings errors: %v", result.Errors)).
			Category(errors.CategoryValidation).
			Context("validation_type", "birdnet-settings-collection").
			Build()
	}

	return nil
}

// validateWebServerSettings validates the WebServer-specific settings.
// This function uses ValidateWebServerSettings internally and handles error formatting
// to maintain backward compatibility.
func validateWebServerSettings(settings *WebServerSettings) error {
	// Call the pure validation function
	result := ValidateWebServerSettings(settings)

	// Return errors if validation failed
	if !result.Valid {
		// Format first error with enhanced error for backward compatibility
		firstError := result.Errors[0]
		return errors.New(fmt.Errorf("%s", firstError)).
			Category(errors.CategoryValidation).
			Context("validation_type", "webserver-settings").
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

// validateMQTTSettings validates the MQTT-specific settings.
// This function uses ValidateMQTTSettings internally and handles error formatting
// to maintain backward compatibility.
func validateMQTTSettings(settings *MQTTSettings) error {
	// Call the pure validation function
	result := ValidateMQTTSettings(settings)

	// Return errors if validation failed
	if !result.Valid {
		// Format first error with enhanced error for backward compatibility
		firstError := result.Errors[0]
		return errors.New(fmt.Errorf("%s", firstError)).
			Category(errors.CategoryValidation).
			Context("validation_type", "mqtt-settings").
			Build()
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

// validateBirdweatherSettings validates the Birdweather-specific settings.
// This function uses ValidateBirdweatherSettings internally and handles side effects
// (logging, mutation) to maintain backward compatibility.
func validateBirdweatherSettings(settings *BirdweatherSettings) error {
	// Call the pure validation function
	result := ValidateBirdweatherSettings(settings)

	// Apply normalized configuration (side effect: mutation)
	if normalized, ok := result.Normalized.(*BirdweatherSettings); ok {
		*settings = *normalized
	}

	// Handle warnings (side effect: logging)
	for _, warning := range result.Warnings {
		log.Println(warning)
	}

	// Return errors if validation failed
	if !result.Valid {
		// Join errors for backward compatibility
		return errors.New(fmt.Errorf("%s", strings.Join(result.Errors, "; "))).
			Category(errors.CategoryValidation).
			Context("validation_type", "birdweather-settings").
			Build()
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

	// Validate spectrogram settings
	if settings.Spectrogram.Mode != "" {
		validModes := []string{"auto", "prerender", "user-requested"}
		isValid := false
		for _, validMode := range validModes {
			if settings.Spectrogram.Mode == validMode {
				isValid = true
				break
			}
		}
		if !isValid {
			// Log warning but don't fail - GetMode() will handle fallback
			log.Printf("WARNING: Invalid spectrogram mode '%s', valid modes are: auto, prerender, user-requested. Using GetMode() fallback.", settings.Spectrogram.Mode)
		}
	}

	// Log the effective spectrogram mode at startup for troubleshooting
	effectiveMode := settings.Spectrogram.GetMode()
	log.Printf("Spectrogram configuration: enabled=%v, mode='%s', effective_mode='%s', size='%s', raw=%v",
		settings.Spectrogram.Enabled, settings.Spectrogram.Mode, effectiveMode,
		settings.Spectrogram.Size, settings.Spectrogram.Raw)

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

// validateNotificationSettings validates notification push configuration
func validateNotificationSettings(n *NotificationConfig) error {
	if !n.Push.Enabled {
		return nil
	}
	// Basic sanity checks
	if n.Push.MaxRetries < 0 {
		return errors.New(fmt.Errorf("notification.push.max_retries must be >= 0")).
			Category(errors.CategoryValidation).
			Context("validation_type", "notification-push-max-retries").
			Build()
	}
	if n.Push.DefaultTimeout < 0 || n.Push.RetryDelay < 0 {
		return errors.New(fmt.Errorf("notification push durations must be non-negative")).
			Category(errors.CategoryValidation).
			Context("validation_type", "notification-push-durations").
			Build()
	}
	for i := range n.Push.Providers {
		p := &n.Push.Providers[i]
		ptype := strings.ToLower(p.Type)
		switch ptype {
		case "script":
			if p.Enabled && strings.TrimSpace(p.Command) == "" {
				return errors.New(fmt.Errorf("script provider requires command when enabled")).
					Category(errors.CategoryValidation).
					Context("validation_type", "notification-push-script-command").
					Build()
			}
		case "shoutrrr":
			if p.Enabled && len(p.URLs) == 0 {
				return errors.New(fmt.Errorf("shoutrrr provider requires at least one URL when enabled")).
					Category(errors.CategoryValidation).
					Context("validation_type", "notification-push-shoutrrr-urls").
					Build()
			}
		case "webhook":
			if err := validateWebhookProvider(p); err != nil {
				return err
			}
		default:
			return errors.New(fmt.Errorf("unknown push provider type: %s", p.Type)).
				Category(errors.CategoryValidation).
				Context("validation_type", "notification-push-provider-type").
				Build()
		}
	}
	return nil
}

// validateWebhookProvider validates webhook provider configuration.
// This function uses ValidateWebhookProvider internally and handles error formatting
// to maintain backward compatibility. Authentication validation is still performed separately
// via validateWebhookAuth due to its complexity.
func validateWebhookProvider(p *PushProviderConfig) error {
	// Call the pure validation function
	result := ValidateWebhookProvider(p)

	// Return early if basic validation failed
	if !result.Valid {
		// Format first error with enhanced error for backward compatibility
		firstError := result.Errors[0]
		return errors.New(fmt.Errorf("%s", firstError)).
			Category(errors.CategoryValidation).
			Context("validation_type", "notification-push-webhook").
			Context("provider_name", p.Name).
			Build()
	}

	// If disabled or no endpoints, no need to validate auth
	if !p.Enabled || len(p.Endpoints) == 0 {
		return nil
	}

	// Validate authentication for each endpoint
	// Note: Auth validation is kept separate as it's complex and not yet in pure version
	for i := range p.Endpoints {
		endpoint := &p.Endpoints[i]
		if err := validateWebhookAuth(&endpoint.Auth, p.Name, i); err != nil {
			return err
		}
	}

	return nil
}

// validateWebhookAuth validates webhook authentication configuration.
// Checks that required fields are provided but does NOT resolve secrets here.
// Secret resolution happens at runtime in the webhook provider.
func validateWebhookAuth(auth *WebhookAuthConfig, providerName string, endpointIndex int) error {
	authType := strings.ToLower(auth.Type)

	// Empty auth type defaults to "none" - this is valid
	if authType == "" || authType == "none" {
		return nil
	}

	switch authType {
	case "bearer":
		// At least one of token or token_file must be provided
		if strings.TrimSpace(auth.Token) == "" && strings.TrimSpace(auth.TokenFile) == "" {
			return errors.New(fmt.Errorf("webhook provider '%s' endpoint %d: bearer auth requires token or token_file", providerName, endpointIndex)).
				Category(errors.CategoryValidation).
				Context("validation_type", "notification-push-webhook-auth-bearer").
				Context("provider_name", providerName).
				Context("endpoint_index", endpointIndex).
				Build()
		}
	case "basic":
		// Check user/user_file
		if strings.TrimSpace(auth.User) == "" && strings.TrimSpace(auth.UserFile) == "" {
			return errors.New(fmt.Errorf("webhook provider '%s' endpoint %d: basic auth requires user or user_file", providerName, endpointIndex)).
				Category(errors.CategoryValidation).
				Context("validation_type", "notification-push-webhook-auth-basic").
				Context("provider_name", providerName).
				Context("endpoint_index", endpointIndex).
				Build()
		}
		// Check pass/pass_file
		if strings.TrimSpace(auth.Pass) == "" && strings.TrimSpace(auth.PassFile) == "" {
			return errors.New(fmt.Errorf("webhook provider '%s' endpoint %d: basic auth requires pass or pass_file", providerName, endpointIndex)).
				Category(errors.CategoryValidation).
				Context("validation_type", "notification-push-webhook-auth-basic").
				Context("provider_name", providerName).
				Context("endpoint_index", endpointIndex).
				Build()
		}
	case "custom":
		// Header name is always required (no file variant)
		if strings.TrimSpace(auth.Header) == "" {
			return errors.New(fmt.Errorf("webhook provider '%s' endpoint %d: custom auth requires header", providerName, endpointIndex)).
				Category(errors.CategoryValidation).
				Context("validation_type", "notification-push-webhook-auth-custom").
				Context("provider_name", providerName).
				Context("endpoint_index", endpointIndex).
				Build()
		}
		// Check value/value_file
		if strings.TrimSpace(auth.Value) == "" && strings.TrimSpace(auth.ValueFile) == "" {
			return errors.New(fmt.Errorf("webhook provider '%s' endpoint %d: custom auth requires value or value_file", providerName, endpointIndex)).
				Category(errors.CategoryValidation).
				Context("validation_type", "notification-push-webhook-auth-custom").
				Context("provider_name", providerName).
				Context("endpoint_index", endpointIndex).
				Build()
		}
	default:
		return errors.New(fmt.Errorf("webhook provider '%s' endpoint %d: unsupported auth type: %s", providerName, endpointIndex, authType)).
			Category(errors.CategoryValidation).
			Context("validation_type", "notification-push-webhook-auth-type").
			Context("provider_name", providerName).
			Context("endpoint_index", endpointIndex).
			Context("auth_type", authType).
			Build()
	}

	return nil
}
