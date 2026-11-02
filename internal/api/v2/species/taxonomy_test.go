// taxonomy_test.go: tests for taxonomy API endpoints.

package species

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/api/v2/apitest"
	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/ebird"
	"github.com/tphakala/birdnet-go/internal/errors"
)

// loadSharedTaxonomyDB parses the embedded taxonomy database once per test binary.
// The tests only read it, so every handler can share the one instance.
var loadSharedTaxonomyDB = sync.OnceValues(classifier.LoadTaxonomyDatabase)

// newTaxonomyHandler builds a species Handler backed by the embedded taxonomy database.
func newTaxonomyHandler(t *testing.T) *Handler {
	t.Helper()
	taxonomyDB, err := loadSharedTaxonomyDB()
	require.NoError(t, err, "Failed to load taxonomy database")
	c := &Handler{Core: &apicore.Core{TaxonomyDB: taxonomyDB}}
	c.Settings.Store(apitest.NewValidTestSettings())
	return c
}

// serveTaxonomyParam runs a taxonomy handler that reads one path parameter and
// returns the recorder. The handler is expected to write its own response, so a
// returned error fails the test.
func serveTaxonomyParam(t *testing.T, handler func(echo.Context) error, path, param, value string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path+value, http.NoBody)
	rec := httptest.NewRecorder()
	ctx := echo.New().NewContext(req, rec)
	ctx.SetParamNames(param)
	ctx.SetParamValues(value)
	require.NoError(t, handler(ctx))
	return rec
}

// requireStaticJSON asserts a 200 JSON response carrying the static-data cache
// headers shared by the taxonomy lookups, and returns the decoded body.
func requireStaticJSON(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Header().Get("Content-Type"), "application/json")
	assert.Equal(t, "public, max-age=86400", rec.Header().Get("Cache-Control"))
	assert.Equal(t, "Accept-Encoding", rec.Header().Get("Vary"))

	var resp map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp), "Failed to parse JSON response")
	return resp
}

// TestGetGenusSpecies tests the GET /api/v2/taxonomy/genus/:genus endpoint
func TestGetGenusSpecies(t *testing.T) {
	t.Parallel()

	c := newTaxonomyHandler(t)

	tests := []struct {
		name        string
		genus       string
		wantGenus   string
		minSpecies  int
		wantMessage string // set for the not-found row
	}{
		{name: "valid genus - corvus", genus: "corvus", wantGenus: "Corvus", minSpecies: 10},
		{name: "valid genus - turdus", genus: "turdus", wantGenus: "Turdus", minSpecies: 50},
		{name: "case insensitive - CORVUS", genus: "CORVUS", wantGenus: "Corvus", minSpecies: 10},
		{name: "nonexistent genus", genus: "nonexistentgenus", wantMessage: "Genus not found"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := serveTaxonomyParam(t, c.GetGenusSpecies, "/api/v2/taxonomy/genus/", "genus", tt.genus)

			if tt.wantMessage != "" {
				apitest.AssertControllerError(t, nil, rec, http.StatusNotFound, tt.wantMessage)
				return
			}

			resp := requireStaticJSON(t, rec)
			assert.Equal(t, tt.wantGenus, resp["genus"], "genus is returned in proper case")
			species, ok := resp["species"].([]any)
			require.True(t, ok, "Expected species array")
			assert.GreaterOrEqual(t, len(species), tt.minSpecies, "Expected at least %d species", tt.minSpecies)
		})
	}
}

// TestGetFamilySpecies tests the GET /api/v2/taxonomy/family/:family endpoint
func TestGetFamilySpecies(t *testing.T) {
	t.Parallel()

	c := newTaxonomyHandler(t)

	tests := []struct {
		name        string
		family      string
		minSpecies  int
		wantMessage string // set for the not-found row
	}{
		{name: "owls - strigidae", family: "strigidae", minSpecies: 200},
		{name: "corvids - corvidae", family: "corvidae", minSpecies: 100},
		{name: "case insensitive", family: "STRIGIDAE", minSpecies: 200},
		{name: "nonexistent family", family: "nonexistentfamily", wantMessage: "Family not found"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := serveTaxonomyParam(t, c.GetFamilySpecies, "/api/v2/taxonomy/family/", "family", tt.family)

			if tt.wantMessage != "" {
				apitest.AssertControllerError(t, nil, rec, http.StatusNotFound, tt.wantMessage)
				return
			}

			resp := requireStaticJSON(t, rec)
			species, ok := resp["species"].([]any)
			require.True(t, ok, "Expected species array in response")
			assert.GreaterOrEqual(t, len(species), tt.minSpecies,
				"Expected at least %d species in family %s", tt.minSpecies, tt.family)
		})
	}
}

