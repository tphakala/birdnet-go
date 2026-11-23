package models

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/api/v2/apitest"
	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
)

// fakeSpeciesKnowledge is a fixed per-model species snapshot: registry ID to the
// canonical species keys the model can classify. A model absent from the map is
// not loaded.
type fakeSpeciesKnowledge map[string][]string

func (f fakeSpeciesKnowledge) HasModel(modelID string) bool {
	_, ok := f[modelID]
	return ok
}

func (f fakeSpeciesKnowledge) Knows(modelID, key string) bool {
	return slices.Contains(f[modelID], key)
}

// getCoverage calls GetSpeciesCoverage with the given raw query and the fixed
// snapshot, and returns the recorder.
func getCoverage(t *testing.T, mutate func(*conf.Settings), know speciesKnowledge, query url.Values) *httptest.ResponseRecorder {
	t.Helper()
	return getCoverageFrom(t, mutate, func() (speciesKnowledge, error) { return know, nil }, query)
}

// getCoverageFrom is getCoverage with an arbitrary seam, so a test can make it fail.
func getCoverageFrom(t *testing.T, mutate func(*conf.Settings), seam func() (speciesKnowledge, error), query url.Values) *httptest.ResponseRecorder {
	t.Helper()
	core := apitest.NewCore(t, apitest.WithSettingsFunc(mutate))
	h := New(core, nil)
	h.speciesKnowledge = seam

	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/api/v2/models/species-coverage?"+query.Encode(), http.NoBody)
	rec := httptest.NewRecorder()
	require.NoError(t, h.GetSpeciesCoverage(e.NewContext(req, rec)))
	return rec
}

func decodeCoverage(t *testing.T, rec *httptest.ResponseRecorder) SpeciesCoverageResponse {
	t.Helper()
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var resp SpeciesCoverageResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	return resp
}

