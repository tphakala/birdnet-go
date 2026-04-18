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
// labels pre-loaded into both name maps. It mirrors the pattern in
// setupInsightsTestController so tests share the same construction style.
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
	c.commonNameMap.Store(buildCommonNameMap(labels))
	c.commonToScientificMap.Store(buildCommonToScientificMap(labels))
	return c
}

func TestResolveSpeciesToScientific(t *testing.T) {
	t.Parallel()

	c := setupSearchTestController(t, testLabels)

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "empty string returns empty string",
			input: "",
			want:  "",
		},
		{
			name:  "whitespace-only returns empty string",
			input: "   ",
			want:  "",
		},
		{
			name:  "exact common-name match",
			input: "Tawny Owl",
			want:  "Strix aluco",
		},
		{
			name:  "case-insensitive match lowercase",
			input: "tawny owl",
			want:  "Strix aluco",
		},
		{
			name:  "case-insensitive match uppercase",
			input: "TAWNY OWL",
			want:  "Strix aluco",
		},
		{
			name:  "non-ASCII common name (Finnish)",
			input: "Lehtopöllö",
			want:  "Strix aluco",
		},
		{
			name:  "non-ASCII common name lowercase (Finnish)",
			input: "lehtopöllö",
			want:  "Strix aluco",
		},
		{
			name:  "scientific name passes through unchanged",
			input: "Strix aluco",
			want:  "Strix aluco",
		},
		{
			name:  "partial scientific name passes through unchanged",
			input: "Turdus",
			want:  "Turdus",
		},
		{
			name:  "unknown text passes through unchanged",
			input: "xyzzy",
			want:  "xyzzy",
		},
		{
			name:  "common name with surrounding whitespace resolves correctly",
			input: "  Tawny Owl  ",
			want:  "Strix aluco",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := c.resolveSpeciesToScientific(tt.input)
			assert.Equal(t, tt.want, got)
		})
	}
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
