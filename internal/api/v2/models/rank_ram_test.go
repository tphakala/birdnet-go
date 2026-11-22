package models

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/api/v2/apitest"
	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/classifier/recommend"
	"github.com/tphakala/birdnet-go/internal/classifier/region"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/hwprofile"
	"github.com/tphakala/birdnet-go/internal/inference"
)

// TestRankCatalogUsesRAMUpperBound pins that a host whose exact RAM is unknown
// (cgroup limit unreadable) still gets the ram.insufficient blocker when its
// upper bound is below the variant's floor.
func TestRankCatalogUsesRAMUpperBound(t *testing.T) {
	// apitest.NewCore publishes the process-global settings snapshot, so this
	// test must not run in parallel.

	const (
		catalogID    = "ram-upper-bound"
		variantID    = "needs-4g"
		minRAMMB     = 4096
		upperBoundGB = 1
	)
	entry := classifier.CatalogEntry{
		ID: catalogID,
		Variants: []classifier.CatalogVariant{{
			ID:           variantID,
			Requirements: classifier.VariantRequirements{MinRAMMB: minRAMMB},
			Backends:     map[string]classifier.BackendSupport{hwprofile.CapONNXRuntimeCPU: {Supported: true, Recommended: true}},
		}},
	}

	h := New(apitest.NewCore(t), nil)
	h.hardwareProfile = func(inference.ORTStatus) hwprofile.Profile {
		return hwprofile.Profile{
			Arch:               "amd64",
			RAMUpperBoundBytes: upperBoundGB * 1024 * 1024 * 1024,
			Backends:           hwprofile.Backends{TFLite: hwprofile.BackendStatus{Available: true}},
		}
	}

	byVariant, _, _ := h.rankCatalog([]classifier.CatalogEntry{entry}, inference.ORTStatus{})
	rec, ok := byVariant[catalogID][variantID]
	require.True(t, ok)
	assert.True(t, slices.ContainsFunc(rec.Blockers, func(b recommend.Reason) bool {
		return b.Code == recommend.BlockerRAMInsufficient
	}), "blockers: %v", rec.Blockers)
}

// TestRankCatalogResolvesRegionPerFamily pins that rankCatalog scores each entry
// against the region of its own family's table: for a station in central India the
// Perch family (which ships a central-india tile) recommends that tile, while the
// BirdNET v3.0 family (which does not) recommends the tile its own table resolves
// the same coordinates to, instead of scoring against a slug it has no tile for.
func TestRankCatalogResolvesRegionPerFamily(t *testing.T) {
	// apitest.NewCore publishes the process-global settings snapshot, so this
	// test must not run in parallel.
	const (
		perchRepo = "tphakala/Perch-v2-Models"
		v30Repo   = "tphakala/BirdNET-v3.0-Models"
		ciSlug    = "central-india"
		lat, lon  = 21.0, 79.75
	)
	v30Table, ok := region.TableForRepo(v30Repo)
	require.True(t, ok)
	v30Slug := region.Select(v30Table, region.ModeAuto, lat, lon).Slug
	require.NotEmpty(t, v30Slug, "BirdNET v3.0 resolves central India to one of its own tiles")
	require.NotEqual(t, ciSlug, v30Slug)
	_, hasCI := v30Table.Regions[ciSlug]
	require.False(t, hasCI, "BirdNET v3.0 ships no central-india tile")

	slice := func(slug string) classifier.CatalogVariant {
		return classifier.CatalogVariant{
			ID: "slice@" + slug, Region: slug,
			Backends: map[string]classifier.BackendSupport{hwprofile.CapONNXRuntimeCPU: {Supported: true, Recommended: true}},
		}
	}
	entries := []classifier.CatalogEntry{
		{ID: "perch", HuggingFaceRepo: perchRepo, Variants: []classifier.CatalogVariant{slice(ciSlug), slice(v30Slug)}},
		{ID: "v30", HuggingFaceRepo: v30Repo, Variants: []classifier.CatalogVariant{slice(ciSlug), slice(v30Slug)}},
	}

	core := apitest.NewCore(t, apitest.WithSettingsFunc(func(s *conf.Settings) {
		s.BirdNET.Latitude, s.BirdNET.Longitude = lat, lon
		s.BirdNET.LocationConfigured = true
		s.BirdNET.ModelRegion = conf.ModelRegionAuto
	}))
	h := New(core, nil)
	h.hardwareProfile = func(inference.ORTStatus) hwprofile.Profile {
		return hwprofile.Profile{
			Arch: "amd64", TotalRAMBytes: 8 * 1024 * 1024 * 1024,
			Backends: hwprofile.Backends{ONNX: hwprofile.BackendStatus{Available: true}},
		}
	}

	_, recommended, _ := h.rankCatalog(entries, inference.ORTStatus{Available: true})
	assert.Equal(t, "slice@"+ciSlug, recommended["perch"], "a family with the tile recommends it")
	assert.Equal(t, "slice@"+v30Slug, recommended["v30"], "a family without the tile recommends its own resolved tile")
}

// TestRepresentativeTable pins the rule behind the top-level detected region: the
// table with the most regions wins, a tie goes to the lowest repo id, and no
// tables give nil.
func TestRepresentativeTable(t *testing.T) {
	t.Parallel()
	tbl := func(repo string, slugs ...string) *region.Table {
		regions := make(map[string]region.Region, len(slugs))
		for _, s := range slugs {
			regions[s] = region.Region{}
		}
		return &region.Table{Repo: repo, Regions: regions}
	}
	a, b, c := tbl("a", "x"), tbl("b", "x", "y"), tbl("c", "x", "y")

	assert.Nil(t, representativeTable(nil))
	assert.Nil(t, representativeTable(map[string]*region.Table{"a": nil}), "a nil table is skipped")
	assert.Same(t, b, representativeTable(map[string]*region.Table{"a": b, "b": nil}), "a nil table after a real one is skipped")
	assert.Same(t, b, representativeTable(map[string]*region.Table{"a": a, "b": b}), "the larger table wins over a lower repo id")
	assert.Same(t, b, representativeTable(map[string]*region.Table{"a": a, "b": b, "c": c}), "a tie goes to the lowest repo id")
}

// TestBuildRegionOptions_SkipsNilTable pins that a nil table in the map is skipped
// instead of dereferenced, whatever its position in repo order.
func TestBuildRegionOptions_SkipsNilTable(t *testing.T) {
	t.Parallel()
	present := &region.Table{Repo: "a", Regions: map[string]region.Region{"x": {}}}

	var opts []RegionOption
	require.NotPanics(t, func() {
		opts = buildRegionOptions(map[string]*region.Table{"a": present, "b": nil})
	})
	require.Len(t, opts, 1)
	assert.Equal(t, "x", opts[0].Slug)
}
