package models

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/api/v2/apitest"
	"github.com/tphakala/birdnet-go/internal/classifier/region"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// expectedRegionCount is the number of tiles each family publishes; the dropdown
// union carries exactly this many because families share a slug set.
const expectedRegionCount = 40

// getRegions calls GetModelRegions directly and decodes the response.
func getRegions(t *testing.T, mutate func(*conf.Settings)) ModelRegionsResponse {
	t.Helper()
	core := apitest.NewCore(t, apitest.WithSettingsFunc(mutate))
	h := New(core, nil)

	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/api/v2/models/regions", http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)
	require.NoError(t, h.GetModelRegions(ctx))
	require.Equal(t, http.StatusOK, rec.Code)

	// The endpoint must never echo the raw coordinates back; only the resolved
	// slug leaves the server. Guard both the latitude and the longitude of the
	// Bogota fixture used below.
	body := rec.Body.String()
	assert.NotContains(t, body, "-74.1", "raw longitude must not leak")
	assert.NotContains(t, body, "4.7", "raw latitude must not leak")

	var resp ModelRegionsResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	return resp
}

// TestGetModelRegions_AutoResolves confirms the auto resolution, the dropdown
// union, the echoed setting, and the per-family projection.
//
// These endpoint tests do NOT use t.Parallel(): the handler reads settings from
// the process-global snapshot that apitest publishes, so parallel tests each
// publishing different coordinates would clobber one another.
func TestGetModelRegions_AutoResolves(t *testing.T) {
	resp := getRegions(t, func(s *conf.Settings) {
		s.BirdNET.Latitude = 4.7 // Bogota
		s.BirdNET.Longitude = -74.1
		s.BirdNET.LocationConfigured = true
		s.BirdNET.ModelRegion = conf.ModelRegionAuto
	})

	assert.Equal(t, conf.ModelRegionAuto, resp.ModelRegion)
	assert.True(t, resp.LocationConfigured)
	assert.Equal(t, "andes", resp.Resolved.Slug, "Bogota resolves to andes")
	assert.Equal(t, string(region.SourceAuto), resp.Resolved.Source)
	assert.Len(t, resp.Regions, expectedRegionCount, "dropdown union covers every tile")

	// The options are sorted and each carries display metadata.
	for _, o := range resp.Regions {
		assert.NotEmpty(t, o.Slug)
		assert.NotEmpty(t, o.Name)
		assert.Contains(t, []int{region.TierContinental, region.TierRegional, region.TierLocal}, o.Tier)
	}

	// The per-family projection resolves each installed-or-available family. The
	// visible catalog carries at least the Perch v2 family (which has a region
	// table), and every family shares geometry, so each resolves to andes here.
	require.NotEmpty(t, resp.Families, "at least one family maps to a region table")
	for _, f := range resp.Families {
		assert.NotEmpty(t, f.CatalogID)
		assert.NotEmpty(t, f.Repo)
		assert.Equal(t, "andes", f.Resolved.Slug, "family %s resolves to andes", f.CatalogID)
	}
}

// TestGetModelRegions_Ambiguous confirms the ambiguous border case surfaces a
// runner-up.
func TestGetModelRegions_Ambiguous(t *testing.T) {
	resp := getRegions(t, func(s *conf.Settings) {
		s.BirdNET.Latitude = 2.0
		s.BirdNET.Longitude = -71.0
		s.BirdNET.LocationConfigured = true
	})
	assert.Equal(t, "andes", resp.Resolved.Slug)
	assert.True(t, resp.Resolved.Ambiguous, "the border point is ambiguous")
	assert.Equal(t, "amazonia", resp.Resolved.RunnerUp)
}

// TestGetModelRegions_GlobalStillPreviews confirms that under the global setting
// the endpoint still reports what auto would resolve to, so the UI can preview a
// mode the user has not saved. The saved mode is echoed separately.
func TestGetModelRegions_GlobalStillPreviews(t *testing.T) {
	resp := getRegions(t, func(s *conf.Settings) {
		s.BirdNET.Latitude = 4.7
		s.BirdNET.Longitude = -74.1
		s.BirdNET.LocationConfigured = true
		s.BirdNET.ModelRegion = conf.ModelRegionGlobal
	})
	assert.Equal(t, conf.ModelRegionGlobal, resp.ModelRegion, "saved mode echoed")
	assert.Equal(t, "andes", resp.Resolved.Slug, "resolved is always the auto preview")
}