// TestGetSpeciesTree tests the GET /api/v2/taxonomy/tree/:scientific_name endpoint
func TestGetSpeciesTree(t *testing.T) {
	t.Parallel()

	c := newTaxonomyHandler(t)

	tests := []struct {
		name           string
		scientificName string
		wantGenus      string
		wantFamily     string
		wantOrder      string
		wantMessage    string // set for the not-found row
	}{
		{name: "american robin", scientificName: "Turdus migratorius", wantGenus: "Turdus", wantFamily: "Turdidae", wantOrder: "Passeriformes"},
		{name: "common raven", scientificName: "Corvus corax", wantGenus: "Corvus", wantFamily: "Corvidae", wantOrder: "Passeriformes"},
		{name: "great horned owl", scientificName: "Bubo virginianus", wantGenus: "Bubo", wantFamily: "Strigidae", wantOrder: "Strigiformes"},
		{name: "nonexistent species", scientificName: "Nonexistent species", wantMessage: "Species not found"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// The name travels URL-encoded in the path and as the raw parameter value.
			rec := serveTaxonomyParam(t, c.GetSpeciesTree, "/api/v2/taxonomy/tree/", "scientific_name", url.PathEscape(tt.scientificName))

			if tt.wantMessage != "" {
				apitest.AssertControllerError(t, nil, rec, http.StatusNotFound, tt.wantMessage)
				return
			}

			resp := requireStaticJSON(t, rec)
			tree, ok := resp["taxonomy_tree"].(map[string]any)
			require.True(t, ok, "Expected taxonomy_tree in response")

			assert.Equal(t, tt.wantGenus, tree["genus"])
			assert.Equal(t, tt.wantFamily, tree["family"])
			assert.Equal(t, tt.wantOrder, tree["order"])
			assert.Equal(t, "Animalia", tree["kingdom"])
			assert.Equal(t, "Chordata", tree["phylum"])
			assert.Equal(t, "Aves", tree["class"])
		})
	}
}

// TestGetSpeciesTaxonomyLocalDB tests the main taxonomy endpoint with local DB
func TestGetSpeciesTaxonomyLocalDB(t *testing.T) {
	t.Parallel()

	c := newTaxonomyHandler(t)

	tests := []struct {
		name           string
		scientificName string
		wantFamily     string
	}{
		{name: "local db lookup - american robin", scientificName: "Turdus migratorius", wantFamily: "Turdidae"},
		{name: "local db lookup - common raven", scientificName: "Corvus corax", wantFamily: "Corvidae"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Call the internal method directly since we need context
			info, err := c.getDetailedTaxonomy(t.Context(), tt.scientificName, "", false, true)
			require.NoError(t, err)
			require.NotNil(t, info, "Expected non-nil taxonomy info")

			assert.Equal(t, tt.wantFamily, info.Taxonomy.Family)
			assert.Contains(t, []any{"local", "local+ebird"}, info.Metadata["source"],
				"Expected source to be 'local' or 'local+ebird'")
		})
	}
}

// TestGetSpeciesTaxonomyWithoutLocalDB verifies that a nil embedded taxonomy
// database is surfaced as an accurate system fault, not the old misleading
// "no local database or eBird API" configuration message (#4105). The embedded
// database is compiled in via go:embed and always loads in a healthy binary, so a
// nil database is a build/deploy defect worth an error, distinct from a normal
// per-species miss (which degrades gracefully, see the tests below).
func TestGetSpeciesTaxonomyWithoutLocalDB(t *testing.T) {
	t.Parallel()

	c := &Handler{Core: &apicore.Core{TaxonomyDB: nil}}
	c.Settings.Store(apitest.NewValidTestSettings())
	require.Nil(t, c.EBird(), "eBird must be off for this test")

	_, err := c.getDetailedTaxonomy(t.Context(), "Turdus migratorius", "", false, true)
	require.Error(t, err, "a nil taxonomy database must surface a system error")
	assert.True(t, errors.IsCategory(err, errors.CategorySystem),
		"a nil DB is a system fault, not a configuration miss")
	assert.NotContains(t, err.Error(), "no local database",
		"the misleading 'no local database' wording must be gone")
}

