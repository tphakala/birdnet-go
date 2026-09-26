package api

// Audio settings change action signals. These constants are the single source
// of truth for action strings emitted by handleAudioSettingsChanges.
const (
	actionReconfigureSoundLevel   = "reconfigure_sound_level"
	actionReconfigureAudioSources = "reconfigure_audio_sources"
	actionRebuildExtendedCapture  = "rebuild_extended_capture"
	// actionRestartAudioCapture tears down and rebuilds all audio capture,
	// reallocating every analysis buffer. Used for changes the diff-based
	// reconfigure_audio_sources cannot see because they are not per-source audio
	// properties, e.g. birdnet.overlap (which changes the analysis buffer
	// read/overlap dimensions).
	actionRestartAudioCapture = "restart_audio_capture"
)

// SettingsChangeActions returns all action strings declared in the
// settingsChangeChecks table. Used by coverage tests to cross-validate
// that the hot-reload registry references real actions.
func SettingsChangeActions() []string {
	actions := make([]string, 0, len(settingsChangeChecks))
	for _, check := range settingsChangeChecks {
		if check.action != "" {
			actions = append(actions, check.action)
		}
	}
	return actions
}

// AudioSettingsChangeActions returns the action strings triggered by
// handleAudioSettingsChanges. Built from named constants shared with
// the implementation, so additions here require updating both sites.
func AudioSettingsChangeActions() []string {
	return []string{
		actionReconfigureSoundLevel,
		actionReconfigureAudioSources,
		actionRebuildExtendedCapture,
	}
}