// TestGetSpeciesCoverage pins the per-species model projection. These tests do
// NOT use t.Parallel(): apitest publishes a process-global settings snapshot.
func TestGetSpeciesCoverage(t *testing.T) {
	const (
		fulica = "fulica atra"
		parus  = "parus major"
	)
	enabled := func(ids ...string) func(*conf.Settings) {
		return func(s *conf.Settings) { s.Models.Enabled = ids }
	}

	t.Run("lists an enabled loaded model that knows the species", func(t *testing.T) {
		resp := decodeCoverage(t, getCoverage(t, enabled(conf.ModelIDPerchV2),
			fakeSpeciesKnowledge{classifier.RegistryIDPerchV2: {fulica}},
			url.Values{"species": {"Fulica atra"}}))
		require.Len(t, resp.Species, 1)
		assert.Equal(t, "Fulica atra", resp.Species[0].Species)
		require.Len(t, resp.Species[0].Models, 1)
		m := resp.Species[0].Models[0]
		assert.Equal(t, conf.ModelIDPerchV2, m.ID)
		assert.Equal(t, classifier.RegistryIDPerchV2, m.RegistryID)
		assert.Equal(t, "Google Perch v2", m.Name)
		assert.Equal(t, "bird", m.TaxonGroup)
		assert.True(t, m.RangeFilterPass, "no location configured: the range filter does not apply")
	})

	t.Run("omits loaded but not enabled, enabled but not loaded, and not knowing models", func(t *testing.T) {
		resp := decodeCoverage(t, getCoverage(t,
			enabled(conf.ModelIDPerchV2, conf.ModelIDBirdNETPlusV3, conf.ModelIDBirdNET),
			fakeSpeciesKnowledge{
				classifier.RegistryIDPerchV2:    {fulica},
				classifier.RegistryIDBirdNETV24: {parus},  // enabled, loaded, does not know the species
				classifier.RegistryIDBSG:        {fulica}, // loaded and knows it, not enabled
				// BirdNET+ is enabled but not loaded.
			},
			url.Values{"species": {"Fulica atra"}}))
		require.Len(t, resp.Species, 1)
		require.Len(t, resp.Species[0].Models, 1)
		assert.Equal(t, classifier.RegistryIDPerchV2, resp.Species[0].Models[0].RegistryID)
	})

	t.Run("range filter pass follows participation and the included list", func(t *testing.T) {
		resp := decodeCoverage(t, getCoverage(t,
			func(s *conf.Settings) {
				s.Models.Enabled = []string{conf.ModelIDPerchV2, conf.ModelIDBat}
				s.BirdNET.LocationConfigured = true
				s.BirdNET.RangeFilter.IncludedScientificNames = map[string]struct{}{parus: {}}
			},
			fakeSpeciesKnowledge{
				classifier.RegistryIDPerchV2: {fulica},
				classifier.RegistryIDBat:     {fulica},
			},
			url.Values{"species": {"Fulica atra"}}))
		require.Len(t, resp.Species, 1)
		byAlias := map[string]bool{}
		for _, m := range resp.Species[0].Models {
			byAlias[m.ID] = m.RangeFilterPass
		}
		assert.Equal(t, map[string]bool{conf.ModelIDPerchV2: false, conf.ModelIDBat: true}, byAlias,
			"a participating model fails outside the included list; a non-participating model always passes")
	})

	t.Run("a participating model passes the range filter for an included species", func(t *testing.T) {
		resp := decodeCoverage(t, getCoverage(t,
			func(s *conf.Settings) {
				s.Models.Enabled = []string{conf.ModelIDPerchV2, conf.ModelIDBat}
				s.BirdNET.LocationConfigured = true
				s.BirdNET.RangeFilter.IncludedScientificNames = map[string]struct{}{fulica: {}}
			},
			fakeSpeciesKnowledge{
				classifier.RegistryIDPerchV2: {fulica},
				classifier.RegistryIDBat:     {fulica},
			},
			url.Values{"species": {"Fulica atra"}}))
		require.Len(t, resp.Species[0].Models, 2)
		for _, m := range resp.Species[0].Models {
			assert.True(t, m.RangeFilterPass, m.ID)
		}
		assert.Equal(t, conf.ModelIDBat, resp.Species[0].Models[0].ID)
		assert.Equal(t, "bat", resp.Species[0].Models[0].TaxonGroup)
	})

	t.Run("reports 500 when the species snapshot is unavailable", func(t *testing.T) {
		rec := getCoverageFrom(t, enabled(conf.ModelIDPerchV2),
			func() (speciesKnowledge, error) { return nil, errors.NewStd("unavailable") },
			url.Values{"species": {"Fulica atra"}})
		assert.Equal(t, http.StatusInternalServerError, rec.Code)
	})

	t.Run("duplicate spellings of one enabled model list it once", func(t *testing.T) {
		resp := decodeCoverage(t, getCoverage(t, enabled("perch_v2", "perch-v2", "PERCH_V2"),
			fakeSpeciesKnowledge{classifier.RegistryIDPerchV2: {fulica}},
			url.Values{"species": {"Fulica atra"}}))
		require.Len(t, resp.Species, 1)
		assert.Len(t, resp.Species[0].Models, 1)
	})

	t.Run("keeps request order, trims names, and returns an empty list for an unknown species", func(t *testing.T) {
		rec := getCoverage(t, enabled(conf.ModelIDPerchV2),
			fakeSpeciesKnowledge{classifier.RegistryIDPerchV2: {fulica}},
			url.Values{"species": {" Parus major ", "Fulica atra", "   "}})
		resp := decodeCoverage(t, rec)
		require.Len(t, resp.Species, 2)
		assert.Equal(t, "Parus major", resp.Species[0].Species)
		assert.Empty(t, resp.Species[0].Models)
		assert.Equal(t, "Fulica atra", resp.Species[1].Species)
		assert.Len(t, resp.Species[1].Models, 1)
		assert.Contains(t, rec.Body.String(), `"models":[]`, "an empty list must serialize as [], not null")
	})

	t.Run("lists several models in alias order", func(t *testing.T) {
		resp := decodeCoverage(t, getCoverage(t,
			enabled(conf.ModelIDPerchV2, conf.ModelIDBirdNET),
			fakeSpeciesKnowledge{
				classifier.RegistryIDPerchV2:    {fulica},
				classifier.RegistryIDBirdNETV24: {fulica},
			},
			url.Values{"species": {"Fulica atra"}}))
		require.Len(t, resp.Species[0].Models, 2)
		assert.Equal(t, conf.ModelIDBirdNET, resp.Species[0].Models[0].ID)
		assert.Equal(t, conf.ModelIDPerchV2, resp.Species[0].Models[1].ID)
	})

	t.Run("rejects a request without a usable species", func(t *testing.T) {
		for name, q := range map[string]url.Values{
			"missing":    {},
			"whitespace": {"species": {"   "}},
		} {
			rec := getCoverage(t, enabled(conf.ModelIDPerchV2), fakeSpeciesKnowledge{}, q)
			assert.Equal(t, http.StatusBadRequest, rec.Code, name)
		}
	})
}
