// conf/validate.go

package conf

import (
	"errors"
	"fmt"
)

// ValidationError represents a collection of validation errors
type ValidationError struct {
	Errors []string
}

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

	// Validate OpenWeather settings
	if err := validateOpenWeatherSettings(&settings.Realtime.OpenWeather); err != nil {
		ve.Errors = append(ve.Errors, err.Error())
	}

	// Validate WebServer settings
	if err := validateWebServerSettings(&settings.WebServer); err != nil {
		ve.Errors = append(ve.Errors, err.Error())
	}

	// Validate Realtime settings
	if err := validateRealtimeSettings(&settings.Realtime); err != nil {
		ve.Errors = append(ve.Errors, err.Error())
	}

	if len(ve.Errors) > 0 {
		return ve
	}
	return nil
}

func validateBirdNETSettings(settings *struct {
	Sensitivity float64
	Threshold   float64
	Overlap     float64
	Longitude   float64
	Latitude    float64
	Threads     int
	Locale      string
	RangeFilter RangeFilterSettings
}) error {
	var errs []string

	if settings.Sensitivity < 0 || settings.Sensitivity > 1.5 {
		errs = append(errs, "BirdNET sensitivity must be between 0 and 1.5")
	}

	if settings.Threshold < 0 || settings.Threshold > 1 {
		errs = append(errs, "BirdNET threshold must be between 0 and 1")
	}

	if settings.Overlap < 0 || settings.Overlap > 2.9 {
		errs = append(errs, "BirdNET overlap value must be between 0 and 2.9 seconds")
	}

	if settings.Longitude < -180 || settings.Longitude > 180 {
		errs = append(errs, "BirdNET longitude must be between -180 and 180")
	}

	if settings.Latitude < -90 || settings.Latitude > 90 {
		errs = append(errs, "BirdNET latitude must be between -90 and 90")
	}

	if settings.Threads < 0 {
		errs = append(errs, "BirdNET threads must be at least 0")
	}

	// Validate RangeFilter settings
	if settings.RangeFilter.Model == "" {
		errs = append(errs, "RangeFilter model must not be empty")
	}

	if settings.RangeFilter.Threshold < 0 || settings.RangeFilter.Threshold > 1 {
		errs = append(errs, "RangeFilter threshold must be between 0 and 1")
	}

	if len(errs) > 0 {
		return fmt.Errorf("BirdNET settings errors: %v", errs)
	}

	return nil
}

func validateOpenWeatherSettings(settings *OpenWeatherSettings) error {
	if settings.Enabled {
		if settings.APIKey == "" {
			return errors.New("OpenWeather API key is required when enabled")
		}
		if settings.Endpoint == "" {
			return errors.New("OpenWeather endpoint is required when enabled")
		}
		if settings.Interval < 1 {
			return errors.New("OpenWeather interval must be at least 1 minute")
		}
	}
	return nil
}

func validateWebServerSettings(settings *struct {
	Enabled bool
	Port    string
	AutoTLS bool
	Log     LogConfig
}) error {
	if settings.Enabled {
		if settings.Port == "" {
			return errors.New("WebServer port is required when enabled")
		}
		// You might want to add more specific port validation here
	}
	return nil
}

func validateRealtimeSettings(settings *RealtimeSettings) error {
	if settings.Interval < 0 {
		return errors.New("Realtime interval must be non-negative")
	}
	// Add more realtime settings validation as needed
	return nil
}
