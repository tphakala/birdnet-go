// internal/api/v2/species_dictionary.go
package api

import (
	"fmt"
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/speciesdict"
)

// Species dictionary endpoint constants.
const (
	// dictContentType is the MIME type for the dictionary response. Set explicitly
	// to prevent Go's content-type sniffing from seeing gzip magic bytes and
	// mislabeling the response, which causes browsers to download instead of parse.
	dictContentType = "application/json"

	// dictContentEncoding is the encoding header value for precompressed responses.
	dictContentEncoding = "gzip"

	// dictNoSniff is the X-Content-Type-Options value that instructs browsers not
	// to override the declared Content-Type.
	dictNoSniff = "nosniff"

	// dictCacheImmutable is the Cache-Control value for versioned (content-addressed)
	// dictionary URLs. Safe because the URL changes when the dataset version changes.
	dictCacheImmutable = "public, max-age=31536000, immutable"

	// dictCacheShort is the Cache-Control value for unversioned dictionary requests.
	// Short enough that stale content does not persist after a dataset update.
	dictCacheShort = "public, max-age=300"
)

// ServeSpeciesDictionary handles GET /api/v2/species/dictionary/:locale.
//
// It returns the precompressed gzip JSON species name dictionary for the
// requested locale. The dictionary maps scientific names to localized common
// names and is intended for client-side species name localization.
//
// The response is served with explicit Content-Type and Content-Encoding
// headers so browsers parse it as JSON rather than treating it as a binary
// download. An optional ?v= cache-buster query param enables long-lived
// immutable caching; requests without it receive a short-lived cache header.
//
// Public endpoint, no authentication required.
func (c *Controller) ServeSpeciesDictionary(ctx echo.Context) error {
	locale := ctx.Param("locale")

	// Validate locale against the embedded allowlist before touching anything
	// else. Unknown locales are rejected here and can only ever 404.
	if !speciesdict.Has(locale) {
		return c.HandleError(ctx,
			errors.Newf("unknown species dictionary locale: %s", locale).
				Category(errors.CategoryNotFound).
				Component("api-species-dictionary").
				Build(),
			"Species dictionary not found for locale",
			http.StatusNotFound,
		)
	}

	body, err := speciesdict.Read(locale)
	if err != nil {
		// Has() already validated the locale; an error here is unexpected.
		return c.HandleError(ctx,
			errors.New(err).
				Category(errors.CategorySystem).
				Component("api-species-dictionary").
				Build(),
			"Failed to read species dictionary",
			http.StatusInternalServerError,
		)
	}

	// Build the quoted ETag from the dataset version, e.g. `"daec15c"`.
	etag := fmt.Sprintf("%q", speciesdict.Version())
	hdr := ctx.Response().Header()
	hdr.Set("ETag", etag)

	// Respond with 304 if the client already has the current version. The ETag is
	// set above so the 304 echoes it, per RFC 7232.
	if ctx.Request().Header.Get("If-None-Match") == etag {
		return ctx.NoContent(http.StatusNotModified)
	}

	// Set response headers BEFORE writing the body so the framework cannot
	// sniff the gzip magic bytes and override Content-Type.
	hdr.Set("Content-Encoding", dictContentEncoding)
	hdr.Set("X-Content-Type-Options", dictNoSniff)

	// Choose cache lifetime based on whether the caller provided a content-
	// addressed version query param.
	if ctx.QueryParam("v") != "" {
		hdr.Set("Cache-Control", dictCacheImmutable)
	} else {
		hdr.Set("Cache-Control", dictCacheShort)
	}

	// ctx.Blob writes the given content type directly without sniffing, so the
	// Content-Encoding header set above survives intact.
	return ctx.Blob(http.StatusOK, dictContentType, body)
}
