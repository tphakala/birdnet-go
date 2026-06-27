package detections

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/api/v2/apitest"
	"github.com/tphakala/birdnet-go/internal/datastore"
)

// testLabels contains representative BirdNET label strings used across
// common-name resolution tests (format: "ScientificName_CommonName").
var testLabels = []string{
	"Strix aluco_Tawny Owl",
	"Strix aluco_Lehtopöllö",
	"Turdus merula_Eurasian Blackbird",
	"Parus major_Great Tit",
}

// buildTestCommonToSci builds the common->scientific lookup map the search
// resolver reads, from "Scientific_Common" labels. It mirrors the facade
// buildNameMaps commonToSci construction (including ambiguous-name removal) using
// the same datastore.ResolveLabelNames + apicore.NormalizeForLookup the production
// name maps use, so the detections resolver tests do not depend on the facade
// name-map plumbing (which is tested separately in package api).
func buildTestCommonToSci(labels []string) map[string]string {
	m := make(map[string]string, len(labels))
	ambiguous := make(map[string]struct{})
	for _, sn := range datastore.ResolveLabelNames(labels, nil) {
		key := apicore.NormalizeForLookup(sn.Common)
		if _, seen := ambiguous[key]; seen {
			continue
		}
		if existing, exists := m[key]; exists && existing != sn.Scientific {
			ambiguous[key] = struct{}{}
			delete(m, key)
			continue
		}
		m[key] = sn.Scientific
	}
	return m
}

// setupSearchTestController builds a detections Handler whose common->scientific
// name map is pre-loaded from the given BirdNET labels, so resolveSpeciesToScientific
// resolves them.
func setupSearchTestController(t *testing.T, labels []string) *Handler {
	t.Helper()
	core := apitest.NewCore(t, apitest.WithoutSettingsPublish())
	return buildTestHandler(t, core, buildTestCommonToSci(labels), nil)
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
			input:   "Lehtopöllö",
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
