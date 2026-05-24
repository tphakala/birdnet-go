// internal/api/v2/settings_audio.go
package api

import (
	"reflect"
	"slices"

	"github.com/tphakala/birdnet-go/internal/audiocore"
	"github.com/tphakala/birdnet-go/internal/audiocore/equalizer"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/notification"
)

// sourceNameUpdater is the subset of SourceRegistry used by the name-sync
// helpers. Accepting an interface keeps them testable without a full registry.
type sourceNameUpdater interface {
	GetByConnection(connStr string) (*audiocore.AudioSource, bool)
	UpdateDisplayName(sourceID, newName string) bool
}

// audioDeviceSettingChanged checks if audio device pipeline settings have changed.
// Only compares device-affecting fields (Device, Gain, Model, Models,
// SampleRate), not display-only fields (Name, Equalizer, QuietHours) which
// are handled separately.
//
// Models is the per-source list of classifier IDs (e.g. ["birdnet",
// "perch_v2"]); Model is the deprecated singular alias. Both are compared so
// hot-reload fires whether the user edits the new list or a legacy field.
func audioDeviceSettingChanged(oldSettings, currentSettings *conf.Settings) bool {
	oldSources := oldSettings.Realtime.Audio.Sources
	newSources := currentSettings.Realtime.Audio.Sources

	if len(oldSources) != len(newSources) {
		return true
	}
	for i := range oldSources {
		if oldSources[i].Device != newSources[i].Device ||
			oldSources[i].Gain != newSources[i].Gain ||
			oldSources[i].Model != newSources[i].Model ||
			!slices.Equal(oldSources[i].Models, newSources[i].Models) ||
			oldSources[i].SampleRate != newSources[i].SampleRate {
			return true
		}
	}
	return false
}

// syncAudioSourceNames detects audio sources that were renamed while keeping
// the same device, and updates their DisplayName in the registry. Returns true
// if any name was changed. Uses a map keyed by Device so renames are detected
// even if the source list was reordered.
func syncAudioSourceNames(oldSettings, currentSettings *conf.Settings, registry sourceNameUpdater) bool {
	sources := oldSettings.Realtime.Audio.Sources
	oldNames := make(map[string]string, len(sources))
	for i := range sources {
		if sources[i].Device != "" {
			oldNames[sources[i].Device] = sources[i].Name
		}
	}

	changed := false
	newSources := currentSettings.Realtime.Audio.Sources
	for i := range newSources {
		if newSources[i].Device == "" {
			continue
		}
		if oldName, ok := oldNames[newSources[i].Device]; ok && oldName != newSources[i].Name {
			changed = true
			if registry != nil {
				if src, ok := registry.GetByConnection(newSources[i].Device); ok {
					registry.UpdateDisplayName(src.ID, newSources[i].Name)
				}
			}
		}
	}
	return changed
}

