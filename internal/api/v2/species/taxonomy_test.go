// taxonomy_test.go: tests for taxonomy API endpoints.

package species

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
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

// TestGetGenusSpecies tests the GET /api/v2/taxonomy/genus/:genus endpoint
func TestGetGenusSpecies(t *testing.T) {
	t.Parallel()

	// Load taxonomy database
	taxonomyDB, err := classifier.LoadTaxonomyDatabase()
	require.NoError(t, err, "Failed to load taxonomy database")

	// Create a minimal controller with taxonomy DB
	c := &Handler{Core: &apicore.Core{TaxonomyDB: taxonomyDB}}
	c.Settings.Store(apitest.NewValidTestSettings())

	tests := []struct {
		name           string
		genus          string
		expectedStatus int
		checkResponse  func(*testing.T, map[string]any)
	}{
		{
			name:           "valid genus - corvus",
			genus:          "corvus",
			expectedStatus: http.StatusOK,
			checkResponse: func(t *testing.T, resp map[string]any) {
				t.Helper()
				genus, ok := resp["genus"].(string)
				assert.True(t, ok, "Expected genus field in response")
				// Genus is returned in proper case (Corvus), not lowercase
				assert.Equal(t, "Corvus", genus, "Expected genus 'Corvus'")

				species, ok := resp["species"].([]any)
				assert.True(t, ok, "Expected species array")
				assert.GreaterOrEqual(t, len(species), 10, "Expected at least 10 corvus species")
			},
		},
		{
			name:           "valid genus - turdus",
			genus:          "turdus",
			expectedStatus: http.StatusOK,
			checkResponse: func(t *testing.T, resp map[string]any) {
				t.Helper()
				species, ok := resp["species"].([]any)
				assert.True(t, ok, "Expected species array")
				assert.GreaterOrEqual(t, len(species), 50, "Expected at least 50 turdus species")
			},
		},
		{
			name:           "case insensitive - CORVUS",
			genus:          "CORVUS",
			expectedStatus: http.StatusOK,
			checkResponse: func(t *testing.T, resp map[string]any) {
				t.Helper()
				genus, ok := resp["genus"].(string)
				assert.True(t, ok, "Expected genus field in response")
				// Genus should match regardless of input case
				assert.Equal(t, "Corvus", genus, "Genus should be 'Corvus'")
			},
		},
		{
			name:           "nonexistent genus",
			genus:          "nonexistentgenus",
			expectedStatus: http.StatusNotFound,
			checkResponse:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := echo.New()
			req := httptest.NewRequest(http.MethodGet, "/api/v2/taxonomy/genus/"+tt.genus, http.NoBody)
			rec := httptest.NewRecorder()
			echoCtx := e.NewContext(req, rec)
			echoCtx.SetParamNames("genus")
			echoCtx.SetParamValues(tt.genus)

			err := c.GetGenusSpecies(echoCtx)

			if tt.expectedStatus == http.StatusOK {
				require.NoError(t, err, "Expected no error")
				assert.Equal(t, http.StatusOK, rec.Code, "Expected HTTP 200 OK")
				assert.Contains(t, rec.Header().Get("Content-Type"), "application/json", "Expected JSON content type")

				// Verify cache headers
				assert.Equal(t, "public, max-age=86400", rec.Header().Get("Cache-Control"), "Expected cache control header")
				assert.Equal(t, "Accept-Encoding", rec.Header().Get("Vary"), "Expected vary header")

				if tt.checkResponse != nil {
					// Parse JSON response
					var resp map[string]any
					err := json.Unmarshal(rec.Body.Bytes(), &resp)
					require.NoError(t, err, "Failed to parse JSON response")
					tt.checkResponse(t, resp)
				}
			} else {
				// For error cases, Echo returns an error or non-OK status
				assert.True(t, err != nil || rec.Code != http.StatusOK, "Expected error or non-OK status")
			}
		})
	}
}

