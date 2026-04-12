// internal/api/v2/settings_audio.go
package api

import (
	"reflect"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/notification"
)

// audioDeviceSettingChanged checks if audio device pipeline settings have changed.
// Only compares device-affecting fields (Device, Gain, Model), not display-only
// fields (Name, Equalizer, QuietHours) which are handled separately.
func audioDeviceSettingChanged(oldSettings, currentSettings *conf.Settings) bool {
	oldSources := oldSettings.Realtime.Audio.Sources
	newSources := currentSettings.Realtime.Audio.Sources

	if len(oldSources) != len(newSources) {
		return true
	}
	for i := range oldSources {
		if oldSources[i].Device != newSources[i].Device ||
			oldSources[i].Gain != newSources[i].Gain ||
			oldSources[i].Model != newSources[i].Model {
			return true
		}
	}
	return false
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
func (c *Controller) handleEqualizerChange(_ *conf.Settings) error {
	// TODO: Equalizer filter chains need to be migrated to audiocore.
	// For now, this is a no-op; equalizer settings changes will take effect on restart.
	c.Debug("Equalizer filter chain update is a no-op pending audiocore filter migration")
	return nil
}

// getAudioBlockedFields returns the blocked fields map for the audio section
func getAudioBlockedFields() map[string]any {
	return map[string]any{
		"FfmpegPath":    true, // Runtime: validated at startup by ValidateToolPath, must not be overwritten by API
		"SoxPath":       true, // Runtime: validated at startup by ValidateToolPath, must not be overwritten by API
		"SoxAudioTypes": true, // Runtime list of supported audio types
	}
}

// extendedCaptureFilterChanged checks if extended capture settings that affect
// the species filter have changed (Enabled, Species, MaxDuration). These changes
// can be applied at runtime without a restart by rebuilding the filter map.
func extendedCaptureFilterChanged(oldSettings, currentSettings *conf.Settings) bool {
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

// extendedCaptureBufferChanged checks if capture buffer settings have changed,
// which requires a restart to resize the audio ring buffer.
func extendedCaptureBufferChanged(oldSettings, currentSettings *conf.Settings) bool {
	old := oldSettings.Realtime.ExtendedCapture
	cur := currentSettings.Realtime.ExtendedCapture

	return old.CaptureBufferSeconds != cur.CaptureBufferSeconds
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
		c.Debug("Audio device settings changed, triggering reconfiguration")
		reconfigActions = append(reconfigActions, "reconfigure_audio_sources")
		_ = c.SendToastWithKey("Reconfiguring audio sources...", "info", toastDurationMedium,
			notification.MsgSettingsReconfiguringAudioSources, nil)
	}

	// Check extended capture filter settings (hot-reloadable: Enabled, Species, MaxDuration)
	if extendedCaptureFilterChanged(oldSettings, currentSettings) {
		c.Debug("Extended capture filter settings changed, triggering rebuild")
		reconfigActions = append(reconfigActions, "rebuild_extended_capture")
		_ = c.SendToastWithKey("Rebuilding extended capture species filter...", "info", toastDurationShort,
			notification.MsgSettingsRebuildingExtendedCapture, nil)
	}

	// Check extended capture buffer settings (requires restart to resize audio ring buffer)
	if extendedCaptureBufferChanged(oldSettings, currentSettings) {
		c.Debug("Extended capture buffer settings changed. A restart will be required.")
		_ = c.SendToastWithKey("Extended capture buffer settings changed. Restart required to apply.", "warning", toastDurationExtended,
			notification.MsgSettingsExtendedCaptureRestart, nil)
	}

	// Check audio equalizer settings
	if equalizerSettingsChanged(oldSettings.Realtime.Audio.Equalizer, currentSettings.Realtime.Audio.Equalizer) {
		c.Debug("Audio equalizer settings changed; will take effect on restart (audiocore filter migration pending)")
		_ = c.SendToast("Equalizer settings saved. Restart required to apply changes.", "warning", toastDurationExtended)
	}

	return reconfigActions, nil
}

// quietHoursSettingsChanged checks if any quiet hours settings have changed
// across streams, audio sources, or the global sound card setting.
func quietHoursSettingsChanged(oldSettings, currentSettings *conf.Settings) bool {
	// Check global sound card quiet hours (legacy fallback)
	if !reflect.DeepEqual(oldSettings.Realtime.Audio.QuietHours, currentSettings.Realtime.Audio.QuietHours) {
		return true
	}

	// Check per-audio-source quiet hours
	oldSources := oldSettings.Realtime.Audio.Sources
	newSources := currentSettings.Realtime.Audio.Sources
	if len(oldSources) != len(newSources) {
		return true
	}
	for i := range oldSources {
		if !reflect.DeepEqual(oldSources[i].QuietHours, newSources[i].QuietHours) {
			return true
		}
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
