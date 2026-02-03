// species_taxonomy_test.go: Package api provides tests for taxonomy API endpoints.

package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/birdnet"
)

// TestGetGenusSpecies tests the GET /api/v2/taxonomy/genus/:genus endpoint
func TestGetGenusSpecies(t *testing.T) {
	t.Parallel()

	// Load taxonomy database
	taxonomyDB, err := birdnet.LoadTaxonomyDatabase()
	require.NoError(t, err, "Failed to load taxonomy database")

	// Create a minimal controller with taxonomy DB
	c := &Controller{
		TaxonomyDB: taxonomyDB,
	}

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

	taxonomyDB, err := birdnet.LoadTaxonomyDatabase()
	require.NoError(t, err, "Failed to load taxonomy database")

	c := &Controller{
		TaxonomyDB: taxonomyDB,
	}

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

	taxonomyDB, err := birdnet.LoadTaxonomyDatabase()
	require.NoError(t, err, "Failed to load taxonomy database")

	c := &Controller{
		TaxonomyDB: taxonomyDB,
	}

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

	taxonomyDB, err := birdnet.LoadTaxonomyDatabase()
	require.NoError(t, err, "Failed to load taxonomy database")

	c := &Controller{
		TaxonomyDB: taxonomyDB,
		// No EBirdClient - testing local DB only
		EBirdClient: nil,
	}

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

// TestGetSpeciesTaxonomyWithoutLocalDB tests fallback when local DB unavailable
func TestGetSpeciesTaxonomyWithoutLocalDB(t *testing.T) {
	t.Parallel()

	c := &Controller{
		// No TaxonomyDB and no EBirdClient
		TaxonomyDB:  nil,
		EBirdClient: nil,
	}

	// This should fail gracefully
	_, err := c.getDetailedTaxonomy(t.Context(), "Turdus migratorius", "", false, true)

	require.Error(t, err, "Expected error when both local DB and eBird client unavailable")
	t.Logf("Correctly returned error: %v", err)
}
