package conf

import (
	"os"
	"path/filepath"
)

// Shared geomodel filenames. These are defined in the conf package because
// conf cannot import classifier (classifier imports conf). The classifier package
// references these constants to avoid duplication.
const (
	GeomodelONNXLocalName   = "geomodel_v3.0.2_fp16.onnx"
	GeomodelLabelsLocalName = "geomodel_v3.0.2_labels.txt"
)

// rangeFilterGeomodelV3 is the literal that the runtime, status code, and UI
// key off to recognize the geomodel v3 range filter (see
// internal/classifier/birdnet.go and internal/conf/validate_services.go).
const rangeFilterGeomodelV3 = "v3"

// ResolveModelsDir computes the model gallery directory. If Models.Directory is set,
// it is used verbatim; otherwise the OS user config directory is used (falling back to
// <home>/.config when os.UserConfigDir fails), joined with "birdnet-go/models".
// The second return value is false when the directory cannot be resolved, in
// which case callers should do nothing.
func (s *Settings) ResolveModelsDir() (string, bool) {
	if s.Models.Directory != "" {
		return s.Models.Directory, true
	}

	configDir, err := os.UserConfigDir()
	if err != nil {
		homeDir, homeErr := GetUserHomeDir()
		if homeErr != nil {
			return "", false
		}
		configDir = filepath.Join(homeDir, ".config")
	}
	return filepath.Join(configDir, "birdnet-go", "models"), true
}

// MigrateOrphanGeomodelRangeFilter reconciles a persisted geomodel-shaped range
// filter config with the gallery-managed shared files on disk. It is a one-time
// migration applied at config load; the caller persists the result when this
// method returns true.
//
// It only acts when the range filter points at the EXACT gallery-managed shared
// paths (<modelsDir>/shared/<geomodel files>); custom or hand-edited paths are
// never touched. When both shared files exist on disk, the config is promoted
// to Model="v3". When the shared files are absent (an orphaned reference, e.g.
// the user removed the only geomodel-capable model), the geomodel range filter
// fields are cleared so BirdNET v2.4 cleanly falls back to the embedded TFLite
// filter. If the config already matches the on-disk reality, it is a no-op.
//
// Returns true if the config was changed, false otherwise.
func (s *Settings) MigrateOrphanGeomodelRangeFilter() bool {
	modelsDir, ok := s.ResolveModelsDir()
	if !ok {
		return false
	}

	sharedDir := filepath.Join(modelsDir, "shared")
	expectedModelPath := filepath.Join(sharedDir, GeomodelONNXLocalName)
	expectedLabelsPath := filepath.Join(sharedDir, GeomodelLabelsLocalName)

	rf := &s.BirdNET.RangeFilter

	// Only reconcile gallery-managed configs. An exact match on both shared
	// paths is the signal that the gallery owns this config; anything else
	// (custom paths, empty paths) is left alone.
	if rf.ModelPath != expectedModelPath || rf.LabelsPath != expectedLabelsPath {
		return false
	}

	if sharedGeomodelFilesPresent(expectedModelPath, expectedLabelsPath) {
		// Files exist: ensure the config keys off v3. No-op if already v3.
		if rf.Model == rangeFilterGeomodelV3 {
			return false
		}
		rf.Model = rangeFilterGeomodelV3
		return true
	}

	// Files are absent: clear the dead references so v2.4 uses the embedded
	// filter. The gallery paths are still set (the guard above required an exact,
	// non-empty match), so clearing them is always a real change.
	rf.Model = ""
	rf.ModelPath = ""
	rf.LabelsPath = ""
	rf.PassUnmappedSpecies = false
	return true
}

// sharedGeomodelFilesPresent reports whether both shared geomodel files exist
// on disk.
func sharedGeomodelFilesPresent(modelPath, labelsPath string) bool {
	if _, err := os.Stat(modelPath); err != nil {
		return false
	}
	if _, err := os.Stat(labelsPath); err != nil {
		return false
	}
	return true
}
