package api

import (
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// testLabels contains representative BirdNET label strings used across
// common-name resolution tests (format: "ScientificName_CommonName").
var testLabels = []string{
	"Strix aluco_Tawny Owl",
	"Strix aluco_Lehtopöllö",
	"Turdus merula_Eurasian Blackbird",
	"Parus major_Great Tit",
}

// setupSearchTestController builds a minimal Controller with the given BirdNET
// labels pre-loaded into both name maps.
func setupSearchTestController(t *testing.T, labels []string) *Controller {
	t.Helper()
	e := echo.New()
	c := &Controller{
		Group: e.Group("/api/v2"),
		Settings: &conf.Settings{
			BirdNET: conf.BirdNETConfig{
				Labels: labels,
			},
		},
	}
	c.nameMaps.Store(buildNameMaps(labels))
	return c
}

func TestResolveSpeciesToScientific(t *testing.T) {
	t.Parallel()

	c := setupSearchTestController(t, testLabels)

	tests := []struct {
		name    string
		input   string
		want    string
		wantHit bool
	}{
		{
			name:    "empty string returns empty string",
			input:   "",
			want:    "",
			wantHit: false,
		},
		{
			name:    "whitespace-only returns empty string without hit",
			input:   "   ",
			want:    "",
			wantHit: false,
		},
		{
			name:    "exact common-name match",
			input:   "Tawny Owl",
			want:    "Strix aluco",
			wantHit: true,
		},
		{
			name:    "case-insensitive match lowercase",
			input:   "tawny owl",
			want:    "Strix aluco",
			wantHit: true,
		},
		{
			name:    "case-insensitive match uppercase",
			input:   "TAWNY OWL",
			want:    "Strix aluco",
			wantHit: true,
		},
		{
			name:    "non-ASCII common name (Finnish)",
			input:   "Lehtopöllö",
			want:    "Strix aluco",
			wantHit: true,
		},
		{
			name:    "non-ASCII common name lowercase (Finnish)",
			input:   "lehtopöllö",
			want:    "Strix aluco",
			wantHit: true,
		},
		{
			name:    "scientific name passes through unchanged",
			input:   "Strix aluco",
			want:    "Strix aluco",
			wantHit: false,
		},
		{
			name:    "partial scientific name passes through unchanged",
			input:   "Turdus",
			want:    "Turdus",
			wantHit: false,
		},
		{
			name:    "unknown text passes through unchanged",
			input:   "xyzzy",
			want:    "xyzzy",
			wantHit: false,
		},
		{
			name:    "common name with surrounding whitespace resolves correctly",
			input:   "  Tawny Owl  ",
			want:    "Strix aluco",
			wantHit: true,
		},
		{
			// macOS and some composing keyboards submit NFD bytes for
			// diacritics. The resolver must normalise to NFC so labels
			// (which ship as NFC) still match.
			name:    "NFD-form diacritic matches NFC-stored label",
			input:   "Lehtopo\u0308llo\u0308",
			want:    "Strix aluco",
			wantHit: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, hit := c.resolveSpeciesToScientific(tt.input)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.wantHit, hit)
		})
	}
}

// TestResolveSpeciesToScientific_AmbiguousCommonName verifies that when two
// species share the same common name, the resolver passes the query through
// untranslated (hit=false) rather than resolving it to an arbitrary species
// based on label order.
func TestResolveSpeciesToScientific_AmbiguousCommonName(t *testing.T) {
	t.Parallel()

	ambiguousLabels := []string{
		"Strix aluco_Owl",
		"Bubo bubo_Owl",
		"Turdus merula_Eurasian Blackbird",
	}
	c := setupSearchTestController(t, ambiguousLabels)

	resolved, hit := c.resolveSpeciesToScientific("Owl")
	assert.Equal(t, "Owl", resolved, "ambiguous common name should pass through untranslated")
	assert.False(t, hit, "ambiguous common name should not register as a hit")

	// Non-ambiguous names must still resolve correctly.
	resolved, hit = c.resolveSpeciesToScientific("Eurasian Blackbird")
	assert.Equal(t, "Turdus merula", resolved)
	assert.True(t, hit)
}

