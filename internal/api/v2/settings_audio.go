// internal/api/v2/settings_audio.go
package api

import (
	"fmt"
	"reflect"
	"slices"
	"strings"

	dspeq "github.com/tphakala/go-audio-dsp/equalizer"

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

// eqSkippedSource names one configured source whose equalizer chain skips at
// least one filter at the sample rate the source will run at.
type eqSkippedSource struct {
	name       string
	sampleRate int
	skipped    int
}

// streamRateLookup finds the registered source for a stream URL.
type streamRateLookup interface {
	GetByConnection(connStr string) (*audiocore.AudioSource, bool)
}

// equalizerSkipReport lists the configured sources whose equalizer chain skips
// at least one filter at the sample rate the source will run at. It walks the
// settings being saved, not the source registry, because the registry still
// holds the old rates, names and sources until the queued reconfigure runs.
// Sources come in config order: sound cards, then streams.
//
// A sound card runs at conf.EffectiveSampleRate of its configured rate, which
// is the rate the audio pipeline registers it with. A stream's rate is probed
// at runtime and not in the config, so it is read from the registry entry for
// its URL, and only when the previous settings had a stream with the same URL
// and models (a model change can switch the stream between the default and a
// probed rate in the later reconfigure). Other streams are left out, and so
// are all streams when registry is nil (the engine is not running).
//
// Not covered: the HLS listen route always runs at the default rate, so a
// filter above its Nyquist frequency is skipped there without being reported;
// a sound card that fails to start at its new rate; a save that changes only
// a stream URL or models and no equalizer setting; and any existing config
// file, whose startup is unchanged and logs a Warn per skipped filter when the
// chain is built.
func equalizerSkipReport(oldSettings, currentSettings *conf.Settings, registry streamRateLookup) []eqSkippedSource {
	var report []eqSkippedSource
	add := func(name string, sampleRate int) {
		eq := equalizer.ResolveEQSettings(currentSettings, name)
		if _, skipped := equalizer.BuildFilterChainReport(eq, sampleRate); len(skipped) > 0 {
			report = append(report, eqSkippedSource{name: name, sampleRate: sampleRate, skipped: len(skipped)})
		}
	}

	for i := range currentSettings.Realtime.Audio.Sources {
		cfg := &currentSettings.Realtime.Audio.Sources[i]
		if strings.TrimSpace(cfg.Device) == "" {
			continue
		}
		add(cfg.Name, conf.EffectiveSampleRate(cfg.SampleRate))
	}

	if registry == nil {
		return report
	}
	for i := range currentSettings.Realtime.RTSP.Streams {
		stream := &currentSettings.Realtime.RTSP.Streams[i]
		if !stream.IsEnabled() || stream.URL == "" || !streamUnchanged(oldSettings, stream) {
			continue
		}
		if src, ok := registry.GetByConnection(stream.URL); ok {
			add(stream.Name, src.EffectiveSampleRate())
		}
	}
	return report
}

// streamUnchanged reports whether oldSettings has a stream with the same URL
// and models as stream, so its registered sample rate still applies.
func streamUnchanged(oldSettings *conf.Settings, stream *conf.StreamConfig) bool {
	for i := range oldSettings.Realtime.RTSP.Streams {
		old := &oldSettings.Realtime.RTSP.Streams[i]
		if old.URL == stream.URL && slices.Equal(old.Models, stream.Models) {
			return true
		}
	}
	return false
}

// newlySkippedSources returns the sources in current that are not in previous,
// by name: the sources that start skipping filters in a save that changed no
// equalizer setting (for example a lower sample rate). A source that already
// skipped before the save is not reported again.
func newlySkippedSources(previous, current []eqSkippedSource) []eqSkippedSource {
	var added []eqSkippedSource
	for i := range current {
		if !slices.ContainsFunc(previous, func(p eqSkippedSource) bool { return p.name == current[i].name }) {
			added = append(added, current[i])
		}
	}
	return added
}

// equalizerSaveToast builds the toast that reports the outcome of an equalizer
// save: a success toast when every filter applies, else a warning naming the
// sources that skip filters. key and params are the translation key and its
// parameters, count and sources.
func equalizerSaveToast(report []eqSkippedSource) (message, toastType string, duration int, key string, params map[string]any) {
	if len(report) == 0 {
		return "Audio equalizer settings updated.", ToastTypeSuccess, toastDurationShort,
			notification.MsgSettingsEqualizerUpdated, nil
	}
	names := make([]string, len(report))
	for i := range report {
		names[i] = report[i].name
	}
	joined := strings.Join(names, ", ")
	return fmt.Sprintf("Audio equalizer settings saved, but some filters are skipped on %s: a filter must be valid at the source sample rate, with its frequency below half of it.", joined),
		ToastTypeWarning, toastDurationExtended, notification.MsgSettingsEqualizerFiltersSkipped,
		map[string]any{"count": len(names), "sources": joined}
}

// audioDeviceSettingChanged checks if audio device pipeline settings have changed.
// Only compares device-affecting fields (Device, Model, Models, SampleRate),
// not display-only fields (Name, Equalizer, QuietHours) or Gain, which are
// handled separately (gain by sourceGainsChanged).
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
			oldSources[i].Model != newSources[i].Model ||
			!slices.Equal(oldSources[i].Models, newSources[i].Models) ||
			oldSources[i].SampleRate != newSources[i].SampleRate {
			return true
		}
	}
	return false
}

