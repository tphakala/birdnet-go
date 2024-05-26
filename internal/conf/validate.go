// conf/validate.go config settings validation code goes here
package conf

import "errors"

// validateSettings validates the settings
func validateSettings(settings *Settings) error {
	// Add your validation logic here
	if settings.Realtime.MQTT.Enabled && settings.Realtime.MQTT.Broker == "" {
		return errors.New("MQTT broker URL is required when MQTT is enabled")
	}
	// Other options to validate go here

	return nil
}
