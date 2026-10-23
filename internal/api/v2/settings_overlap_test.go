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

// TestAnalysisOverlapChanged verifies the birdnet.overlap change detector. Overlap
// drives the realtime analysis-buffer cadence, so a change must be detected to
// trigger restart_audio_capture (a full teardown that reallocates the buffers
// with new dimensions); an unrelated save must not, so buffers are not needlessly
// rebuilt.
func TestAnalysisOverlapChanged(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		mutate  func(s *conf.Settings)
		changed bool
	}{
		{
			name:    "overlap change is detected",
			mutate:  func(s *conf.Settings) { s.BirdNET.Overlap += 0.5 },
			changed: true,
		},
		{
			name:    "overlap set to a new value is detected",
			mutate:  func(s *conf.Settings) { s.BirdNET.Overlap = 2.4 },
			changed: true,
		},
		{
			name:    "unrelated birdnet field is not an overlap change",
			mutate:  func(s *conf.Settings) { s.BirdNET.Sensitivity += 0.1 },
			changed: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			base := apitest.NewValidTestSettings()
			base.BirdNET.Overlap = 0.0
			updated := conf.CloneSettings(base)
			tt.mutate(updated)

			assert.Equal(t, tt.changed, analysisOverlapChanged(base, updated),
				"detector result mismatch for %q", tt.name)
			assert.False(t, analysisOverlapChanged(base, conf.CloneSettings(base)),
				"detector reported a change for identical settings in %q", tt.name)
		})
	}
}

func TestFalsePositiveFilterActiveChanged(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		from, to int
		changed  bool
	}{
		{"off to on", 0, 3, true},
		{"on to off", 2, 0, true},
		{"on to on", 1, 5, false},
		{"off to off", 0, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			oldS := apitest.NewValidTestSettings()
			oldS.Realtime.FalsePositiveFilter.Level = tt.from
			newS := conf.CloneSettings(oldS)
			newS.Realtime.FalsePositiveFilter.Level = tt.to
			assert.Equal(t, tt.changed, falsePositiveFilterActiveChanged(oldS, newS))
		})
	}
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