// sourceGainsChanged reports whether the gain of any sound card source or
// stream differs between the two settings, compared index by index. A length
// change or a reorder is not reported here: audioDeviceSettingChanged and
// streamsSettingsChanged already trigger a reconfigure for those, and the
// reconfigure applies each source's gain. The gain itself is applied by the
// update_source_gains action to the live routes without a reconfigure.
func sourceGainsChanged(oldSettings, currentSettings *conf.Settings) bool {
	oldSources := oldSettings.Realtime.Audio.Sources
	newSources := currentSettings.Realtime.Audio.Sources
	if len(oldSources) == len(newSources) {
		for i := range oldSources {
			if oldSources[i].Gain != newSources[i].Gain {
				return true
			}
		}
	}

	oldStreams := oldSettings.Realtime.RTSP.Streams
	newStreams := currentSettings.Realtime.RTSP.Streams
	if len(oldStreams) == len(newStreams) {
		for i := range oldStreams {
			if oldStreams[i].Gain != newStreams[i].Gain {
				return true
			}
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
	eng := c.Engine.Load()
	if eng == nil {
		return nil
	}

	router := eng.Router()

	for _, src := range eng.Registry().List() {
		displayName := src.DisplayName
		router.UpdateFilterChain(src.ID, func(sampleRate int) *dspeq.FilterChain {
			return equalizer.ResolveAndBuildFilterChain(currentSettings, displayName, sampleRate)
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
		"FfmpegPath": true, // Runtime: validated at startup by ValidateToolPath, must not be overwritten by API
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
	sourcesChanged := audioDeviceSettingChanged(oldSettings, currentSettings)
	if sourcesChanged {
		audiocore.GetLogger().Info("audio device settings changed, triggering reconfiguration")
		reconfigActions = append(reconfigActions, actionReconfigureAudioSources)
		_ = c.SendToastWithKey("Reconfiguring audio sources...", "info", toastDurationMedium,
			notification.MsgSettingsReconfiguringAudioSources, nil)
	}

	// Check source gains: applied to the live routes without a reconfigure.
	if sourceGainsChanged(oldSettings, currentSettings) {
		c.Debug("Source gain settings changed, triggering live gain update")
		reconfigActions = append(reconfigActions, actionUpdateSourceGains)
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
	if eng := c.Engine.Load(); eng != nil {
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
			message, toastType, duration, key, params := equalizerSaveToast(equalizerSkipReport(oldSettings, currentSettings, registry))
			_ = c.SendToastWithKey(message, toastType, duration, key, params)
			return reconfigActions, nil
		}
	}

	// No equalizer setting changed, but a source setting did: warn about the
	// sources whose existing filters this save newly makes skip (a lower rate).
	// Sources are matched by name, so a save that also renames a source cannot
	// tell a renamed source from a new one and sends no warning.
	if sourcesChanged && !nameChanged {
		added := newlySkippedSources(
			equalizerSkipReport(oldSettings, oldSettings, registry),
			equalizerSkipReport(oldSettings, currentSettings, registry))
		if len(added) > 0 {
			message, toastType, duration, key, params := equalizerSaveToast(added)
			_ = c.SendToastWithKey(message, toastType, duration, key, params)
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
