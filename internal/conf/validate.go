// conf/validate.go config settings validation code goes here
package conf

import "errors"

// validateSettings validates the settings
func validateSettings(settings *Settings) error {
	// Add your validation logic here
	if settings.Realtime.MQTT.Enabled && settings.Realtime.MQTT.Broker == "" {
		return errors.New("MQTT broker URL is required when MQTT is enabled")
	}

	// Add validation for BirdNET.Overlap
	if settings.BirdNET.Overlap < 0 || settings.BirdNET.Overlap > 2.9 {
		return errors.New("BirdNET overlap value must be between 0 and 2.9 seconds")
	}

	// Other options to validate go here

	return nil
}
