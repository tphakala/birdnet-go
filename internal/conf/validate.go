// conf/validate.go config settings validation code goes here
package conf

import (
	"errors"
	"fmt"
)

// validateSettings validates the settings
func validateSettings(settings *Settings) error {
	// MQTT Validation
	if settings.Realtime.MQTT.Enabled && settings.Realtime.MQTT.Broker == "" {
		return errors.New("MQTT broker URL is required when MQTT is enabled")
	}

	// BirdNET Overlap Validation
	if settings.BirdNET.Overlap < 0 || settings.BirdNET.Overlap > 2.9 {
		return errors.New("BirdNET overlap value must be between 0 and 2.9 seconds")
	}

	// OpenWeather Validation
	if settings.Realtime.OpenWeather.Enabled {
		if settings.Realtime.OpenWeather.APIKey == "" {
			return fmt.Errorf("OpenWeather API key is required")
		}
		if settings.Realtime.OpenWeather.Endpoint == "" {
			return fmt.Errorf("OpenWeather endpoint is required")
		}
	}

	// Other options to validate go here

	return nil
}