// TestUpdateCommonNameMap_PopulatesBothMaps verifies that UpdateCommonNameMap
// populates both the scientific-to-common map and the common-to-scientific map
// from the same label input, keeping them consistent.
func TestUpdateCommonNameMap_PopulatesBothMaps(t *testing.T) {
	t.Parallel()

	e := echo.New()
	c := &Controller{
		Group: e.Group("/api/v2"),
	}

	labels := []string{
		"Strix aluco_Tawny Owl",
		"Parus major_Great Tit",
	}
	c.UpdateCommonNameMap(labels)

	// Verify the scientific-to-common map (used by insights endpoints).
	sciToCommon := c.loadCommonNameMap()
	require.NotNil(t, sciToCommon)
	assert.Equal(t, "Tawny Owl", sciToCommon["Strix aluco"])
	assert.Equal(t, "Great Tit", sciToCommon["Parus major"])

	// Verify the common-to-scientific map (used by the search resolver).
	commonToSci := c.loadCommonToScientificMap()
	require.NotNil(t, commonToSci)
	assert.Equal(t, "Strix aluco", commonToSci["tawny owl"])
	assert.Equal(t, "Parus major", commonToSci["great tit"])
}

// TestBuildNameMaps_AmbiguousCommonName verifies that a common name mapped
// by two different scientific names is removed from commonToSci so the
// search resolver passes ambiguous queries through untranslated.
func TestBuildNameMaps_AmbiguousCommonName(t *testing.T) {
	t.Parallel()

	nm := buildNameMaps([]string{
		"Strix aluco_Owl",
		"Bubo bubo_Owl",
		"Parus major_Great Tit",
	})
	require.NotNil(t, nm)

	// sciToCommon keeps both species; scientific names are always unique.
	assert.Equal(t, "Owl", nm.sciToCommon["Strix aluco"])
	assert.Equal(t, "Owl", nm.sciToCommon["Bubo bubo"])

	// commonToSci must NOT contain the ambiguous key.
	_, ok := nm.commonToSci["owl"]
	assert.False(t, ok, "ambiguous common-name key should be removed")

	// A third label that repeats an already-ambiguous key should not
	// accidentally restore the key.
	nm = buildNameMaps([]string{
		"Strix aluco_Owl",
		"Bubo bubo_Owl",
		"Tyto alba_Owl",
	})
	_, ok = nm.commonToSci["owl"]
	assert.False(t, ok)

	// Non-ambiguous names remain.
	nm = buildNameMaps([]string{
		"Strix aluco_Owl",
		"Bubo bubo_Owl",
		"Parus major_Great Tit",
	})
	assert.Equal(t, "Parus major", nm.commonToSci["great tit"])
}

// TestBuildNameMaps_MalformedLabels verifies that labels missing a scientific
// name, a common name, or the separator are silently skipped rather than
// producing empty keys.
func TestBuildNameMaps_MalformedLabels(t *testing.T) {
	t.Parallel()

	nm := buildNameMaps([]string{
		"Strix aluco_Tawny Owl",
		"_MissingScientific",
		"MissingCommon_",
		"NoSeparatorAtAll",
		"",
		"   _   ",
	})
	require.NotNil(t, nm)
	assert.Len(t, nm.sciToCommon, 1)
	assert.Len(t, nm.commonToSci, 1)
	assert.Equal(t, "Tawny Owl", nm.sciToCommon["Strix aluco"])
	assert.Equal(t, "Strix aluco", nm.commonToSci["tawny owl"])
}

// TestLoadNameMaps_CalledBeforeInit verifies that the load helpers return
// non-nil empty maps when the Controller has not yet seeded nameMaps, so
// callers can index without nil checks during the startup window.
func TestLoadNameMaps_CalledBeforeInit(t *testing.T) {
	t.Parallel()

	c := &Controller{}
	assert.NotNil(t, c.loadCommonNameMap())
	assert.NotNil(t, c.loadCommonToScientificMap())
	assert.Empty(t, c.loadCommonNameMap())
	assert.Empty(t, c.loadCommonToScientificMap())
}
