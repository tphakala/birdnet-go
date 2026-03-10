// internal/api/v2/settings_audio.go
package api

import (
	"fmt"
	"reflect"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/myaudio"
	"github.com/tphakala/birdnet-go/internal/notification"
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
	registry := myaudio.GetRegistry()
	if err := registry.UpdateAllFilterChains(settings); err != nil {
		return fmt.Errorf("failed to update audio filter chains: %w", err)
	}
	return nil
}

// getAudioBlockedFields returns the blocked fields map for the audio section
func getAudioBlockedFields() map[string]any {
	return map[string]any{
		"SoxAudioTypes": true, // Runtime list of supported audio types
	}
}

// extendedCaptureSettingsChanged checks if extended capture settings have changed
// in a way that requires a restart. When extended capture is disabled on both old
// and new settings, changes to MaxDuration or Species are irrelevant.
func extendedCaptureSettingsChanged(oldSettings, currentSettings *conf.Settings) bool {
	old := oldSettings.Realtime.ExtendedCapture
	cur := currentSettings.Realtime.ExtendedCapture

	if old.Enabled != cur.Enabled {
		return true
	}

	// If disabled on both sides, other fields don't matter
	if !old.Enabled && !cur.Enabled {
		return false
	}

	if old.MaxDuration != cur.MaxDuration {
		return true
	}

	if !reflect.DeepEqual(old.Species, cur.Species) {
		return true
	}

	return false
}

// handleAudioSettingsChanges checks for audio-related settings changes and triggers appropriate actions
func (c *Controller) handleAudioSettingsChanges(oldSettings, currentSettings *conf.Settings) ([]string, error) {
	var reconfigActions []string

	// Check sound level monitoring settings
	if soundLevelSettingsChanged(oldSettings, currentSettings) {
		c.Debug("Sound level monitoring settings changed, triggering reconfiguration")
		reconfigActions = append(reconfigActions, "reconfigure_sound_level")
		// Send toast notification
		_ = c.SendToastWithKey("Reconfiguring sound level monitoring...", "info", toastDurationShort,
			notification.MsgSettingsReconfiguringSoundLevel, nil)
	}

	// Check audio device settings
	if audioDeviceSettingChanged(oldSettings, currentSettings) {
		c.Debug("Audio device changed. A restart will be required.")
		// Send toast notification about restart requirement
		_ = c.SendToastWithKey("Audio device changed. Restart required to apply changes.", "warning", toastDurationExtended,
			notification.MsgSettingsAudioDeviceRestart, nil)
	}

	// Check extended capture settings (requires restart to resize capture buffers)
	if extendedCaptureSettingsChanged(oldSettings, currentSettings) {
		c.Debug("Extended capture settings changed. A restart will be required.")
		_ = c.SendToastWithKey("Extended capture settings changed. Restart required to apply changes.", "warning", toastDurationExtended,
			notification.MsgSettingsExtendedCaptureRestart, nil)
	}

	// Check audio equalizer settings
	if equalizerSettingsChanged(oldSettings.Realtime.Audio.Equalizer, currentSettings.Realtime.Audio.Equalizer) {
		c.Debug("Audio equalizer settings changed, updating filter chain")
		// Handle audio equalizer changes synchronously as it returns an error
		if err := c.handleEqualizerChange(currentSettings); err != nil {
			// Send error toast
			_ = c.SendToastWithKey("Failed to update audio equalizer settings", "error", toastDurationLong,
				notification.MsgSettingsEqualizerFailed, nil)
			return reconfigActions, fmt.Errorf("failed to update audio equalizer: %w", err)
		}
		// Send success toast
		_ = c.SendToastWithKey("Audio equalizer settings updated", "success", toastDurationShort,
			notification.MsgSettingsEqualizerUpdated, nil)
	}

	return reconfigActions, nil
}

// quietHoursSettingsChanged checks if any quiet hours settings have changed
// across streams or the sound card
func quietHoursSettingsChanged(oldSettings, currentSettings *conf.Settings) bool {
	// Check sound card quiet hours
	if !reflect.DeepEqual(oldSettings.Realtime.Audio.QuietHours, currentSettings.Realtime.Audio.QuietHours) {
		return true
	}

	// Check stream quiet hours (compare each stream's QuietHours field)
	oldStreams := oldSettings.Realtime.RTSP.Streams
	newStreams := currentSettings.Realtime.RTSP.Streams

	// If stream count changed, quiet hours need re-evaluation
	// (added/removed streams may have quiet hours configured)
	if len(oldStreams) != len(newStreams) {
		return true
	}

	for i := range oldStreams {
		if !reflect.DeepEqual(oldStreams[i].QuietHours, newStreams[i].QuietHours) {
			return true
		}
	}

	return false
}

// getAudioSectionValue returns a pointer to the audio section of settings for in-place updates
func getAudioSectionValue(settings *conf.Settings) any {
	return &settings.Realtime.Audio
}

// getAudioSection returns the audio section of settings
func getAudioSection(settings *conf.Settings) any {
	return settings.Realtime.Audio
}
