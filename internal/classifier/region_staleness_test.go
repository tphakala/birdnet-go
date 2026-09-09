package classifier

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/classifier/region"
	"github.com/tphakala/birdnet-go/internal/notification"
)

// perchRepo is the HuggingFace repo whose embedded region table the detector
// tests resolve against. Using the real shipped snapshot (rather than synthetic
// boxes) keeps the geometry anchors honest.
const perchRepo = "tphakala/Perch-v2-Models"

// Coordinate anchors, verified against the embedded Perch snapshot.
const (
	helsinkiLat, helsinkiLon = 60.17, 24.94  // -> nordic
	osloLat, osloLon         = 59.91, 10.75  // -> nordic
	bogotaLat, bogotaLon     = 4.61, -74.08  // -> andes
	madridLat, madridLon     = 40.4, -3.7    // -> iberia
	oceanLat, oceanLon       = -40.0, -130.0 // South Pacific -> no tile (global)
)

func perchTables(t *testing.T) map[string]*region.Table {
	t.Helper()
	tbl, ok := region.TableForRepo(perchRepo)
	require.True(t, ok, "embedded Perch region table must be present")
	require.NotNil(t, tbl)
	return map[string]*region.Table{perchRepo: tbl}
}

// installed builds a one-model snapshot on the Perch family with the given
// installed variant region slug.
func installed(regionSlug string) []InstalledModelRegion {
	return []InstalledModelRegion{{
		CatalogID: "perch-v2",
		ModelName: "Perch v2",
		Repo:      perchRepo,
		Region:    regionSlug,
	}}
}

func configured(lat, lon float64) RegionCoords {
	return RegionCoords{Lat: lat, Lon: lon, Configured: true}
}

// TestDetectRegionStaleness_ResolutionAnchors documents the city->region anchors
// the other cases rely on, so a snapshot rebanding fails here first with a clear
// message rather than as a confusing miss elsewhere.
func TestDetectRegionStaleness_ResolutionAnchors(t *testing.T) {
	t.Parallel()
	tbl, ok := region.TableForRepo(perchRepo)
	require.True(t, ok)
	assert.Equal(t, "nordic", region.Select(tbl, region.ModeAuto, helsinkiLat, helsinkiLon).Slug, "Helsinki")
	assert.Equal(t, "nordic", region.Select(tbl, region.ModeAuto, osloLat, osloLon).Slug, "Oslo")
	assert.Equal(t, "andes", region.Select(tbl, region.ModeAuto, bogotaLat, bogotaLon).Slug, "Bogota")
	assert.Equal(t, "iberia", region.Select(tbl, region.ModeAuto, madridLat, madridLon).Slug, "Madrid")
	assert.Empty(t, region.Select(tbl, region.ModeAuto, oceanLat, oceanLon).Slug, "South Pacific -> global")
}

