// conf/validate_realtime.go

package conf

import (
	"slices"
	"strings"

	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// Maximum realtime interval in seconds (24 hours)
const MaxRealtimeInterval = 86400

// validateRealtimeSettings validates the Realtime-specific settings
func validateRealtimeSettings(settings *RealtimeSettings) error {
	// Check if interval is positive (zero means no detections would ever be logged)
	if settings.Interval <= 0 {
		return errors.Newf("realtime interval must be positive, got %d", settings.Interval).
			Category(errors.CategoryValidation).
			Context("validation_type", "realtime-interval").
			Context("interval", settings.Interval).
			Build()
	}

	// Reject absurdly large intervals
	if settings.Interval > MaxRealtimeInterval {
		return errors.Newf("realtime interval must not exceed %d seconds (24h), got %d", MaxRealtimeInterval, settings.Interval).
			Category(errors.CategoryValidation).
			Context("validation_type", "realtime-interval").
			Context("interval", settings.Interval).
			Context("max_interval", MaxRealtimeInterval).
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

	// Apply default transport before stream validation
	settings.RTSP.ApplyStreamDefaults()

	// Validate stream configurations
	if err := settings.RTSP.ValidateStreams(); err != nil {
		return errors.New(err).
			Category(errors.CategoryValidation).
			Context("validation_type", "stream-config").
			Build()
	}

	// Validate telemetry settings
	if err := validateTelemetrySettings(&settings.Telemetry); err != nil {
		return err
	}

	// Validate dynamic threshold settings
	if err := validateDynamicThresholdSettings(&settings.DynamicThreshold); err != nil {
		return err
	}

	return nil
}

// validateSoundLevelSettings validates the SoundLevel-specific settings
func validateSoundLevelSettings(settings *SoundLevelSettings) error {
	// Sound level settings are optional, only validate if enabled
	if settings.Enabled {
		// Check if interval is at least the minimum to avoid excessive CPU usage
		if settings.Interval < MinSoundLevelInterval {
			return errors.Newf("sound level interval must be at least %d seconds to avoid excessive CPU usage, got %d", MinSoundLevelInterval, settings.Interval).
				Category(errors.CategoryValidation).
				Context("validation_type", "sound-level-interval").
				Context("interval", settings.Interval).
				Context("minimum_interval", MinSoundLevelInterval).
				Build()
		}
	}
	return nil
}

// validateRetentionSettings validates retention policy, MaxAge, and MaxUsage at startup.
// This catches invalid values early instead of failing silently at runtime when the
// disk manager first attempts cleanup.
func validateRetentionSettings(settings *RetentionSettings) error {
	// Validate MinClips regardless of policy: a negative value is never valid
	// and could persist if retention is later enabled without re-validation.
	if settings.MinClips < 0 {
		return errors.Newf("retention minClips must be non-negative, got %d", settings.MinClips).
			Category(errors.CategoryValidation).
			Context("validation_type", "retention-min-clips").
			Context("min_clips", settings.MinClips).
			Build()
	}

	// Empty policy means retention is disabled — treat as "none"
	if settings.Policy == "" {
		return nil
	}

	// Validate policy against known values
	if !slices.Contains(validRetentionPolicies, settings.Policy) {
		return errors.Newf("retention policy must be one of %v, got %q", validRetentionPolicies, settings.Policy).
			Category(errors.CategoryValidation).
			Context("validation_type", "retention-policy").
			Context("policy", settings.Policy).
			Build()
	}

	// Validate MaxAge when age-based policy is active
	if settings.Policy == RetentionPolicyAge {
		hours, err := ParseRetentionPeriod(settings.MaxAge)
		if err != nil {
			return errors.Newf("retention maxAge %q is invalid: %v", settings.MaxAge, err).
				Category(errors.CategoryValidation).
				Context("validation_type", "retention-max-age").
				Context("max_age", settings.MaxAge).
				Build()
		}
		if hours <= 0 {
			return errors.Newf("retention maxAge must be positive, got %q (%d hours)", settings.MaxAge, hours).
				Category(errors.CategoryValidation).
				Context("validation_type", "retention-max-age").
				Context("max_age", settings.MaxAge).
				Context("parsed_hours", hours).
				Build()
		}
	}

	// Validate MaxUsage when usage-based policy is active
	if settings.Policy == RetentionPolicyUsage {
		if _, err := ParsePercentage(settings.MaxUsage, "retention.maxUsage"); err != nil {
			return errors.Newf("retention maxUsage %q is invalid: %v", settings.MaxUsage, err).
				Category(errors.CategoryValidation).
				Context("validation_type", "retention-max-usage").
				Context("max_usage", settings.MaxUsage).
				Build()
		}
	}

	return nil
}

func validateDashboardSettings(settings *Dashboard) error {
	// Validate deprecated root SummaryLimit (only when non-zero, i.e. not yet migrated)
	if settings.SummaryLimit != 0 && (settings.SummaryLimit < 10 || settings.SummaryLimit > 1000) {
		GetLogger().Warn("Dashboard SummaryLimit out of range (10-1000), resetting to 10",
			logger.Int("invalid_value", settings.SummaryLimit))
		settings.SummaryLimit = 10
	}

	// Validate layout element configs
	validElementTypes := []string{"banner", "daily-summary", "currently-hearing", "detections-grid", "live-spectrogram", "video-embed"}
	validWidths := []string{"", "full", "half"}

	for i, el := range settings.Layout.Elements {
		if !slices.Contains(validElementTypes, el.Type) {
			return errors.Newf("Dashboard layout element %d has invalid type %q", i, el.Type).
				Category(errors.CategoryValidation).
				Context("validation_type", "dashboard-element-type").
				Context("element_index", i).
				Context("element_type", el.Type).
				Build()
		}

		if !slices.Contains(validWidths, el.Width) {
			return errors.Newf("Dashboard layout element %d has invalid width %q (must be \"full\" or \"half\")", i, el.Width).
				Category(errors.CategoryValidation).
				Context("validation_type", "dashboard-element-width").
				Context("element_index", i).
				Context("element_width", el.Width).
				Build()
		}

		if el.Summary != nil && el.Summary.SummaryLimit != 0 && (el.Summary.SummaryLimit < 10 || el.Summary.SummaryLimit > 1000) {
			GetLogger().Warn("Dashboard layout element SummaryLimit out of range (10-1000), resetting to 10",
				logger.Int("element_index", i),
				logger.String("element_type", el.Type),
				logger.Int("invalid_value", el.Summary.SummaryLimit))
			el.Summary.SummaryLimit = 10
			settings.Layout.Elements[i] = el
		}
	}

	// Validate UI locale if provided
	if settings.Locale != "" {
		isValid := slices.Contains(ValidUILocales(), settings.Locale)
		if !isValid {
			// Log warning but don't fail - fallback to default
			GetLogger().Warn("Invalid UI locale, will use default", logger.String("invalid_locale", settings.Locale), logger.String("fallback", "en"))
			settings.Locale = "en"
		}
	}

	// Validate default audio gain (playback UI preference)
	if settings.DefaultAudioGain < MinPlaybackGain || settings.DefaultAudioGain > MaxPlaybackGain {
		GetLogger().Warn("Dashboard DefaultAudioGain out of range, clamping to safe value",
			logger.Float64("invalid_value", settings.DefaultAudioGain),
			logger.Float64("min", MinPlaybackGain),
			logger.Float64("max", MaxPlaybackGain))
		if settings.DefaultAudioGain < MinPlaybackGain {
			settings.DefaultAudioGain = MinPlaybackGain
		} else {
			settings.DefaultAudioGain = MaxPlaybackGain
		}
	}

	// Validate spectrogram settings
	if settings.Spectrogram.Mode != "" {
		validModes := []string{"auto", "prerender", "user-requested"}
		isValid := slices.Contains(validModes, settings.Spectrogram.Mode)
		if !isValid {
			// Log warning but don't fail - GetMode() will handle fallback
			GetLogger().Warn("Invalid spectrogram mode, using GetMode() fallback",
				logger.String("invalid_mode", settings.Spectrogram.Mode),
				logger.String("valid_modes", "auto, prerender, user-requested"))
		}
	}

	// Validate spectrogram size
	if settings.Spectrogram.Size != "" {
		validSizes := []string{"sm", "md", "lg", "xl"}
		if !slices.Contains(validSizes, settings.Spectrogram.Size) {
			GetLogger().Warn("Invalid spectrogram size, using default",
				logger.String("invalid_size", settings.Spectrogram.Size),
				logger.String("valid_sizes", strings.Join(validSizes, ", ")),
				logger.String("fallback", "lg"))
			settings.Spectrogram.Size = "lg"
		}
	}

	// Validate spectrogram style
	if settings.Spectrogram.Style != "" {
		validStyles := []string{
			SpectrogramStyleDefault,
			SpectrogramStyleScientificDark,
			SpectrogramStyleHighContrastDark,
			SpectrogramStyleScientific,
		}
		if !slices.Contains(validStyles, settings.Spectrogram.Style) {
			// Log warning but don't fail - default to "default" style
			GetLogger().Warn("Invalid spectrogram style, using default",
				logger.String("invalid_style", settings.Spectrogram.Style),
				logger.String("valid_styles", strings.Join(validStyles, ", ")))
			settings.Spectrogram.Style = SpectrogramStyleDefault
		}
	}

	// Log the effective spectrogram mode at startup for troubleshooting
	effectiveMode := settings.Spectrogram.GetMode()
	GetLogger().Debug("Spectrogram configuration",
		logger.Bool("enabled", settings.Spectrogram.Enabled),
		logger.String("mode", settings.Spectrogram.Mode),
		logger.String("effective_mode", effectiveMode),
		logger.String("size", settings.Spectrogram.Size),
		logger.Bool("raw", settings.Spectrogram.Raw),
		logger.String("style", settings.Spectrogram.Style))

	return nil
}

// validWeatherProviders contains all recognized weather provider values.
var validWeatherProviders = []string{"none", "yrno", "openweather", "wunderground"}

// validateWeatherSettings validates weather-specific settings
func validateWeatherSettings(settings *WeatherSettings) error {
	// When a provider is set (not empty and not "none"), validate it
	if settings.Provider != "" && settings.Provider != "none" {
		if !slices.Contains(validWeatherProviders, settings.Provider) {
			return errors.Newf("weather provider must be one of %v, got %q", validWeatherProviders, settings.Provider).
				Category(errors.CategoryValidation).
				Context("validation_type", "weather-provider").
				Context("provider", settings.Provider).
				Build()
		}
	}

	// Validate poll interval (minimum 15 minutes)
	if settings.PollInterval < 15 {
		return errors.Newf("weather poll interval must be at least 15 minutes, got %d", settings.PollInterval).
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

// validateDynamicThresholdSettings validates dynamic threshold cross-field constraints.
func validateDynamicThresholdSettings(settings *DynamicThresholdSettings) error {
	if !settings.Enabled {
		return nil
	}

	// Trigger and Min must be valid confidence values in [0, 1]
	if settings.Trigger < 0 || settings.Trigger > 1 {
		return errors.Newf("dynamic threshold trigger must be between 0 and 1, got %f", settings.Trigger).
			Category(errors.CategoryValidation).
			Context("validation_type", "dynamic-threshold-trigger").
			Context("trigger", settings.Trigger).
			Build()
	}

	if settings.Min < 0 || settings.Min > 1 {
		return errors.Newf("dynamic threshold min must be between 0 and 1, got %f", settings.Min).
			Category(errors.CategoryValidation).
			Context("validation_type", "dynamic-threshold-min").
			Context("min", settings.Min).
			Build()
	}

	// Min must be less than or equal to Trigger
	if settings.Min > settings.Trigger {
		return errors.Newf("dynamic threshold min (%f) must not exceed trigger (%f)", settings.Min, settings.Trigger).
			Category(errors.CategoryValidation).
			Context("validation_type", "dynamic-threshold-cross-field").
			Context("min", settings.Min).
			Context("trigger", settings.Trigger).
			Build()
	}

	// ValidHours must be strictly positive when enabled
	if settings.ValidHours <= 0 {
		return errors.Newf("dynamic threshold validHours must be positive when enabled, got %d", settings.ValidHours).
			Category(errors.CategoryValidation).
			Context("validation_type", "dynamic-threshold-valid-hours").
			Context("valid_hours", settings.ValidHours).
			Build()
	}

	return nil
}

// validateSpeciesTrackingSettings validates the species tracking settings
func validateSpeciesTrackingSettings(settings *SpeciesTrackingSettings) error {
	if settings.Enabled {
		// Validate window days
		if settings.NewSpeciesWindowDays < 1 || settings.NewSpeciesWindowDays > 365 {
			return errors.Newf("species tracking window days must be between 1 and 365, got %d", settings.NewSpeciesWindowDays).
				Category(errors.CategoryValidation).
				Context("validation_type", "species-tracking-window-days").
				Context("window_days", settings.NewSpeciesWindowDays).
				Build()
		}

		// Validate sync interval
		if settings.SyncIntervalMinutes < 5 || settings.SyncIntervalMinutes > 1440 {
			return errors.Newf("species tracking sync interval must be between 5 and 1440 minutes (24 hours), got %d", settings.SyncIntervalMinutes).
				Category(errors.CategoryValidation).
				Context("validation_type", "species-tracking-sync-interval").
				Context("sync_interval", settings.SyncIntervalMinutes).
				Build()
		}

		// Validate notification suppression hours
		if settings.NotificationSuppressionHours < 0 || settings.NotificationSuppressionHours > 720 {
			return errors.Newf("notification suppression hours must be between 0 and 720 (30 days), got %d", settings.NotificationSuppressionHours).
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
			return errors.Newf("yearly tracking reset month must be between 1 and 12, got %d", settings.ResetMonth).
				Category(errors.CategoryValidation).
				Context("validation_type", "yearly-tracking-reset-month").
				Context("reset_month", settings.ResetMonth).
				Build()
		}
		// Validate reset day - must be valid for the specified month
		maxDaysInMonth := getMaxDaysInMonth(settings.ResetMonth)
		if settings.ResetDay < 1 || settings.ResetDay > maxDaysInMonth {
			return errors.Newf("yearly tracking reset day must be between 1 and %d for month %d, got %d", maxDaysInMonth, settings.ResetMonth, settings.ResetDay).
				Category(errors.CategoryValidation).
				Context("validation_type", "yearly-tracking-reset-day").
				Context("reset_month", settings.ResetMonth).
				Context("reset_day", settings.ResetDay).
				Context("max_days_in_month", maxDaysInMonth).
				Build()
		}
		// Validate window days
		if settings.WindowDays < 1 || settings.WindowDays > 365 {
			return errors.Newf("yearly tracking window days must be between 1 and 365, got %d", settings.WindowDays).
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
			return errors.Newf("seasonal tracking window days must be between 1 and 365, got %d", settings.WindowDays).
				Category(errors.CategoryValidation).
				Context("validation_type", "seasonal-tracking-window-days").
				Context("window_days", settings.WindowDays).
				Build()
		}
		// Validate seasons
		if len(settings.Seasons) == 0 {
			return errors.Newf("seasonal tracking requires at least one season to be defined").
				Category(errors.CategoryValidation).
				Context("validation_type", "seasonal-tracking-seasons").
				Build()
		}
		for seasonName, season := range settings.Seasons {
			if season.StartMonth < 1 || season.StartMonth > 12 {
				return errors.Newf("season %s start month must be between 1 and 12, got %d", seasonName, season.StartMonth).
					Category(errors.CategoryValidation).
					Context("validation_type", "seasonal-tracking-season-month").
					Context("season", seasonName).
					Context("start_month", season.StartMonth).
					Build()
			}
			maxDaysInMonth := getMaxDaysInMonth(season.StartMonth)
			if season.StartDay < 1 || season.StartDay > maxDaysInMonth {
				return errors.Newf("season %s start day must be between 1 and %d for month %d, got %d", seasonName, maxDaysInMonth, season.StartMonth, season.StartDay).
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
			return errors.Newf("species config for '%s': interval must be non-negative, got %d", speciesName, config.Interval).
				Category(errors.CategoryValidation).
				Context("validation_type", "species-config-interval").
				Context("species_name", speciesName).
				Context("interval", config.Interval).
				Build()
		}

		// Check if threshold is within valid range
		if config.Threshold < 0 || config.Threshold > 1 {
			return errors.Newf("species config for '%s': threshold must be between 0 and 1, got %f", speciesName, config.Threshold).
				Category(errors.CategoryValidation).
				Context("validation_type", "species-config-threshold").
				Context("species_name", speciesName).
				Context("threshold", config.Threshold).
				Build()
		}
	}
	return nil
}
