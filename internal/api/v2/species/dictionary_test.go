// dictionary_test.go: tests for the species dictionary endpoint.
package species

import (
	"compress/gzip"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/speciesdict"
)

// newDictController creates an Echo + species Handler and registers the species
// routes through the production RegisterRoutes path, so these tests exercise the
// real route registration (public mount, no auth middleware) rather than a
// hand-mounted route. No auth middleware is injected, matching the public
// endpoint's contract.
func newDictController(t *testing.T) (*echo.Echo, *Handler) {
	t.Helper()
	e := echo.New()
	controller := &Handler{Core: &apicore.Core{Echo: e, Group: e.Group("/api/v2")}}
	controller.RegisterRoutes(controller.Group)
	return e, controller
}

// serveDictionary issues a GET against the registered dictionary route.
func serveDictionary(t *testing.T, target string, header http.Header) *httptest.ResponseRecorder {
	t.Helper()
	e, _ := newDictController(t)
	req := httptest.NewRequest(http.MethodGet, target, http.NoBody)
	maps.Copy(req.Header, header)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

// TestSpeciesDictionary_OK checks the unversioned 200 response: headers, the
// gzip-compressed JSON body, the short-lived Cache-Control and the dataset ETag.
func TestSpeciesDictionary_OK(t *testing.T) {
	t.Parallel()
	t.Attr("component", "species-dictionary")
	t.Attr("type", "integration")

	rec := serveDictionary(t, "/api/v2/species/dictionary/fi", nil)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	assert.Equal(t, "gzip", rec.Header().Get("Content-Encoding"))
	assert.Equal(t, "nosniff", rec.Header().Get("X-Content-Type-Options"))
	assert.Equal(t, "public, max-age=300", rec.Header().Get("Cache-Control"))
	assert.Equal(t, fmt.Sprintf("%q", speciesdict.Version()), rec.Header().Get("ETag"))

	gr, err := gzip.NewReader(rec.Body)
	require.NoError(t, err, "response body must be valid gzip")
	t.Cleanup(func() {
		require.NoError(t, gr.Close())
	})

	var dict map[string]string
	err = json.NewDecoder(gr).Decode(&dict)
	require.NoError(t, err, "decompressed body must be valid JSON object")
	// Spot-check a known entry so a structurally-valid-but-garbage body is caught.
	assert.Equal(t, "mopsilepakko", dict["Barbastella barbastellus"],
		"fi dictionary must localize the canonical bat example")
}

// TestSpeciesDictionary_UnknownLocale checks that an unknown locale returns 404.
func TestSpeciesDictionary_UnknownLocale(t *testing.T) {
	t.Parallel()
	t.Attr("component", "species-dictionary")
	t.Attr("type", "integration")

	rec := serveDictionary(t, "/api/v2/species/dictionary/zz", nil)

	assert.Equal(t, http.StatusNotFound, rec.Code)

	// Assert the standardized error payload contract, not just the status code.
	var body apicore.ErrorResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, http.StatusNotFound, body.Code)
	assert.NotEmpty(t, body.Error)
	assert.NotEmpty(t, body.Message)
	assert.NotEmpty(t, body.CorrelationID)
}

// TestSpeciesDictionary_CacheControlVersioned checks that a request with the ?v=
// cache-buster query param gets an immutable Cache-Control header.
func TestSpeciesDictionary_CacheControlVersioned(t *testing.T) {
	t.Parallel()
	t.Attr("component", "species-dictionary")
	t.Attr("type", "integration")

	rec := serveDictionary(t, "/api/v2/species/dictionary/fi?v=abc123", nil)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "public, max-age=31536000, immutable", rec.Header().Get("Cache-Control"))
}

// TestSpeciesDictionary_NotModified checks that If-None-Match with the current ETag
// returns 304 with no body, including a weakened ETag as a CDN or reverse proxy may
// rewrite it.
func TestSpeciesDictionary_NotModified(t *testing.T) {
	t.Parallel()
	t.Attr("component", "species-dictionary")
	t.Attr("type", "integration")

	etag := fmt.Sprintf("%q", speciesdict.Version())
	tests := []struct {
		name        string
		ifNoneMatch string
	}{
		{name: "strong ETag", ifNoneMatch: etag},
		{name: "weak ETag", ifNoneMatch: "W/" + etag},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			rec := serveDictionary(t, "/api/v2/species/dictionary/fi", http.Header{"If-None-Match": {tt.ifNoneMatch}})

			assert.Equal(t, http.StatusNotModified, rec.Code)
			assert.Equal(t, 0, rec.Body.Len(), "304 response must have no body")
			// A 304 must carry the cache directives so the client knows how long its cached
			// representation stays fresh (RFC 7232).
			assert.NotEmpty(t, rec.Header().Get("Cache-Control"), "304 must include Cache-Control")
			assert.Equal(t, etag, rec.Header().Get("ETag"))
		})
	}
}