func TestDetectRegionStaleness(t *testing.T) {
	t.Parallel()
	tables := perchTables(t)

	tests := []struct {
		name        string
		installed   []InstalledModelRegion
		modelRegion string // passed verbatim; "" exercises the empty-as-auto contract
		old         RegionCoords
		next        RegionCoords
		wantCount   int
		wantOld     string // display name, checked when wantCount == 1
		wantNew     string
	}{
		{
			name:        "coordinate flip triggers",
			installed:   installed("nordic"),
			modelRegion: region.ModeAuto,
			old:         configured(helsinkiLat, helsinkiLon),
			next:        configured(bogotaLat, bogotaLon),
			wantCount:   1, wantOld: "Nordic", wantNew: "Andes",
		},
		{
			name:        "resolves back to installed region",
			installed:   installed("nordic"),
			modelRegion: region.ModeAuto,
			old:         configured(bogotaLat, bogotaLon), // was andes
			next:        configured(helsinkiLat, helsinkiLon),
			wantCount:   0,
		},
		{
			name:        "pre-existing mismatch, resolution unchanged",
			installed:   installed("iberia"), // user is in the nordic area with iberia installed
			modelRegion: region.ModeAuto,
			old:         configured(helsinkiLat, helsinkiLon),
			next:        configured(osloLat, osloLon), // still nordic
			wantCount:   0,
		},
		{
			name:        "pinned mode never triggers",
			installed:   installed("nordic"),
			modelRegion: "iberia",
			old:         configured(helsinkiLat, helsinkiLon),
			next:        configured(bogotaLat, bogotaLon),
			wantCount:   0,
		},
		{
			name:        "global mode never triggers",
			installed:   installed("nordic"),
			modelRegion: region.ModeGlobal,
			old:         configured(helsinkiLat, helsinkiLon),
			next:        configured(bogotaLat, bogotaLon),
			wantCount:   0,
		},
		{
			name:        "empty mode treated as auto",
			installed:   installed("nordic"),
			modelRegion: "", // the back-compat contract: "" behaves like auto
			old:         configured(helsinkiLat, helsinkiLon),
			next:        configured(bogotaLat, bogotaLon),
			wantCount:   1, wantOld: "Nordic", wantNew: "Andes",
		},
		{
			name:        "hardware/global variant skipped",
			installed:   installed(""), // empty region slug
			modelRegion: region.ModeAuto,
			old:         configured(helsinkiLat, helsinkiLon),
			next:        configured(bogotaLat, bogotaLon),
			wantCount:   0,
		},
		{
			name: "family without table skipped",
			installed: []InstalledModelRegion{{
				CatalogID: "unknown", ModelName: "Unknown", Repo: "tphakala/Not-A-Real-Repo", Region: "nordic",
			}},
			modelRegion: region.ModeAuto,
			old:         configured(helsinkiLat, helsinkiLon),
			next:        configured(bogotaLat, bogotaLon),
			wantCount:   0,
		},
		{
			name:        "new location unconfigured skips",
			installed:   installed("nordic"),
			modelRegion: region.ModeAuto,
			old:         configured(helsinkiLat, helsinkiLon),
			next:        RegionCoords{Lat: bogotaLat, Lon: bogotaLon, Configured: false},
			wantCount:   0,
		},
		{
			name:        "first-time location set fires",
			installed:   installed("iberia"),
			modelRegion: region.ModeAuto,
			old:         RegionCoords{Configured: false},
			next:        configured(helsinkiLat, helsinkiLon), // resolves nordic, differs from iberia
			wantCount:   1, wantOld: "Iberia", wantNew: "Nordic",
		},
		{
			name:        "first-time location set resolving to global fires as region-to-global",
			installed:   installed("iberia"),
			modelRegion: region.ModeAuto,
			old:         RegionCoords{Configured: false},
			next:        configured(oceanLat, oceanLon),    // outside every tile; iberia no longer covers it
			wantCount:   1, wantOld: "Iberia", wantNew: "", // NewRegion "" => global message
		},
		{
			name:        "configured region to global fires as region-to-global",
			installed:   installed("nordic"),
			modelRegion: region.ModeAuto,
			old:         configured(helsinkiLat, helsinkiLon),
			next:        configured(oceanLat, oceanLon),
			wantCount:   1, wantOld: "Nordic", wantNew: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			changes := DetectRegionStaleness(tables, tt.installed, tt.modelRegion, tt.old, tt.next)
			require.Len(t, changes, tt.wantCount)
			if tt.wantCount == 1 {
				assert.Equal(t, tt.wantOld, changes[0].OldRegion)
				assert.Equal(t, tt.wantNew, changes[0].NewRegion)
			}
		})
	}
}

// TestDetectRegionStaleness_MultipleFamiliesIndependent verifies each installed
// model is judged on its own, and one flipping does not drag the others in.
func TestDetectRegionStaleness_MultipleFamiliesIndependent(t *testing.T) {
	t.Parallel()
	tables := perchTables(t)
	in := []InstalledModelRegion{
		{CatalogID: "a", ModelName: "A", Repo: perchRepo, Region: "nordic"}, // Helsinki->Bogota flips nordic->andes
		{CatalogID: "b", ModelName: "B", Repo: perchRepo, Region: "andes"},  // Bogota still resolves andes: no change
	}
	changes := DetectRegionStaleness(tables, in, region.ModeAuto,
		configured(helsinkiLat, helsinkiLon), configured(bogotaLat, bogotaLon))
	require.Len(t, changes, 1)
	assert.Equal(t, "a", changes[0].CatalogID)
	assert.Equal(t, "Nordic", changes[0].OldRegion)
	assert.Equal(t, "Andes", changes[0].NewRegion)
}

// TestModelManager_InstalledRegionalModels verifies the snapshot maps an
// installed entry's fields through and skips uninstalled ones. A hardware variant
// (fp32) carries no regional slug, so Region is empty; the detector then treats
// it as non-regional.
func TestModelManager_InstalledRegionalModels(t *testing.T) {
	t.Parallel()

	entry, ok := GetCatalogEntry("perch-v2")
	require.True(t, ok, "expected perch-v2 catalog entry to exist")
	require.NotEmpty(t, entry.Variants, "perch-v2 must be a variant entry")

	mm := NewModelManager(t.TempDir(), nil, nil)
	mm.installed[entry.ID] = InstalledModel{CatalogID: entry.ID, VariantID: "fp32"}

	got := mm.InstalledRegionalModels()

	var perch *InstalledModelRegion
	for i := range got {
		assert.True(t, mm.IsInstalled(got[i].CatalogID), "only installed models should be listed")
		if got[i].CatalogID == entry.ID {
			perch = &got[i]
		}
	}
	require.NotNil(t, perch, "installed perch-v2 should be listed")
	assert.Equal(t, entry.Name, perch.ModelName)
	assert.Equal(t, entry.HuggingFaceRepo, perch.Repo)
	assert.Empty(t, perch.Region, "a hardware variant carries no regional slug")
}

