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
		kind       rangeFilterBackendKind
		filesExist bool
		want       GeomodelIssue
	}{
		{"v2.4 only on legacy config needs no geomodel", conf.RangeFilterSettings{Model: conf.RangeFilterModelLegacy}, false, rfKindMDataV1, false, GeomodelIssueNone},
		{"geomodel backend is healthy", conf.RangeFilterSettings{}, true, rfKindGeomodelV3, true, GeomodelIssueNone},
		{"default config, no files, no backend", conf.RangeFilterSettings{}, true, rfKindNone, false, GeomodelIssueNotInstalled},
		{"default config, no files, v2.4 MData backend", conf.RangeFilterSettings{}, true, rfKindMDataV2, false, GeomodelIssueNotInstalled},
		{"legacy config with files", conf.RangeFilterSettings{Model: conf.RangeFilterModelLegacy}, true, rfKindMDataV1, true, GeomodelIssueNotSelected},
		{"strict ONNX path without labels, with files", conf.RangeFilterSettings{ModelPath: "/m/custom.onnx"}, true, rfKindNone, true, GeomodelIssueNotSelected},
		{"latest with files and no backend", conf.RangeFilterSettings{Model: conf.RangeFilterModelLatest}, true, rfKindNone, true, GeomodelIssueLoadFailed},
		{"explicit v3 with no files", conf.RangeFilterSettings{Model: conf.RangeFilterModelV3}, true, rfKindNone, false, GeomodelIssueLoadFailed},
		{"explicit v3 with MData backend", conf.RangeFilterSettings{Model: conf.RangeFilterModelV3}, true, rfKindMDataV2, false, GeomodelIssueLoadFailed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, geomodelIssueFor(&tt.rf, tt.wants, tt.kind, tt.filesExist))
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
	assert.True(t, o.RangeFilterStatus().ReloadFailed)

	ok := conf.CloneSettings(settings)
	ok.BirdNET.RangeFilter.Model = ""
	require.NoError(t, o.rangeFilter.reload(ok, o.rangeFilterView))
	assert.False(t, o.RangeFilterStatus().ReloadFailed, "a successful reload clears the flag")
}