// syncStreamNames detects streams that were renamed while keeping the same URL,
// and updates their DisplayName in the registry. Returns true if any name was
// changed. Uses a map keyed by URL so renames are detected even if the stream
// list was reordered.
func syncStreamNames(oldSettings, currentSettings *conf.Settings, registry sourceNameUpdater) bool {
	streams := oldSettings.Realtime.RTSP.Streams
	oldNames := make(map[string]string, len(streams))
	for i := range streams {
		if streams[i].URL != "" {
			oldNames[streams[i].URL] = streams[i].Name
		}
	}

	changed := false
	newStreams := currentSettings.Realtime.RTSP.Streams
	for i := range newStreams {
		if newStreams[i].URL == "" {
			continue
		}
		if oldName, ok := oldNames[newStreams[i].URL]; ok && oldName != newStreams[i].Name {
			changed = true
			if registry != nil {
				if src, ok := registry.GetByConnection(newStreams[i].URL); ok {
					registry.UpdateDisplayName(src.ID, newStreams[i].Name)
				}
			}
		}
	}
	return changed
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

// handleEqualizerChange rebuilds EQ filter chains and hot-swaps them on all
// registered sources (both sound cards and streams). Each source's effective EQ
// is resolved via Settings.ResolveEQOverride using the registry DisplayName.
func (c *Controller) handleEqualizerChange(currentSettings *conf.Settings) error {
	eng := c.engine.Load()
	if eng == nil {
		return nil
	}

	router := eng.Router()
	globalEQ := currentSettings.Realtime.Audio.Equalizer

	for _, src := range eng.Registry().List() {
		displayName := src.DisplayName
		override := currentSettings.ResolveEQOverride(displayName)
		router.UpdateFilterChain(src.ID, func(sampleRate int) *equalizer.FilterChain {
			return equalizer.BuildFilterChainWithOverride(override, globalEQ, displayName, sampleRate)
		})
	}

	c.Debug("EQ filter chains updated for all sources")
	return nil
}

// perSourceEqualizerChanged checks if any per-source equalizer settings have changed.
func perSourceEqualizerChanged(oldSettings, currentSettings *conf.Settings) bool {
	oldSources := oldSettings.Realtime.Audio.Sources
	newSources := currentSettings.Realtime.Audio.Sources
	if len(oldSources) != len(newSources) {
		return true
	}
	for i := range oldSources {
		if !reflect.DeepEqual(oldSources[i].Equalizer, newSources[i].Equalizer) {
			return true
		}
	}
	return false
}

// perStreamEqualizerChanged checks if any per-stream equalizer settings have changed.
func perStreamEqualizerChanged(oldSettings, currentSettings *conf.Settings) bool {
	oldStreams := oldSettings.Realtime.RTSP.Streams
	newStreams := currentSettings.Realtime.RTSP.Streams
	if len(oldStreams) != len(newStreams) {
		return true
	}
	for i := range oldStreams {
		if !reflect.DeepEqual(oldStreams[i].Equalizer, newStreams[i].Equalizer) {
			return true
		}
	}
	return false
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
		reconfigActions = append(reconfigActions, actionReconfigureSoundLevel)
		// Send toast notification
		_ = c.SendToastWithKey("Reconfiguring sound level monitoring...", "info", toastDurationShort,
			notification.MsgSettingsReconfiguringSoundLevel, nil)
	}

	// Check audio device settings
	if audioDeviceSettingChanged(oldSettings, currentSettings) {
		audiocore.GetLogger().Info("audio device settings changed, triggering reconfiguration")
		reconfigActions = append(reconfigActions, actionReconfigureAudioSources)
		_ = c.SendToastWithKey("Reconfiguring audio sources...", "info", toastDurationMedium,
			notification.MsgSettingsReconfiguringAudioSources, nil)
	}

	// Check extended capture filter settings (hot-reloadable: Enabled, Species, MaxDuration)
	if extendedCaptureFilterChanged(oldSettings, currentSettings) {
		c.Debug("Extended capture filter settings changed, triggering rebuild")
		reconfigActions = append(reconfigActions, actionRebuildExtendedCapture)
		_ = c.SendToastWithKey("Rebuilding extended capture species filter...", "info", toastDurationShort,
			notification.MsgSettingsRebuildingExtendedCapture, nil)
	}

	// Check extended capture buffer settings (requires restart to resize audio ring buffer)
	if extendedCaptureBufferChanged(oldSettings, currentSettings) {
		c.Debug("Extended capture buffer settings changed. A restart will be required.")
		_ = c.SendToastWithKey("Extended capture buffer settings changed. Restart required to apply.", "warning", toastDurationExtended,
			notification.MsgSettingsExtendedCaptureRestart, nil)
	}

	// Detect source/stream name changes and sync DisplayName in the registry.
	// Each function detects renames and updates the registry in a single pass.
	var registry sourceNameUpdater
	if eng := c.engine.Load(); eng != nil {
		registry = eng.Registry()
	}
	srcNameChanged := syncAudioSourceNames(oldSettings, currentSettings, registry)
	strmNameChanged := syncStreamNames(oldSettings, currentSettings, registry)

	// Check audio equalizer settings (global, per-source, or per-stream) - hot-swap filter chains.
	// Also rebuild when names change, since ResolveEQOverride matches by name.
	globalEQChanged := equalizerSettingsChanged(oldSettings.Realtime.Audio.Equalizer, currentSettings.Realtime.Audio.Equalizer)
	perSourceEQChanged := perSourceEqualizerChanged(oldSettings, currentSettings)
	perStreamEQChanged := perStreamEqualizerChanged(oldSettings, currentSettings)
	nameChanged := srcNameChanged || strmNameChanged

	if globalEQChanged || perSourceEQChanged || perStreamEQChanged || nameChanged {
		c.Debug("Audio equalizer settings changed (global=%v, perSource=%v, perStream=%v, nameChanged=%v)",
			globalEQChanged, perSourceEQChanged, perStreamEQChanged, nameChanged)
		for i := range currentSettings.Realtime.Audio.Sources {
			src := &currentSettings.Realtime.Audio.Sources[i]
			if src.Equalizer != nil {
				c.Debug("Source %d: per-source EQ enabled=%v, filters=%d",
					i, src.Equalizer.Enabled, len(src.Equalizer.Filters))
			} else {
				c.Debug("Source %d: per-source EQ is nil (using global)", i)
			}
		}
		if err := c.handleEqualizerChange(currentSettings); err != nil {
			c.Debug("Failed to update EQ filter chains: %v", err)
		}
		if globalEQChanged || perSourceEQChanged || perStreamEQChanged {
			_ = c.SendToast("Audio equalizer settings updated.", "success", toastDurationShort)
		}
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
