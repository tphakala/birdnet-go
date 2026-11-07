package classifier

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/conf"
)

// TestBSGBat_Wiring pins that BSG-BAT can be loaded at startup and rebuilt on a
// thread-count change: a registry entry without a loader is recognized but never
// runs, and one without a reload builder keeps its old thread count.
func TestBSGBat_Wiring(t *testing.T) {
	t.Parallel()

	assert.Contains(t, modelLoaders, RegistryIDBSGBat)
	assert.Contains(t, openvinoCapableSecondaryBuilders, RegistryIDBSGBat)
}

// TestBuildBSGBat_NotInstalled verifies that building with no configured files and
// nothing installed fails with the "not installed or configured" error.
func TestBuildBSGBat_NotInstalled(t *testing.T) {
	t.Parallel()

	o := &Orchestrator{}
	o.SetModelsDir(t.TempDir())

	_, _, err := o.buildBSGBat(&conf.Settings{}, 1)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not installed or configured")
}

// TestBuildBSGBat_ResolvesInstalledGalleryPair verifies that a model and labels
// file installed from the catalog entry are found with no configured paths and no
// embeddings file, so the build reaches the runtime step (which fails here on the
// placeholder model file, but no longer with "not installed or configured").
func TestBuildBSGBat_ResolvesInstalledGalleryPair(t *testing.T) {
	t.Parallel()

	entry, ok := GetCatalogEntry("bsg-bat")
	require.True(t, ok)

	modelsDir := t.TempDir()
	installedModel := writeVariantModelFile(t, modelsDir, &entry, "fp32")
	installedLabels := writeVariantLabelsFile(t, modelsDir, &entry, "fp32")

	o := &Orchestrator{}
	o.SetModelsDir(modelsDir)

	res := o.resolveFamilyPaths(RegistryIDBSGBat, modelFileSet{}, false)
	assert.Equal(t, installedModel, res.resolved.model)
	assert.Equal(t, installedLabels, res.resolved.labels)

	_, _, err := o.buildBSGBat(&conf.Settings{}, 1)
	require.Error(t, err, "the placeholder model file cannot load")
	assert.NotContains(t, err.Error(), "not installed or configured")
}
