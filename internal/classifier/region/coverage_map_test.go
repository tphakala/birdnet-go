package region

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCoverageMap_EveryTableSlugHasMap verifies every region slug in every
// embedded table resolves to a coverage map, so the gallery never links a region
// option to a missing SVG.
func TestCoverageMap_EveryTableSlugHasMap(t *testing.T) {
	t.Parallel()
	tables, err := Tables()
	require.NoError(t, err)
	require.NotEmpty(t, tables)
	for repo := range tables {
		for slug := range tables[repo].Regions {
			svg, etag, ok := CoverageMap(slug)
			assert.Truef(t, ok, "table %s slug %q has no embedded coverage map", repo, slug)
			if ok {
				assert.NotEmpty(t, svg, "slug %q map bytes", slug)
				assert.NotEmpty(t, etag, "slug %q etag", slug)
			}
		}
	}
}

// TestCoverageMap_NoStrayFiles verifies every embedded data/maps/*.svg belongs to
// a known region slug, catching an orphan map left behind by a bad sync.
func TestCoverageMap_NoStrayFiles(t *testing.T) {
	t.Parallel()
	tables, err := Tables()
	require.NoError(t, err)
	known := make(map[string]bool)
	for repo := range tables {
		for slug := range tables[repo].Regions {
			known[slug] = true
		}
	}
	entries, err := mapsFS.ReadDir(mapsDir)
	require.NoError(t, err)
	require.NotEmpty(t, entries)
	for _, e := range entries {
		slug := strings.TrimSuffix(e.Name(), ".svg")
		assert.Truef(t, known[slug], "embedded map %q matches no known region slug", e.Name())
	}
}

// TestCoverageMap_ContentSanity checks that a served map is a themeable coverage
// SVG scoped to its own slug (the clipPath id is tile-scoped so several inlined
// maps cannot collide), and that the ETag is a quoted strong validator.
func TestCoverageMap_ContentSanity(t *testing.T) {
	t.Parallel()
	svg, etag, ok := CoverageMap("nordic")
	require.True(t, ok)
	body := string(svg)
	assert.True(t, strings.HasPrefix(strings.TrimSpace(body), "<svg"), "starts with <svg")
	assert.Contains(t, body, `class="cov"`, "carries the .cov styling handle")
	assert.Contains(t, body, "fp-nordic", "clipPath id is tile-scoped to the slug")
	assert.True(t, strings.HasPrefix(etag, `"`) && strings.HasSuffix(etag, `"`), "ETag is a quoted string, got %q", etag)
}

// TestCoverageMap_StableETag verifies the ETag is deterministic across calls, so
// conditional requests keep matching for the life of the binary.
func TestCoverageMap_StableETag(t *testing.T) {
	t.Parallel()
	_, first, ok := CoverageMap("nordic")
	require.True(t, ok)
	_, second, ok := CoverageMap("nordic")
	require.True(t, ok)
	assert.Equal(t, first, second)
}

// TestCoverageMap_RejectsUnknownAndMalformed verifies the accessor returns
// ok=false for unknown slugs and for malformed input (path traversal, separators,
// casing, empties), so the HTTP layer 404s rather than reaching outside the set.
func TestCoverageMap_RejectsUnknownAndMalformed(t *testing.T) {
	t.Parallel()
	for _, slug := range []string{
		"", "does-not-exist", "../embed", "a/b", "..", "Nordic", "nord ic",
		"nordic.svg", "nordic/", ".", "%2e%2e", "data/maps/nordic",
	} {
		_, _, ok := CoverageMap(slug)
		assert.Falsef(t, ok, "slug %q must not resolve", slug)
	}
}

// TestSlugPattern_GuardsInput pins the slug validator directly, so the guard's
// rejection of separators, dots, casing, and spaces is proven independently of
// the map-miss path that would also reject an unknown slug.
func TestSlugPattern_GuardsInput(t *testing.T) {
	t.Parallel()
	for _, bad := range []string{"", "../x", "a/b", "..", "Nordic", "nord ic", "nordic.svg", "%2e%2e", "a_b", "data/maps/nordic"} {
		assert.Falsef(t, slugPattern.MatchString(bad), "slug %q must be rejected", bad)
	}
	for _, good := range []string{"nordic", "british-isles", "sao-tome-principe", "amazonia"} {
		assert.Truef(t, slugPattern.MatchString(good), "slug %q must be accepted", good)
	}
}