// TestGetModelRegions_NoLocation confirms an unconfigured location yields the
// global model even when coordinates that WOULD resolve are present: the gate is
// the LocationConfigured flag, not the geography of the 0,0 default.
func TestGetModelRegions_NoLocation(t *testing.T) {
	resp := getRegions(t, func(s *conf.Settings) {
		s.BirdNET.Latitude = 4.7 // Bogota coordinates that resolve to andes...
		s.BirdNET.Longitude = -74.1
		s.BirdNET.LocationConfigured = false // ...but the location is not configured
	})
	assert.False(t, resp.LocationConfigured)
	assert.Empty(t, resp.Resolved.Slug, "unconfigured location uses the global model")
	assert.Equal(t, string(region.SourceGlobal), resp.Resolved.Source)
	for _, f := range resp.Families {
		assert.Empty(t, f.Resolved.Slug, "family %s is global when location unconfigured", f.CatalogID)
	}
}

// TestModelRegionConstantsMatchResolver is the drift guard for keeping the conf
// mode constants (defined independently so conf stays free of a classifier
// dependency) equal to the resolver's mode vocabulary.
func TestModelRegionConstantsMatchResolver(t *testing.T) {
	t.Parallel()
	assert.Equal(t, region.ModeAuto, conf.ModelRegionAuto, "auto mode constant drifted")
	assert.Equal(t, region.ModeGlobal, conf.ModelRegionGlobal, "global mode constant drifted")
}

// TestResolveRecommendRegion covers the slug the recommender scores against for
// each ModelRegion mode. It mutates process-global settings via WithSettingsFunc,
// so it must not run in parallel.
func TestResolveRecommendRegion(t *testing.T) {
	// A valid slug from the embedded table, for the pinned cases, taken from the
	// data rather than hardcoded so a future slug rename does not silently pass.
	tables, err := region.Tables()
	require.NoError(t, err)
	rep := representativeTable(tables)
	require.NotNil(t, rep)
	var validSlug string
	for slug := range rep.Regions {
		validSlug = slug
		break
	}
	require.NotEmpty(t, validSlug)

	resolve := func(t *testing.T, mutate func(*conf.Settings)) string {
		t.Helper()
		core := apitest.NewCore(t, apitest.WithSettingsFunc(mutate))
		return New(core, nil).resolveRecommendRegion()
	}

	t.Run("auto with configured coordinates resolves a region", func(t *testing.T) {
		got := resolve(t, func(s *conf.Settings) {
			s.BirdNET.Latitude = 4.7 // Bogota, inside a region tile
			s.BirdNET.Longitude = -74.1
			s.BirdNET.LocationConfigured = true
			s.BirdNET.ModelRegion = conf.ModelRegionAuto
		})
		assert.NotEmpty(t, got, "configured coordinates over land resolve to a region")
	})

	t.Run("global mode yields the global model", func(t *testing.T) {
		got := resolve(t, func(s *conf.Settings) {
			s.BirdNET.Latitude = 4.7
			s.BirdNET.Longitude = -74.1
			s.BirdNET.LocationConfigured = true
			s.BirdNET.ModelRegion = conf.ModelRegionGlobal
		})
		assert.Empty(t, got, "global mode opts out of regional selection")
	})

	t.Run("no configured location under auto yields the global model", func(t *testing.T) {
		got := resolve(t, func(s *conf.Settings) {
			s.BirdNET.Latitude = 4.7 // coordinates present but not marked configured
			s.BirdNET.Longitude = -74.1
			s.BirdNET.LocationConfigured = false
			s.BirdNET.ModelRegion = conf.ModelRegionAuto
		})
		assert.Empty(t, got, "auto resolution requires a configured location")
	})

	t.Run("pinned region is honored without a configured location", func(t *testing.T) {
		got := resolve(t, func(s *conf.Settings) {
			s.BirdNET.LocationConfigured = false
			s.BirdNET.ModelRegion = validSlug
		})
		assert.Equal(t, validSlug, got, "an explicit region pin does not require coordinates")
	})
}

// findRegion returns the option with the given slug from the dropdown options.
func findRegion(t *testing.T, regions []RegionOption, slug string) RegionOption {
	t.Helper()
	for i := range regions {
		if regions[i].Slug == slug {
			return regions[i]
		}
	}
	t.Fatalf("region %q not present in options", slug)
	return RegionOption{}
}

// TestGetModelRegions_OptionsCarryCountries confirms the dropdown options carry
// the ISO country codes the UI localizes client-side, for both the core set and
// the partial set (a region that straddles a border).
func TestGetModelRegions_OptionsCarryCountries(t *testing.T) {
	resp := getRegions(t, func(s *conf.Settings) {
		s.BirdNET.Latitude = 4.7 // Bogota (as elsewhere); value is irrelevant here
		s.BirdNET.Longitude = -74.1
		s.BirdNET.LocationConfigured = true
	})

	nordic := findRegion(t, resp.Regions, "nordic")
	assert.NotEmpty(t, nordic.Countries.Core, "core countries populated")
	assert.Contains(t, nordic.Countries.Core, "FI", "Nordic covers Finland")

	britishIsles := findRegion(t, resp.Regions, "british-isles")
	assert.Contains(t, britishIsles.Countries.Core, "GB")
	assert.NotEmpty(t, britishIsles.Countries.Partial, "British Isles has a partial-coverage country")
}

