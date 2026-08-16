package classifier

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/classifier/region"
)

// TestDetectRegionStaleness_FiresForInstalledRegionalSlice is the end-to-end
// detection test that only became reachable once an installed variant can carry a
// region slug (issue #1439): before the regional catalog existed, every installed
// variant had an empty region, so InstalledRegionalModels never yielded a regional
// entry and DetectRegionStaleness had nothing to flag. It installs the nordic
// slice of BirdNET v3.0 by its on-disk model name, has ScanInstalled derive the
// regional variant, then asserts the detector reports the slice stale when the
// location moves out of its region and stays quiet when it does not.
//
// It lives in the classifier package, not internal/api/v2, on purpose: the
// notification EMISSION for a change is already covered by
// TestNotifyRegionStaleness_EmitsPerChange, and the thin Controller glue by
// TestController_notifyRegionStaleness_Guards, so this test can assert the
// detector output directly without initializing the process-global notification
// singleton (which the api/v2 suite's TestController_SendToast_ServiceNotInitialized
// forbids any api/v2 test from doing).
func TestDetectRegionStaleness_FiresForInstalledRegionalSlice(t *testing.T) {
	t.Parallel()

	entry, ok := GetCatalogEntry("birdnet-v3.0")
	require.True(t, ok)

	// Find fp32@nordic's on-disk model name; installedFromVariant keys detection on
	// the model file, so placing just that file makes ScanInstalled derive the slice.
	var modelName string
	for i := range entry.Variants {
		if entry.Variants[i].ID != "fp32@nordic" {
			continue
		}
		for j := range entry.Variants[i].Files {
			if entry.Variants[i].Files[j].Role == RoleModel {
				modelName = entry.Variants[i].Files[j].LocalName
			}
		}
	}
	require.NotEmpty(t, modelName, "fp32@nordic model LocalName")

	modelsDir := t.TempDir()
	subdir := filepath.Join(modelsDir, entry.ID)
	require.NoError(t, os.MkdirAll(subdir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(subdir, modelName), []byte("dummy-model"), 0o644))

	mm := NewModelManager(modelsDir, nil, nil)
	mm.ScanInstalled()

	vid, installed := mm.InstalledVariantID(entry.ID)
	require.True(t, installed, "scan must detect the regional slice as installed")
	require.Equal(t, "fp32@nordic", vid, "scan must derive the regional variant id from the on-disk model name")

	regionalModels := mm.InstalledRegionalModels()
	var v30 *InstalledModelRegion
	for i := range regionalModels {
		if regionalModels[i].CatalogID == entry.ID {
			v30 = &regionalModels[i]
		}
	}
	require.NotNil(t, v30, "installed v3.0 must appear in the regional-model snapshot")
	require.Equal(t, "nordic", v30.Region, "the installed variant must carry the region slug")

	tables, err := region.Tables()
	require.NoError(t, err)

	helsinki := RegionCoords{Lat: 60.17, Lon: 24.94, Configured: true} // resolves to nordic
	bogota := RegionCoords{Lat: 4.61, Lon: -74.08, Configured: true}   // resolves to andes

	// Moving out of the installed region flags the slice stale, with display names.
	changes := DetectRegionStaleness(tables, regionalModels, region.ModeAuto, helsinki, bogota)
	require.Len(t, changes, 1, "moving nordic -> andes must report the installed slice stale")
	assert.Equal(t, entry.ID, changes[0].CatalogID)
	assert.Equal(t, "Nordic", changes[0].OldRegion, "old region is the installed slice's display name")
	assert.Equal(t, "Andes", changes[0].NewRegion, "new region is the newly resolved display name")

	// Moving from a different region back into the installed region reports
	// nothing. Using a distinct old location (bogota -> andes) rather than an
	// identical re-save isolates the region-match guard (new resolves to the
	// installed region) from the unchanged-coordinates spam guard.
	assert.Empty(t, DetectRegionStaleness(tables, regionalModels, region.ModeAuto, bogota, helsinki),
		"a location that resolves to the installed region must not report staleness")
}
