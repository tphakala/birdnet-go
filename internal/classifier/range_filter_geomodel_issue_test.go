package classifier

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/conf/conftest"
)

// TestGeomodelIssueFor pins the diagnosis order: a config that selects a geomodel is a
// load failure even when the files exist, and only a non-selecting config with the files
// present reads as "not selected". A reordered rule would tell a user to install
// something that is already installed.
func TestGeomodelIssueFor(t *testing.T) {
	tests := []struct {
		name       string
		rf         conf.RangeFilterSettings
		wants      bool
		serving    bool
		filesExist bool
		want       GeomodelIssue
	}{
		{"v2.4 only on legacy config needs no geomodel", conf.RangeFilterSettings{Model: conf.RangeFilterModelLegacy}, false, false, false, GeomodelIssueNone},
		{"geomodel backend serves the set", conf.RangeFilterSettings{}, true, true, true, GeomodelIssueNone},
		{"default config, no files, no backend", conf.RangeFilterSettings{}, true, false, false, GeomodelIssueNotInstalled},
		{"default config, no files, v2.4 MData backend", conf.RangeFilterSettings{}, true, false, false, GeomodelIssueNotInstalled},
		{"legacy config with files", conf.RangeFilterSettings{Model: conf.RangeFilterModelLegacy}, true, false, true, GeomodelIssueNotSelected},
		{"strict ONNX path without labels, with files", conf.RangeFilterSettings{ModelPath: "/m/custom.onnx"}, true, false, true, GeomodelIssueNotSelected},
		{"latest with files and no backend", conf.RangeFilterSettings{Model: conf.RangeFilterModelLatest}, true, false, true, GeomodelIssueLoadFailed},
		{"explicit v3 with no files", conf.RangeFilterSettings{Model: conf.RangeFilterModelV3}, true, false, false, GeomodelIssueLoadFailed},
		{"explicit v3 with MData backend", conf.RangeFilterSettings{Model: conf.RangeFilterModelV3}, true, false, false, GeomodelIssueLoadFailed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, geomodelIssueFor(&tt.rf, tt.wants, tt.serving, tt.filesExist))
		})
	}
}

// TestRangeFilterStatus_GeomodelIssueFollowsLiveState walks one orchestrator through
// install, config and model-set changes: the diagnosis is recomputed on every status call.
func TestRangeFilterStatus_GeomodelIssueFollowsLiveState(t *testing.T) {
	settings := conftest.GetTestSettings()
	settings.BirdNET.Labels = nil
	settings.BirdNET.RangeFilter.Model = ""
	settings.BirdNET.RangeFilter.ModelPath = ""
	conftest.SetTestSettings(settings)
	t.Cleanup(func() { conftest.SetTestSettings(nil) })

	perchLabels := []string{"Turdus merula_Common Blackbird"}
	modelsDir := t.TempDir()
	o := &Orchestrator{
		Settings:  settings,
		modelsDir: modelsDir,
		models: map[string]*modelEntry{
			RegistryIDPerchV2: {instance: &mockModelInstance{id: RegistryIDPerchV2, labels: perchLabels, numSpecies: len(perchLabels)}},
		},
	}
	o.settingsAtomic.Store(settings)
	o.rangeFilter = newRangeFilterService(nil)

	assert.Equal(t, GeomodelIssueNotInstalled, o.RangeFilterStatus().GeomodelIssue, "Perch without geomodel files")

	writeSharedGeomodel(t, modelsDir)
	settings.BirdNET.RangeFilter.Model = conf.RangeFilterModelLegacy
	assert.Equal(t, GeomodelIssueNotSelected, o.RangeFilterStatus().GeomodelIssue, "files installed, legacy model configured")

	o.rangeFilter.state.Store(&rangeFilterState{
		backend: &fakeUniversalRangeFilter{}, kind: rfKindGeomodelV3, generation: 1,
	})
	assert.Equal(t, GeomodelIssueNone, o.RangeFilterStatus().GeomodelIssue, "geomodel backend active")

	o.rangeFilter.state.Store(&rangeFilterState{
		backend: &fakeUniversalRangeFilter{}, kind: rfKindMDataV2, generation: 2,
	})
	o.models = map[string]*modelEntry{}
	assert.Equal(t, GeomodelIssueNone, o.RangeFilterStatus().GeomodelIssue, "no participant needs the geomodel")
}