// TestGetSpeciesTaxonomyNilDBWithEBirdConfigured verifies that a nil embedded
// database fails fast as a system fault even when eBird is configured, i.e. the
// nil-DB check is not masked by the eBird fallback (#4105). The eBird path is never
// reached (no network call), because the nil-DB check short-circuits first.
func TestGetSpeciesTaxonomyNilDBWithEBirdConfigured(t *testing.T) {
	// Not parallel: mutates the global conf singleton so ReconfigureEBird can build
	// a client. Snapshot and restore it around the test.
	orig := conf.GetSettings()
	t.Cleanup(func() { conf.StoreSettings(orig) })

	settings := apitest.NewValidTestSettings()
	settings.Realtime.EBird.Enabled = true
	settings.Realtime.EBird.APIKey = "test-api-key"
	conf.StoreSettings(settings)

	c := &Handler{Core: &apicore.Core{TaxonomyDB: nil}}
	c.Settings.Store(settings)
	require.NotNil(t, c.ReconfigureEBird(), "eBird client must build from the configured key")
	require.NotNil(t, c.EBird(), "eBird client must be available after configuring it")

	_, err := c.getDetailedTaxonomy(t.Context(), "Turdus migratorius", "", false, true)
	require.Error(t, err, "a nil DB must fail even when eBird is configured")
	assert.True(t, errors.IsCategory(err, errors.CategorySystem),
		"a nil DB must fail fast as a system fault, not be masked by the eBird path")
}

// TestGetSpeciesTaxonomyLocalMissDegradesGracefully verifies that a species absent
// from the embedded taxonomy, with eBird off, degrades to an "unresolved" 200
// response instead of hard-erroring (#4105). No taxonomy hierarchy is fabricated:
// the requested name may be a non-organism noise label, so not even kingdom or
// genus is safe to assert, and include_hierarchy makes no difference to the
// fallback.
func TestGetSpeciesTaxonomyLocalMissDegradesGracefully(t *testing.T) {
	t.Parallel()

	c := newTaxonomyHandler(t)
	require.Nil(t, c.EBird(), "eBird must be off for this test")

	// A validly-formatted binomial (len >= 3, contains a space) guaranteed to be
	// absent from the frozen embedded snapshot.
	const missName = "Zzyzxus fictus"

	for _, includeHierarchy := range []bool{true, false} {
		t.Run(fmt.Sprintf("include_hierarchy=%t", includeHierarchy), func(t *testing.T) {
			t.Parallel()
			info, err := c.getDetailedTaxonomy(t.Context(), missName, "", false, includeHierarchy)
			require.NoError(t, err, "a local miss with eBird off must degrade gracefully, not error")
			require.NotNil(t, info)

			assert.Equal(t, missName, info.ScientificName)
			assert.Equal(t, taxonomySourceUnresolved, info.Metadata["source"],
				"source must mark the response as unresolved")
			assert.Equal(t, taxonomyUnresolvedNote, info.Metadata["note"],
				"the explanatory note must be present")
			assert.Equal(t, TaxonomyHierarchy{}, info.Taxonomy,
				"no rank may be fabricated for an unresolved species")
		})
	}
}