// coverageMapReq issues GET /models/regions/:slug/map for slug with an optional
// If-None-Match header and returns the recorder. The slug is set as a path param
// directly, so malformed values (traversal, casing) exercise the accessor guard.
func coverageMapReq(t *testing.T, slug, ifNoneMatch string) *httptest.ResponseRecorder {
	t.Helper()
	core := apitest.NewCore(t)
	h := New(core, nil)

	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/api/v2/models/regions/"+slug+"/map", http.NoBody)
	if ifNoneMatch != "" {
		req.Header.Set("If-None-Match", ifNoneMatch)
	}
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)
	ctx.SetParamNames("slug")
	ctx.SetParamValues(slug)
	require.NoError(t, h.GetRegionCoverageMap(ctx))
	return rec
}

// TestGetRegionCoverageMap_ServesSVG confirms a known slug serves the themeable
// SVG with a strong ETag and cache header, and that a matching If-None-Match
// yields a bodyless 304.
func TestGetRegionCoverageMap_ServesSVG(t *testing.T) {
	rec := coverageMapReq(t, "nordic", "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, svgContentType, rec.Header().Get("Content-Type"))
	assert.Contains(t, rec.Body.String(), `class="cov"`, "serves a themeable coverage SVG")
	assert.Contains(t, rec.Header().Get("Cache-Control"), "max-age", "sets a cache window")

	etag := rec.Header().Get("ETag")
	require.NotEmpty(t, etag, "sets a strong ETag")

	// A conditional request carrying the same ETag revalidates to 304 with no body.
	notModified := coverageMapReq(t, "nordic", etag)
	assert.Equal(t, http.StatusNotModified, notModified.Code)
	assert.Empty(t, notModified.Body.String(), "304 carries no body")
	assert.Equal(t, etag, notModified.Header().Get("ETag"), "304 still echoes the ETag")
}

// TestGetRegionCoverageMap_NotFound confirms unknown and malformed slugs 404, so
// the UI falls back to its text-only country list.
func TestGetRegionCoverageMap_NotFound(t *testing.T) {
	for _, slug := range []string{"does-not-exist", "../secret", "Nordic", "a/b"} {
		rec := coverageMapReq(t, slug, "")
		assert.Equalf(t, http.StatusNotFound, rec.Code, "slug %q must 404", slug)
	}
}

// TestIfNoneMatch covers the conditional-request matcher directly, including the
// wildcard, comma-separated lists, and the weak-validator form a proxy may send.
func TestIfNoneMatch(t *testing.T) {
	t.Parallel()
	const etag = `"abc123"`
	cases := []struct {
		name   string
		header string
		want   bool
	}{
		{"empty header", "", false},
		{"exact strong match", `"abc123"`, true},
		{"wildcard", "*", true},
		{"weak-validator form", `W/"abc123"`, true},
		{"comma list contains match", `"nope", "abc123"`, true},
		{"comma list contains weak match", `W/"x", W/"abc123"`, true},
		{"no match", `"different"`, false},
		{"prefix-only, not a real tag", `"abc"`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, ifNoneMatch(tc.header, etag), "header %q", tc.header)
		})
	}
}

// TestGetRegionCoverageMap_MismatchedETagServesBody confirms a stale/mismatched
// If-None-Match still gets the full SVG (200), not a 304.
func TestGetRegionCoverageMap_MismatchedETagServesBody(t *testing.T) {
	rec := coverageMapReq(t, "nordic", `"stale-etag"`)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `class="cov"`)
}

// TestGetModelRegions_OptionsAreSorted pins the (groupDisplay, name, slug)
// ordering the endpoint promises. The frontend groups consecutive options by
// their group slug and relies on same-group options being contiguous, so a sort
// regression here would silently break the gallery's grouped region list.
func TestGetModelRegions_OptionsAreSorted(t *testing.T) {
	resp := getRegions(t, func(s *conf.Settings) {
		s.BirdNET.Latitude = 4.7
		s.BirdNET.Longitude = -74.1
		s.BirdNET.LocationConfigured = true
	})
	require.Greater(t, len(resp.Regions), 1, "need multiple options to check ordering")
	for i := 1; i < len(resp.Regions); i++ {
		prev, cur := resp.Regions[i-1], resp.Regions[i]
		switch {
		case prev.GroupDisplay != cur.GroupDisplay:
			assert.Less(t, prev.GroupDisplay, cur.GroupDisplay, "groupDisplay order at index %d", i)
		case prev.Name != cur.Name:
			assert.Less(t, prev.Name, cur.Name, "name order within group at index %d", i)
		default:
			assert.Less(t, prev.Slug, cur.Slug, "slug order at index %d", i)
		}
	}
}
