package classifier

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tphakala/birdnet-go/internal/conf/conftest"
)

// TestApplyRangeFilterConfigForInstall_ClearsStaleOppositePath verifies that installing a
// geomodel entry clears both range-filter role paths before writing the entry's own files,
// so a half-tuple entry (only one geomodel role, reachable only via a hand-edited catalog)
// cannot leave the opposite path pointing at a stale value from an earlier config. The
// shipped catalog always pairs both roles (geomodelFiles enforces it), so the full-tuple
// case is the byte-identical behavior real users see and is kept here as a regression guard.
func TestApplyRangeFilterConfigForInstall_ClearsStaleOppositePath(t *testing.T) {
	t.Parallel()
	const (
		staleModelPath  = "/stale/previous-model.onnx"
		staleLabelsPath = "/stale/previous-labels.txt"
		modelLocalName  = "geomodel-v3-model.onnx"
		labelsLocalName = "geomodel-v3-labels.txt"
	)

	tests := []struct {
		name          string
		files         []CatalogFile
		wantModelPath func(sharedDir string) string
		wantLabels    func(sharedDir string) string
	}{
		{
			name:          "model-only entry clears the stale labels path",
			files:         []CatalogFile{{Role: RoleGeomodelModel, LocalName: modelLocalName}},
			wantModelPath: func(sharedDir string) string { return filepath.Join(sharedDir, modelLocalName) },
			wantLabels:    func(string) string { return "" },
		},
		{
			name:          "labels-only entry clears the stale model path",
			files:         []CatalogFile{{Role: RoleGeomodelLabels, LocalName: labelsLocalName}},
			wantModelPath: func(string) string { return "" },
			wantLabels:    func(sharedDir string) string { return filepath.Join(sharedDir, labelsLocalName) },
		},
		{
			name: "full tuple sets both paths",
			files: []CatalogFile{
				{Role: RoleGeomodelModel, LocalName: modelLocalName},
				{Role: RoleGeomodelLabels, LocalName: labelsLocalName},
			},
			wantModelPath: func(sharedDir string) string { return filepath.Join(sharedDir, modelLocalName) },
			wantLabels:    func(sharedDir string) string { return filepath.Join(sharedDir, labelsLocalName) },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			modelsDir := t.TempDir()
			sharedDir := filepath.Join(modelsDir, sharedDirName)
			mm := NewModelManager(modelsDir, nil, conftest.GetTestSettings())

			updated := conftest.GetTestSettings()
			rf := updated.RangeFilterConfig()
			// Simulate a full-tuple config left over from an earlier install.
			rf.ModelPath = staleModelPath
			rf.LabelsPath = staleLabelsPath

			entry := &CatalogEntry{GeomodelVersion: geomodelRangeFilterVersion, Files: tt.files}
			mm.applyRangeFilterConfigForInstall(updated, entry)

			assert.Equal(t, geomodelRangeFilterVersion, rf.Model, "the geomodel version must be written")
			assert.Equal(t, tt.wantModelPath(sharedDir), rf.ModelPath,
				"model path must be the entry's shared model file, or cleared when the entry omits the model role")
			assert.Equal(t, tt.wantLabels(sharedDir), rf.LabelsPath,
				"labels path must be the entry's shared labels file, or cleared when the entry omits the labels role")
		})
	}
}

// TestApplyRangeFilterConfigForInstall_NonGeomodelEntryIsNoop verifies that installing a
// model that carries no geomodel files leaves a pre-existing range-filter config untouched
// (the early return), so a standard acoustic-model install never wipes a legitimately
// configured range filter.
func TestApplyRangeFilterConfigForInstall_NonGeomodelEntryIsNoop(t *testing.T) {
	t.Parallel()
	modelsDir := t.TempDir()
	mm := NewModelManager(modelsDir, nil, conftest.GetTestSettings())

	updated := conftest.GetTestSettings()
	rf := updated.RangeFilterConfig()
	rf.Model = geomodelRangeFilterVersion
	rf.ModelPath = "/existing/geomodel.onnx"
	rf.LabelsPath = "/existing/geomodel-labels.txt"

	// A plain acoustic-model entry: a classifier model role, no geomodel files.
	entry := &CatalogEntry{Files: []CatalogFile{{Role: RoleModel, LocalName: "classifier.onnx"}}}
	mm.applyRangeFilterConfigForInstall(updated, entry)

	assert.Equal(t, geomodelRangeFilterVersion, rf.Model, "a non-geomodel install must not touch the range-filter version")
	assert.Equal(t, "/existing/geomodel.onnx", rf.ModelPath, "a non-geomodel install must not touch the model path")
	assert.Equal(t, "/existing/geomodel-labels.txt", rf.LabelsPath, "a non-geomodel install must not touch the labels path")
}