// TestGetFamilySpecies tests the GET /api/v2/taxonomy/family/:family endpoint
func TestGetFamilySpecies(t *testing.T) {
	t.Parallel()

	taxonomyDB, err := classifier.LoadTaxonomyDatabase()
	require.NoError(t, err, "Failed to load taxonomy database")

	c := &Handler{Core: &apicore.Core{TaxonomyDB: taxonomyDB}}
	c.Settings.Store(apitest.NewValidTestSettings())

	tests := []struct {
		name           string
		family         string
		expectedStatus int
		minSpecies     int
	}{
		{
			name:           "owls - strigidae",
			family:         "strigidae",
			expectedStatus: http.StatusOK,
			minSpecies:     200,
		},
		{
			name:           "corvids - corvidae",
			family:         "corvidae",
			expectedStatus: http.StatusOK,
			minSpecies:     100,
		},
		{
			name:           "case insensitive",
			family:         "STRIGIDAE",
			expectedStatus: http.StatusOK,
			minSpecies:     200,
		},
		{
			name:           "nonexistent family",
			family:         "nonexistentfamily",
			expectedStatus: http.StatusNotFound,
			minSpecies:     0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := echo.New()
			req := httptest.NewRequest(http.MethodGet, "/api/v2/taxonomy/family/"+tt.family, http.NoBody)
			rec := httptest.NewRecorder()
			echoCtx := e.NewContext(req, rec)
			echoCtx.SetParamNames("family")
			echoCtx.SetParamValues(tt.family)

			err := c.GetFamilySpecies(echoCtx)

			if tt.expectedStatus == http.StatusOK {
				require.NoError(t, err, "Expected no error")
				assert.Equal(t, http.StatusOK, rec.Code, "Expected HTTP 200 OK")
				assert.Contains(t, rec.Header().Get("Content-Type"), "application/json", "Expected JSON content type")

				// Verify cache headers
				assert.Equal(t, "public, max-age=86400", rec.Header().Get("Cache-Control"), "Expected cache control header")
				assert.Equal(t, "Accept-Encoding", rec.Header().Get("Vary"), "Expected vary header")

				var resp map[string]any
				err := json.Unmarshal(rec.Body.Bytes(), &resp)
				require.NoError(t, err, "Failed to parse JSON response")

				species, ok := resp["species"].([]any)
				assert.True(t, ok, "Expected species array in response")
				assert.GreaterOrEqual(t, len(species), tt.minSpecies,
					"Expected at least %d species in family %s", tt.minSpecies, tt.family)

				t.Logf("Family %s has %d species", tt.family, len(species))
			} else {
				assert.True(t, err != nil || rec.Code != http.StatusOK, "Expected error or non-OK status")
			}
		})
	}
}

// TestGetSpeciesTree tests the GET /api/v2/taxonomy/tree/:scientific_name endpoint
func TestGetSpeciesTree(t *testing.T) {
	t.Parallel()

	taxonomyDB, err := classifier.LoadTaxonomyDatabase()
	require.NoError(t, err, "Failed to load taxonomy database")

	c := &Handler{Core: &apicore.Core{TaxonomyDB: taxonomyDB}}
	c.Settings.Store(apitest.NewValidTestSettings())

	tests := []struct {
		name           string
		scientificName string
		expectedStatus int
		wantGenus      string
		wantFamily     string
		wantOrder      string
	}{
		{
			name:           "american robin",
			scientificName: "Turdus migratorius",
			expectedStatus: http.StatusOK,
			wantGenus:      "Turdus",
			wantFamily:     "Turdidae",
			wantOrder:      "Passeriformes",
		},
		{
			name:           "common raven",
			scientificName: "Corvus corax",
			expectedStatus: http.StatusOK,
			wantGenus:      "Corvus",
			wantFamily:     "Corvidae",
			wantOrder:      "Passeriformes",
		},
		{
			name:           "great horned owl",
			scientificName: "Bubo virginianus",
			expectedStatus: http.StatusOK,
			wantGenus:      "Bubo",
			wantFamily:     "Strigidae",
			wantOrder:      "Strigiformes",
		},
		{
			name:           "url encoded spaces",
			scientificName: "Turdus migratorius",
			expectedStatus: http.StatusOK,
			wantGenus:      "Turdus",
			wantFamily:     "Turdidae",
			wantOrder:      "Passeriformes",
		},
		{
			name:           "nonexistent species",
			scientificName: "Nonexistent species",
			expectedStatus: http.StatusNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := echo.New()
			// URL-encode the scientific name for the request URL
			encodedName := url.PathEscape(tt.scientificName)
			req := httptest.NewRequest(http.MethodGet, "/api/v2/taxonomy/tree/"+encodedName, http.NoBody)
			rec := httptest.NewRecorder()
			echoCtx := e.NewContext(req, rec)
			echoCtx.SetParamNames("scientific_name")
			// Echo would decode the URL param, so pass the encoded value to simulate that
			echoCtx.SetParamValues(encodedName)

			err := c.GetSpeciesTree(echoCtx)

			if tt.expectedStatus == http.StatusOK {
				require.NoError(t, err, "Expected no error")
				assert.Equal(t, http.StatusOK, rec.Code, "Expected HTTP 200 OK")
				assert.Contains(t, rec.Header().Get("Content-Type"), "application/json", "Expected JSON content type")

				// Verify cache headers
				assert.Equal(t, "public, max-age=86400", rec.Header().Get("Cache-Control"), "Expected cache control header")
				assert.Equal(t, "Accept-Encoding", rec.Header().Get("Vary"), "Expected vary header")

				var resp map[string]any
				err := json.Unmarshal(rec.Body.Bytes(), &resp)
				require.NoError(t, err, "Failed to parse JSON response")

				// Check taxonomy tree structure (JSON field is "taxonomy_tree" with underscore)
				tree, ok := resp["taxonomy_tree"].(map[string]any)
				assert.True(t, ok, "Expected taxonomy_tree in response")

				assert.Equal(t, tt.wantGenus, tree["genus"], "Expected genus to match")
				assert.Equal(t, tt.wantFamily, tree["family"], "Expected family to match")
				assert.Equal(t, tt.wantOrder, tree["order"], "Expected order to match")

				// Verify basic taxonomy structure
				assert.Equal(t, "Animalia", tree["kingdom"], "Expected kingdom Animalia")
				assert.Equal(t, "Chordata", tree["phylum"], "Expected phylum Chordata")
				assert.Equal(t, "Aves", tree["class"], "Expected class Aves")
			} else {
				assert.True(t, err != nil || rec.Code != http.StatusOK, "Expected error or non-OK status")
			}
		})
	}
}