// TestReload_FailureMarksStateUntilNextPublishedState checks that a failed reload that
// keeps the previous backend is visible in the status, and that the next successful
// reload clears it.
func TestReload_FailureMarksStateUntilNextPublishedState(t *testing.T) {
	settings := conftest.GetTestSettings()
	settings.BirdNET.Labels = nil
	settings.BirdNET.RangeFilter.Model = conf.RangeFilterModelV3
	settings.BirdNET.RangeFilter.ModelPath = ""
	settings.BirdNET.RangeFilter.LabelsPath = ""
	conftest.SetTestSettings(settings)
	t.Cleanup(func() { conftest.SetTestSettings(nil) })

	perchLabels := []string{"Turdus merula_Common Blackbird"}
	o := &Orchestrator{
		Settings:  settings,
		modelsDir: t.TempDir(),
		models: map[string]*modelEntry{
			RegistryIDPerchV2: {instance: &mockModelInstance{id: RegistryIDPerchV2, labels: perchLabels, numSpecies: len(perchLabels)}},
		},
	}
	o.settingsAtomic.Store(settings)
	o.rangeFilter = newRangeFilterService(nil)
	kept := &fakeUniversalRangeFilter{}
	o.rangeFilter.state.Store(&rangeFilterState{backend: kept, kind: rfKindGeomodelV3, generation: 5})

	require.Error(t, o.rangeFilter.reload(settings, o.rangeFilterView))
	st := o.rangeFilter.loadState()
	assert.Same(t, kept, st.backend, "failed reload keeps the previous backend")
	assert.Equal(t, uint64(5), st.generation, "failed reload does not bump the generation")
	failed := o.RangeFilterStatus()
	assert.True(t, failed.ReloadFailed)
	// The retained geomodel backend was built before Perch loaded, so it does not serve
	// Perch: that is a geomodel problem with install guidance, not a healthy geomodel.
	assert.Equal(t, GeomodelIssueLoadFailed, failed.GeomodelIssue, "retained geomodel backend does not serve the new Perch")

	ok := conf.CloneSettings(settings)
	ok.BirdNET.RangeFilter.Model = ""
	require.NoError(t, o.rangeFilter.reload(ok, o.rangeFilterView))
	assert.False(t, o.RangeFilterStatus().ReloadFailed, "a successful reload clears the flag")
}

// TestReload_FailureWithoutBackendMarksState checks that a failed reload on a service with
// no loaded backend publishes a state that reports the failure, with no backend active.
func TestReload_FailureWithoutBackendMarksState(t *testing.T) {
	settings := conftest.GetTestSettings()
	settings.BirdNET.Labels = nil
	settings.BirdNET.RangeFilter.Model = conf.RangeFilterModelV3
	settings.BirdNET.RangeFilter.ModelPath = ""
	settings.BirdNET.RangeFilter.LabelsPath = ""
	conftest.SetTestSettings(settings)
	t.Cleanup(func() { conftest.SetTestSettings(nil) })

	perchLabels := []string{"Turdus merula_Common Blackbird"}
	o := &Orchestrator{
		Settings:  settings,
		modelsDir: t.TempDir(),
		models: map[string]*modelEntry{
			RegistryIDPerchV2: {instance: &mockModelInstance{id: RegistryIDPerchV2, labels: perchLabels, numSpecies: len(perchLabels)}},
		},
	}
	o.settingsAtomic.Store(settings)
	o.rangeFilter = newRangeFilterService(nil)

	require.Error(t, o.rangeFilter.reload(settings, o.rangeFilterView))
	status := o.RangeFilterStatus()
	assert.False(t, status.Active)
	assert.True(t, status.ReloadFailed)
}

// TestGeomodelParticipantMissing checks that only a geomodel-needing participant absent
// from the backend's built-over set counts as missing; v2.4 never needs the geomodel.
func TestGeomodelParticipantMissing(t *testing.T) {
	perch := participantLabels{id: RegistryIDPerchV2}
	v24 := participantLabels{id: RegistryIDBirdNETV24}
	builtOverPerch := &rangeFilterState{participants: []participantLabels{perch}}
	builtOverV24 := &rangeFilterState{participants: []participantLabels{v24}}

	assert.False(t, geomodelParticipantMissing([]participantLabels{perch}, builtOverPerch), "Perch is in the built-over set")
	assert.False(t, geomodelParticipantMissing([]participantLabels{v24}, builtOverPerch), "v2.4 does not need the geomodel")
	assert.True(t, geomodelParticipantMissing([]participantLabels{v24, perch}, builtOverV24), "Perch loaded after the build")
	assert.True(t, geomodelParticipantMissing([]participantLabels{perch}, nil), "no state covers nothing")
}