// TestGetSpeciesTaxonomyEBirdFallback drives the eBird-configured local-miss branch
// against an httptest eBird server via the eBirdClientOverride seam. It locks the
// decision at the heart of #4105: an eBird not-found (which every non-avian label
// hits, since eBird is a bird database) degrades to the unresolved 200, an eBird
// hit returns full taxonomy, and a genuine eBird error (auth/network) propagates
// rather than being misread as not-found.
func TestGetSpeciesTaxonomyEBirdFallback(t *testing.T) {
	t.Parallel()

	taxonomyDB, err := loadSharedTaxonomyDB()
	require.NoError(t, err, "Failed to load taxonomy database")

	// A binomial absent from the embedded snapshot, so the local lookup misses and
	// the eBird branch is exercised.
	const missName = "Zzyzxus fictus"

	newHandler := func(t *testing.T, h http.HandlerFunc) *Handler {
		t.Helper()
		srv := httptest.NewServer(h)
		t.Cleanup(srv.Close)
		client, err := ebird.NewClient(ebird.Config{BaseURL: srv.URL, APIKey: "test-key"})
		require.NoError(t, err, "eBird client must build")
		c := &Handler{Core: &apicore.Core{TaxonomyDB: taxonomyDB}, eBirdClientOverride: client}
		c.Settings.Store(apitest.NewValidTestSettings())
		return c
	}

	t.Run("eBird hit returns full taxonomy", func(t *testing.T) {
		t.Parallel()
		c := newHandler(t, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[{"sciName":"Zzyzxus fictus","comName":"Fake Species","speciesCode":"fake1","category":"species","order":"Testiformes","familySciName":"Testidae","familyComName":"Test Family"}]`))
		})
		info, err := c.getDetailedTaxonomy(t.Context(), missName, "", false, true)
		require.NoError(t, err)
		require.NotNil(t, info)
		assert.Equal(t, "ebird", info.Metadata["source"], "an eBird hit must be sourced from eBird")
		assert.Equal(t, "Testidae", info.Taxonomy.Family)
	})

	t.Run("eBird not-found degrades to unresolved", func(t *testing.T) {
		t.Parallel()
		c := newHandler(t, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[]`)) // taxonomy present but does not contain the species
		})
		info, err := c.getDetailedTaxonomy(t.Context(), missName, "", false, true)
		require.NoError(t, err, "an eBird not-found must degrade, not propagate")
		require.NotNil(t, info)
		assert.Equal(t, taxonomySourceUnresolved, info.Metadata["source"])
		assert.Equal(t, taxonomyUnresolvedNoteEBirdOn, info.Metadata["note"],
			"the note must not tell an eBird-configured user to configure eBird")
		assert.Equal(t, TaxonomyHierarchy{}, info.Taxonomy)
	})

	t.Run("eBird hit honors include_hierarchy=false", func(t *testing.T) {
		t.Parallel()
		c := newHandler(t, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[{"sciName":"Zzyzxus fictus","comName":"Fake Species","speciesCode":"fake1","category":"species","order":"Testiformes","familySciName":"Testidae","familyComName":"Test Family"}]`))
		})
		info, err := c.getDetailedTaxonomy(t.Context(), missName, "", false, false)
		require.NoError(t, err)
		require.NotNil(t, info)
		assert.Equal(t, "ebird", info.Metadata["source"], "still an eBird-sourced result")
		assert.Equal(t, TaxonomyHierarchy{}, info.Taxonomy,
			"the hierarchy must be omitted when include_hierarchy is false")
		assert.Equal(t, "fake1", info.SpeciesCode,
			"clearing the hierarchy must preserve sibling fields such as the species code")
	})

	t.Run("genuine eBird error propagates", func(t *testing.T) {
		t.Parallel()
		// 401 maps to CategoryConfiguration (not CategoryNotFound) and is not
		// retried, so it is fast and must propagate as an error, never be swallowed.
		c := newHandler(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
		})
		_, err := c.getDetailedTaxonomy(t.Context(), missName, "", false, true)
		require.Error(t, err, "a non-not-found eBird error must propagate, not be swallowed")
		assert.False(t, errors.IsCategory(err, errors.CategoryNotFound),
			"the propagated error must not be a not-found")
		assert.True(t, errors.IsCategory(err, errors.CategoryConfiguration),
			"a 401 must surface as a configuration error, confirming it was not swallowed")
	})
}

// TestGetSpeciesTaxonomyHandlerContract locks the observable HTTP contract through
// the exported handler (not just the internal helper): a local miss with eBird off
// returns 200 with metadata.source="unresolved" and no taxonomy block, and a nil
// embedded database returns 500 (#4105).
func TestGetSpeciesTaxonomyHandlerContract(t *testing.T) {
	t.Parallel()

	t.Run("local miss with eBird off returns 200 unresolved", func(t *testing.T) {
		t.Parallel()
		c := newTaxonomyHandler(t)

		e := echo.New()
		req := httptest.NewRequest(http.MethodGet,
			"/api/v2/species/taxonomy?scientific_name="+url.QueryEscape("Zzyzxus fictus"), http.NoBody)
		rec := httptest.NewRecorder()
		echoCtx := e.NewContext(req, rec)

		require.NoError(t, c.GetSpeciesTaxonomy(echoCtx))
		assert.Equal(t, http.StatusOK, rec.Code, "a local miss must be a graceful 200, not an error")

		var resp map[string]any
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
		meta, ok := resp["metadata"].(map[string]any)
		require.True(t, ok, "metadata must be present")
		assert.Equal(t, taxonomySourceUnresolved, meta["source"])
		_, hasTaxonomy := resp["taxonomy"]
		assert.False(t, hasTaxonomy, "no taxonomy block for an unresolved species")
	})

	t.Run("nil DB returns 500", func(t *testing.T) {
		t.Parallel()
		c := &Handler{Core: &apicore.Core{TaxonomyDB: nil}}
		c.Settings.Store(apitest.NewValidTestSettings())

		e := echo.New()
		req := httptest.NewRequest(http.MethodGet,
			"/api/v2/species/taxonomy?scientific_name="+url.QueryEscape("Turdus migratorius"), http.NoBody)
		rec := httptest.NewRecorder()
		echoCtx := e.NewContext(req, rec)

		err := c.GetSpeciesTaxonomy(echoCtx)
		apitest.AssertControllerError(t, err, rec, http.StatusInternalServerError, "Failed to get taxonomy information")
	})
}
