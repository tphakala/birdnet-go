package classifier

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// testLabelsEnvVar is the environment variable the external-label tests use to
// exercise os.ExpandEnv path expansion in loadExternalLabels.
const testLabelsEnvVar = "BIRDNET_TEST_LABELS_DIR"

// newExternalLabelBirdNET builds a minimal BirdNET wired to load labels from the
// given external label path, without invoking the full NewBirdNET model load.
func newExternalLabelBirdNET(labelPath string) *BirdNET {
	settings := &conf.Settings{}
	settings.BirdNET.LabelPath = labelPath
	return &BirdNET{
		Settings:     settings,
		speciesCache: make(map[string]*speciesCacheEntry),
		ModelInfo:    ModelInfo{ID: "BirdNET_V2.4", Name: "BirdNET v2.4"},
	}
}

const twoLabelFile = "Turdus merula_Common Blackbird\nParus major_Great Tit\n"

var twoLabelsExpected = []string{
	"Turdus merula_Common Blackbird",
	"Parus major_Great Tit",
}

// TestLoadExternalLabels_LiteralPath is the regression guard ensuring the new
// path-expansion logic does not break an ordinary (non-tilde, non-env) label
// path: a plain absolute path must still load correctly.
func TestLoadExternalLabels_LiteralPath(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	labelPath := filepath.Join(dir, "labels.txt")
	require.NoError(t, os.WriteFile(labelPath, []byte(twoLabelFile), 0o644))

	bn := newExternalLabelBirdNET(labelPath)
	require.NoError(t, bn.loadLabels())
	assert.Equal(t, twoLabelsExpected, bn.Settings.BirdNET.Labels)
}

// TestLoadExternalLabels_ExpandsEnvVar verifies that loadExternalLabels expands
// an environment variable embedded in the label path via os.ExpandEnv before
// opening the file. This is the behavior introduced by the change under review.
func TestLoadExternalLabels_ExpandsEnvVar(t *testing.T) {
	// Not parallel: t.Setenv mutates process environment.
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "labels.txt"), []byte(twoLabelFile), 0o644))

	t.Setenv(testLabelsEnvVar, dir)

	bn := newExternalLabelBirdNET(filepath.Join("$"+testLabelsEnvVar, "labels.txt"))
	require.NoError(t, bn.loadLabels(), "loadExternalLabels must expand $VAR in the label path")
	assert.Equal(t, twoLabelsExpected, bn.Settings.BirdNET.Labels)
}

// TestLoadExternalLabels_MissingPathReportsExpandedPath verifies that when the
// expanded path does not exist, loading fails (rather than silently succeeding)
// and the error carries the expanded path, not the raw template.
func TestLoadExternalLabels_MissingPathReportsExpandedPath(t *testing.T) {
	// Not parallel: t.Setenv mutates process environment.
	dir := t.TempDir()
	t.Setenv(testLabelsEnvVar, dir)

	missing := filepath.Join(dir, "does-not-exist.txt")
	bn := newExternalLabelBirdNET(filepath.Join("$"+testLabelsEnvVar, "does-not-exist.txt"))
	err := bn.loadLabels()
	require.Error(t, err, "loading a non-existent external label file must fail")
	assert.Contains(t, err.Error(), missing,
		"error context should reference the expanded path, not the unexpanded $VAR template")
}

// TestLoadLabels_RefreshesModelInfoNumSpecies verifies loadLabels refreshes the
// cached ModelInfo.NumSpecies to the actual loaded label count, so a stale stock
// count seeded from the registry template (e.g. 6523 for BirdNET v2.4 vs the real
// 6522) is corrected, and leaves it untouched when loading fails. This keeps
// o.ModelInfo / PrimaryModelInfo() reporting the live count.
func TestLoadLabels_RefreshesModelInfoNumSpecies(t *testing.T) {
	t.Parallel()

	const staleStockCount = 6523

	t.Run("refreshes to the loaded label count", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		labelPath := filepath.Join(dir, "labels.txt")
		require.NoError(t, os.WriteFile(labelPath, []byte(twoLabelFile), 0o644))

		bn := newExternalLabelBirdNET(labelPath)
		bn.ModelInfo.NumSpecies = staleStockCount // as the registry template would seed it

		require.NoError(t, bn.loadLabels())
		require.Len(t, bn.Settings.BirdNET.Labels, 2)
		assert.Equal(t, 2, bn.ModelInfo.NumSpecies,
			"loadLabels must refresh ModelInfo.NumSpecies to the loaded label count, not keep the stale template value")
		assert.Equal(t, bn.NumSpecies(), bn.ModelInfo.NumSpecies,
			"ModelInfo.NumSpecies must match the live NumSpecies() count")
	})

	t.Run("leaves NumSpecies untouched when loading fails", func(t *testing.T) {
		t.Parallel()
		bn := newExternalLabelBirdNET(filepath.Join(t.TempDir(), "does-not-exist.txt"))
		bn.ModelInfo.NumSpecies = staleStockCount

		require.Error(t, bn.loadLabels())
		assert.Equal(t, staleStockCount, bn.ModelInfo.NumSpecies,
			"a failed label load must not clobber the previous NumSpecies")
	})
}