// TestGetSpeciesTaxonomyLocalDB tests the main taxonomy endpoint with local DB
func TestGetSpeciesTaxonomyLocalDB(t *testing.T) {
	t.Parallel()

	taxonomyDB, err := classifier.LoadTaxonomyDatabase()
	require.NoError(t, err, "Failed to load taxonomy database")

	c := &Handler{Core: &apicore.Core{TaxonomyDB: taxonomyDB}}
	c.Settings.Store(apitest.NewValidTestSettings())

	tests := []struct {
		name           string
		scientificName string
		expectedStatus int
		wantFamily     string
	}{
		{
			name:           "local db lookup - american robin",
			scientificName: "Turdus migratorius",
			expectedStatus: http.StatusOK,
			wantFamily:     "Turdidae",
		},
		{
			name:           "local db lookup - common raven",
			scientificName: "Corvus corax",
			expectedStatus: http.StatusOK,
			wantFamily:     "Corvidae",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Call the internal method directly since we need context
			info, err := c.getDetailedTaxonomy(t.Context(), tt.scientificName, "", false, true)

			if tt.expectedStatus == http.StatusOK {
				require.NoError(t, err, "Expected no error")
				require.NotNil(t, info, "Expected non-nil taxonomy info")
				require.NotNil(t, info.Taxonomy, "Expected non-nil taxonomy hierarchy")

				assert.Equal(t, tt.wantFamily, info.Taxonomy.Family, "Expected family to match")

				// Verify metadata indicates local source
				source, ok := info.Metadata["source"].(string)
				assert.True(t, ok, "Expected source in metadata")
				assert.Contains(t, []string{"local", "local+ebird"}, source,
					"Expected source to be 'local' or 'local+ebird'")

				t.Logf("Successfully retrieved taxonomy from local DB (source: %v)", source)
			} else {
				assert.Error(t, err, "Expected error")
			}
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

	taxonomyDB, err := classifier.LoadTaxonomyDatabase()
	require.NoError(t, err, "Failed to load taxonomy database")

	c := &Handler{Core: &apicore.Core{TaxonomyDB: taxonomyDB}}
	c.Settings.Store(apitest.NewValidTestSettings())
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

	taxonomyDB, err := classifier.LoadTaxonomyDatabase()
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

	taxonomyDB, err := classifier.LoadTaxonomyDatabase()
	require.NoError(t, err, "Failed to load taxonomy database")

	t.Run("local miss with eBird off returns 200 unresolved", func(t *testing.T) {
		t.Parallel()
		c := &Handler{Core: &apicore.Core{TaxonomyDB: taxonomyDB}}
		c.Settings.Store(apitest.NewValidTestSettings())

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
