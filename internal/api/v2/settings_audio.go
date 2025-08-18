// internal/api/v2/settings_audio.go
package api

import (
	"fmt"
	"reflect"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/myaudio"
)

// audioDeviceSettingChanged checks if audio device settings have changed
func audioDeviceSettingChanged(oldSettings, currentSettings *conf.Settings) bool {
	return oldSettings.Realtime.Audio.Source != currentSettings.Realtime.Audio.Source
}

// soundLevelSettingsChanged checks if sound level monitoring settings have changed
func soundLevelSettingsChanged(oldSettings, currentSettings *conf.Settings) bool {
	// Check for changes in enabled state
	if oldSettings.Realtime.Audio.SoundLevel.Enabled != currentSettings.Realtime.Audio.SoundLevel.Enabled {
		return true
	}

	// Check for changes in interval (only if enabled)
	if currentSettings.Realtime.Audio.SoundLevel.Enabled &&
		oldSettings.Realtime.Audio.SoundLevel.Interval != currentSettings.Realtime.Audio.SoundLevel.Interval {
		return true
	}

	return false
}

// equalizerSettingsChanged checks if audio equalizer settings have changed
func equalizerSettingsChanged(oldSettings, newSettings conf.EqualizerSettings) bool {
	return !reflect.DeepEqual(oldSettings, newSettings)
}

// handleEqualizerChange updates the audio filter chain when equalizer settings change
func (c *Controller) handleEqualizerChange(settings *conf.Settings) error {
	if err := myaudio.UpdateFilterChain(settings); err != nil {
		return fmt.Errorf("failed to update audio filter chain: %w", err)
	}
	return nil
}

// getAudioBlockedFields returns the blocked fields map for the audio section
func getAudioBlockedFields() map[string]interface{} {
	return map[string]interface{}{
		"SoxAudioTypes": true, // Runtime list of supported audio types
	}
}

// handleAudioSettingsChanges checks for audio-related settings changes and triggers appropriate actions
func (c *Controller) handleAudioSettingsChanges(oldSettings, currentSettings *conf.Settings) ([]string, error) {
	var reconfigActions []string

	// Check sound level monitoring settings
	if soundLevelSettingsChanged(oldSettings, currentSettings) {
		c.Debug("Sound level monitoring settings changed, triggering reconfiguration")
		reconfigActions = append(reconfigActions, "reconfigure_sound_level")
		// Send toast notification
		_ = c.SendToast("Reconfiguring sound level monitoring...", "info", 3000)
	}

	// Check audio device settings
	if audioDeviceSettingChanged(oldSettings, currentSettings) {
		c.Debug("Audio device changed. A restart will be required.")
		// Send toast notification about restart requirement
		_ = c.SendToast("Audio device changed. Restart required to apply changes.", "warning", 8000)
	}

	// Check audio equalizer settings
	if equalizerSettingsChanged(oldSettings.Realtime.Audio.Equalizer, currentSettings.Realtime.Audio.Equalizer) {
		c.Debug("Audio equalizer settings changed, updating filter chain")
		// Handle audio equalizer changes synchronously as it returns an error
		if err := c.handleEqualizerChange(currentSettings); err != nil {
			// Send error toast
			_ = c.SendToast("Failed to update audio equalizer settings", "error", 5000)
			return reconfigActions, fmt.Errorf("failed to update audio equalizer: %w", err)
		}
		// Send success toast
		_ = c.SendToast("Audio equalizer settings updated", "success", 3000)
	}

	return reconfigActions, nil
}

// getAudioSectionValue returns a pointer to the audio section of settings for in-place updates
func getAudioSectionValue(settings *conf.Settings) interface{} {
	return &settings.Realtime.Audio
}

// getAudioSection returns the audio section of settings
func getAudioSection(settings *conf.Settings) interface{} {
	return settings.Realtime.Audio
}