// TestVariantRegion covers the shared catalog helper's branches, including the
// nil-entry and unknown-id guards the detector snapshot relies on.
func TestVariantRegion(t *testing.T) {
	t.Parallel()
	entry := &CatalogEntry{Variants: []CatalogVariant{
		{ID: "fp32@nordic", Region: "nordic"},
		{ID: "fp32", Region: ""},
	}}
	assert.Equal(t, "nordic", VariantRegion(entry, "fp32@nordic"), "regional variant returns its slug")
	assert.Empty(t, VariantRegion(entry, "fp32"), "hardware variant has no region")
	assert.Empty(t, VariantRegion(entry, "unknown"), "unknown id returns empty")
	assert.Empty(t, VariantRegion(entry, ""), "empty id (flat entry) returns empty")
	assert.Empty(t, VariantRegion(nil, "fp32@nordic"), "nil entry is safe")
}

// TestRegionDisplayName covers the slug-to-name mapping, including the
// unknown-slug and nil-table fallbacks that the anchors above never reach.
func TestRegionDisplayName(t *testing.T) {
	t.Parallel()
	tbl, ok := region.TableForRepo(perchRepo)
	require.True(t, ok)
	assert.Equal(t, "Nordic", regionDisplayName(tbl, "nordic"), "known slug maps to its name")
	assert.Equal(t, "no-such-slug", regionDisplayName(tbl, "no-such-slug"), "unknown slug falls back to itself")
	assert.Empty(t, regionDisplayName(tbl, ""), "empty slug stays empty (global)")
	assert.Equal(t, "nordic", regionDisplayName(nil, "nordic"), "nil table falls back to the slug")
}

func TestNotifyRegionStaleness_NilChangesAndNilServiceSafe(t *testing.T) {
	// Force the process-global service to its uninitialized state so this test is
	// order-independent under -shuffle: EmitsPerChange initializes the singleton via
	// a sync.Once that otherwise stays fired for the rest of the run. Reset on
	// cleanup too, so this test never leaks its uninitialized state onto a later one.
	notification.ResetForTest()
	t.Cleanup(notification.ResetForTest)
	require.Nil(t, notification.GetService(), "nil-service test requires an uninitialized notification service")

	assert.NotPanics(t, func() { NotifyRegionStaleness(nil) })
	assert.NotPanics(t, func() {
		NotifyRegionStaleness([]RegionStalenessChange{
			{CatalogID: "perch-v2", ModelName: "Perch v2", OldRegion: "Nordic", NewRegion: "Andes"},
		})
	})
}

// TestNotifyRegionStaleness_EmitsPerChange checks the two message-key branches
// against a real service. It initializes the process-global service (once), so
// it filters emitted notifications by the region title key to stay robust to any
// other notifications present.
func TestNotifyRegionStaleness_EmitsPerChange(t *testing.T) {
	// Reset first so Initialize constructs a fresh service even if a prior test under
	// -shuffle already fired the singleton's sync.Once, and reset again on cleanup so
	// the initialized singleton never leaks into other tests. Cleanups run LIFO, so
	// svc.Stop (registered later) runs before ResetForTest: the goroutine is stopped
	// before the instance is cleared.
	notification.ResetForTest()
	t.Cleanup(notification.ResetForTest)
	notification.Initialize(notification.DefaultServiceConfig())
	svc := notification.GetService()
	require.NotNil(t, svc)
	// Stop the service's cleanup goroutine so the package goleak check stays clean.
	// Several tests in this package start the process-global notification service;
	// each is responsible for stopping the instance it starts.
	t.Cleanup(svc.Stop)

	NotifyRegionStaleness([]RegionStalenessChange{
		{CatalogID: "perch-v2", ModelName: "Perch v2", OldRegion: "Nordic", NewRegion: "Andes"},
		{CatalogID: "perch-v2", ModelName: "Perch v2", OldRegion: "Nordic", NewRegion: ""},
	})

	list, err := svc.List(nil)
	require.NoError(t, err)

	var regionMsg, globalMsg, allBell bool
	count := 0
	allBell = true
	for _, n := range list {
		if n.TitleKey != notification.MsgModelRegionStaleTitle {
			continue
		}
		count++
		switch n.MessageKey {
		case notification.MsgModelRegionStaleMessage:
			regionMsg = true
		case notification.MsgModelRegionStaleGlobalMessage:
			globalMsg = true
		}
		if n.Type != notification.TypeWarning || n.Priority != notification.PriorityMedium {
			allBell = false
		}
	}
	assert.GreaterOrEqual(t, count, 2, "both staleness notifications should be stored")
	assert.True(t, regionMsg, "region-to-region change should use the region message key")
	assert.True(t, globalMsg, "region-to-global change should use the global message key")
	assert.True(t, allBell, "notifications should be warning/medium")
}
