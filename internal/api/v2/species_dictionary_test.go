// species_dictionary_test.go: Package api tests for the species dictionary endpoint.
package api

import (
	"compress/gzip"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/speciesdict"
)

// newDictController creates a minimal Echo + Controller wired with the
// ServeSpeciesDictionary route for testing. No auth middleware is added.
func newDictController(t *testing.T) (*echo.Echo, *Controller) {
	t.Helper()
	e := echo.New()
	controller := &Controller{Echo: e, Group: e.Group("/api/v2")}
	controller.Group.GET("/species/dictionary/:locale", controller.ServeSpeciesDictionary)
	return e, controller
}

// TestSpeciesDictionary_PublicReachable verifies the endpoint returns 200 without any
// auth middleware configured and that it serves the Finnish dictionary successfully.
func TestSpeciesDictionary_PublicReachable(t *testing.T) {
	t.Parallel()
	t.Attr("component", "species-dictionary")
	t.Attr("type", "integration")

	e, _ := newDictController(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v2/species/dictionary/fi", http.NoBody)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

// TestSpeciesDictionary_Headers checks Content-Type, Content-Encoding, and
// X-Content-Type-Options are set correctly.
func TestSpeciesDictionary_Headers(t *testing.T) {
	t.Parallel()
	t.Attr("component", "species-dictionary")
	t.Attr("type", "integration")

	e, _ := newDictController(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v2/species/dictionary/fi", http.NoBody)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	assert.Equal(t, "gzip", rec.Header().Get("Content-Encoding"))
	assert.Equal(t, "nosniff", rec.Header().Get("X-Content-Type-Options"))
}

// TestSpeciesDictionary_BodyIsValidGzipJSON decompresses the response and asserts
// it is a JSON object with at least one scientific-name to common-name mapping.
func TestSpeciesDictionary_BodyIsValidGzipJSON(t *testing.T) {
	t.Parallel()
	t.Attr("component", "species-dictionary")
	t.Attr("type", "integration")

	e, _ := newDictController(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v2/species/dictionary/fi", http.NoBody)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	gr, err := gzip.NewReader(rec.Body)
	require.NoError(t, err, "response body must be valid gzip")
	t.Cleanup(func() {
		require.NoError(t, gr.Close())
	})

	var dict map[string]string
	err = json.NewDecoder(gr).Decode(&dict)
	require.NoError(t, err, "decompressed body must be valid JSON object")
	assert.NotEmpty(t, dict, "dictionary must contain at least one entry")
}

// TestSpeciesDictionary_UnknownLocale checks that an unknown locale returns 404.
func TestSpeciesDictionary_UnknownLocale(t *testing.T) {
	t.Parallel()
	t.Attr("component", "species-dictionary")
	t.Attr("type", "integration")

	e, _ := newDictController(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v2/species/dictionary/zz", http.NoBody)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// TestSpeciesDictionary_CacheControlVersioned checks that a request with the ?v=
// cache-buster query param gets an immutable Cache-Control header.
func TestSpeciesDictionary_CacheControlVersioned(t *testing.T) {
	t.Parallel()
	t.Attr("component", "species-dictionary")
	t.Attr("type", "integration")

	e, _ := newDictController(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v2/species/dictionary/fi?v=abc123", http.NoBody)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "public, max-age=31536000, immutable", rec.Header().Get("Cache-Control"))
}

// TestSpeciesDictionary_CacheControlUnversioned checks that a request without a ?v=
// param gets the short-lived Cache-Control header.
func TestSpeciesDictionary_CacheControlUnversioned(t *testing.T) {
	t.Parallel()
	t.Attr("component", "species-dictionary")
	t.Attr("type", "integration")

	e, _ := newDictController(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v2/species/dictionary/fi", http.NoBody)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "public, max-age=300", rec.Header().Get("Cache-Control"))
}

// TestSpeciesDictionary_ETag checks that the ETag header equals the quoted dataset version.
func TestSpeciesDictionary_ETag(t *testing.T) {
	t.Parallel()
	t.Attr("component", "species-dictionary")
	t.Attr("type", "integration")

	e, _ := newDictController(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v2/species/dictionary/fi", http.NoBody)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	expectedETag := fmt.Sprintf("%q", speciesdict.Version())
	assert.Equal(t, expectedETag, rec.Header().Get("ETag"))
}

// TestSpeciesDictionary_NotModified checks that If-None-Match with the current ETag
// returns 304 with no body.
func TestSpeciesDictionary_NotModified(t *testing.T) {
	t.Parallel()
	t.Attr("component", "species-dictionary")
	t.Attr("type", "integration")

	e, _ := newDictController(t)

	etag := fmt.Sprintf("%q", speciesdict.Version())
	req := httptest.NewRequest(http.MethodGet, "/api/v2/species/dictionary/fi", http.NoBody)
	req.Header.Set("If-None-Match", etag)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotModified, rec.Code)
	assert.Equal(t, 0, rec.Body.Len(), "304 response must have no body")
}
