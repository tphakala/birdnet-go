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
const expectedRegionCount = 39

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
