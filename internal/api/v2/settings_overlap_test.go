package api

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/api/v2/apitest"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// TestAnalysisOverlapDispatchesRestart guards the actual hot-reload wiring: an
// overlap change must resolve, through the settingsChangeChecks table, to the
// restart_audio_capture action (a full teardown that reallocates the analysis
// buffers). This fails if the table row is removed or its action is changed back
// to the diff-based reconfigure that cannot see an overlap change (issue #4096).
func TestAnalysisOverlapDispatchesRestart(t *testing.T) {
	t.Parallel()

	var entry *settingsChangeCheck
	for i := range settingsChangeChecks {
		if settingsChangeChecks[i].changed == nil {
			continue
		}
		if settingsChangeChecks[i].name == "Analysis overlap" {
			entry = &settingsChangeChecks[i]
			break
		}
	}
	require.NotNil(t, entry, "settingsChangeChecks must contain the Analysis overlap detector")
	assert.Equal(t, actionRestartAudioCapture, entry.action,
		"an overlap change must trigger a full audio-capture restart, not the diff-based reconfigure")

	base := apitest.NewValidTestSettings()
	base.BirdNET.Overlap = 0.0
	changed := conf.CloneSettings(base)
	changed.BirdNET.Overlap = 2.4
	assert.True(t, entry.changed(base, changed), "overlap change must be detected by the table entry")
	assert.False(t, entry.changed(base, conf.CloneSettings(base)), "identical settings must not fire")
}

// TestAnalysisOverlapChangedIgnoresOtherBirdNETFields verifies that a save that
// touches an unrelated birdnet field does not look like an overlap change, so the
// analysis buffers are not rebuilt (a full restart_audio_capture) needlessly.
// That an overlap change is detected is pinned by
// TestAnalysisOverlapDispatchesRestart.
func TestAnalysisOverlapChangedIgnoresOtherBirdNETFields(t *testing.T) {
	t.Parallel()

	base := apitest.NewValidTestSettings()
	base.BirdNET.Overlap = 0.0
	updated := conf.CloneSettings(base)
	updated.BirdNET.Sensitivity += 0.1

	assert.False(t, analysisOverlapChanged(base, updated),
		"a sensitivity change must not count as an overlap change")
}

// TestFalsePositiveFilterCrossingDispatchesReconfigure guards the table row that
// re-plans the analysis cadence when the false positive filter is switched on or
// off: the cap applies only while the filter is on, so a 0 to >=1 save (and
// back) must reach reconfigure_audio_sources, while a change that stays on one
// side of 0 must not fire.
func TestFalsePositiveFilterCrossingDispatchesReconfigure(t *testing.T) {
	t.Parallel()

	var entry *settingsChangeCheck
	for i := range settingsChangeChecks {
		if settingsChangeChecks[i].name == "FP filter cadence" {
			entry = &settingsChangeChecks[i]
			break
		}
	}
	require.NotNil(t, entry, "settingsChangeChecks must contain the FP filter cadence detector")
	assert.Equal(t, actionReconfigureAudioSources, entry.action,
		"a filter on/off change must re-plan through reconfigure_audio_sources")

	withLevel := func(level int) *conf.Settings {
		s := apitest.NewValidTestSettings()
		s.Realtime.FalsePositiveFilter.Level = level
		return s
	}
	assert.True(t, entry.changed(withLevel(0), withLevel(1)), "0 to 1 turns the filter on")
	assert.True(t, entry.changed(withLevel(3), withLevel(0)), "3 to 0 turns the filter off")
	assert.False(t, entry.changed(withLevel(2), withLevel(4)), "2 to 4 keeps the filter on")
	assert.False(t, entry.changed(withLevel(0), withLevel(0)), "0 to 0 keeps the filter off")
}
