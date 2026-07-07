package guideprovider

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClassifyCanonicalHeading(t *testing.T) {
	t.Parallel()
	cases := []struct {
		heading string
		want    canonicalSectionID
		ok      bool
	}{
		// Appearance / description (multiple locales).
		{"Description", sectionAppearance, true},
		{"Appearance", sectionAppearance, true},
		{"Beschreibung", sectionAppearance, true}, // de
		{"Opis", sectionAppearance, true},         // pl
		// Voice / songs & calls, including inflected forms.
		{"Voice", sectionVoice, true},
		{"Vocalizations", sectionVoice, true}, // trailing inflection allowed
		{"Song", sectionVoice, true},
		{"Songs and calls", sectionVoice, true},
		{"Stimme", sectionVoice, true}, // de
		// Habitat / distribution / range.
		{"Distribution and habitat", sectionHabitat, true},
		{"Habitat", sectionHabitat, true},
		{"Range", sectionHabitat, true},
		{"Verbreitung", sectionHabitat, true}, // de
		// Behaviour / ecology.
		{"Behaviour", sectionBehaviour, true},
		{"Behavior", sectionBehaviour, true},
		{"Ecology", sectionBehaviour, true},
		{"Verhalten", sectionBehaviour, true}, // de
		// Non-canonical: must NOT classify (and must not be promoted).
		{"Subsong", "", false},    // "song" is mid-word, not a leading boundary
		{"Taxonomy", "", false},   //
		{"Subspecies", "", false}, //
		{"Breeding", "", false},   //
		{"Feeding", "", false},    //
		{"Diet", "", false},       //
		{"Dialects", "", false},   //
		{"Status", "", false},     //
		{"", "", false},           // empty (article lead)
		{"   ", "", false},        // whitespace only
	}
	for _, tc := range cases {
		t.Run(tc.heading, func(t *testing.T) {
			t.Parallel()
			got, ok := classifyCanonicalHeading(tc.heading)
			assert.Equalf(t, tc.ok, ok, "classify(%q) ok", tc.heading)
			assert.Equalf(t, tc.want, got, "classify(%q) id", tc.heading)
			assert.Equalf(t, tc.ok, isCanonicalHeading(tc.heading), "isCanonicalHeading(%q)", tc.heading)
		})
	}
}

// TestContainsHeadingToken_LeadingBoundary guards the discriminator that keeps
// "Subsong" from being misclassified as a Voice section while still matching
// inflected forms like "Songs" / "Vocalizations".
func TestContainsHeadingToken_LeadingBoundary(t *testing.T) {
	t.Parallel()
	assert.True(t, containsHeadingToken("song", "song"))
	assert.True(t, containsHeadingToken("songs", "song"), "trailing inflection allowed")
	assert.True(t, containsHeadingToken("distribution and habitat", "habitat"), "boundary after space")
	assert.False(t, containsHeadingToken("subsong", "song"), "embedded mid-word must not match")
	assert.False(t, containsHeadingToken("", "song"))
	assert.False(t, containsHeadingToken("song", ""))
}

// TestConvertWikiSections_PromotesCanonicalSubsection is the core regression guard:
// a "=== Voice ===" sub-section nested under "== Description ==" must be promoted to
// a top-level "## Voice" so the frontend can split it out, instead of being flattened
// into the Description (Appearance) body.
func TestConvertWikiSections_PromotesCanonicalSubsection(t *testing.T) {
	t.Parallel()
	in := "Lead paragraph.\n\n== Description ==\nGreyish blue crown.\n\n=== Voice ===\nA loud pink-pink call.\n\n== Distribution and habitat ==\nWoodlands."
	out := convertWikiSections(in)

	assert.Contains(t, out, "## Description")
	assert.Contains(t, out, "## Voice", "canonical sub-section is promoted to a top-level row")
	assert.Contains(t, out, "## Distribution and habitat")

	// The promoted Voice header must sit BETWEEN Description and Distribution so the
	// splitter cuts the Description body before the voice prose (the bug was the voice
	// prose being absorbed into the appearance block).
	descIdx := strings.Index(out, "## Description")
	voiceIdx := strings.Index(out, "## Voice")
	habIdx := strings.Index(out, "## Distribution and habitat")
	require.Positive(t, voiceIdx)
	assert.Less(t, descIdx, voiceIdx)
	assert.Less(t, voiceIdx, habIdx)
}

// TestConvertWikiSections_KeepsNonCanonicalSubsectionsInline verifies that
// non-canonical sub-sections stay flattened so their prose remains inline in the
// parent row (no content is split out into a row the frontend would drop).
func TestConvertWikiSections_KeepsNonCanonicalSubsectionsInline(t *testing.T) {
	t.Parallel()
	in := "== Behaviour ==\nActive by day.\n\n=== Breeding ===\nNests in April.\n\n=== Feeding ===\nEats seeds."
	out := convertWikiSections(in)

	assert.Contains(t, out, "## Behaviour")
	// Breeding/Feeding are behaviour content: kept inline, NOT promoted to their own
	// (frontend-ignored) rows.
	assert.NotContains(t, out, "## Breeding")
	assert.NotContains(t, out, "## Feeding")
	assert.Contains(t, out, "Breeding")
	assert.Contains(t, out, "Nests in April.")
	assert.Contains(t, out, "Feeding")
	assert.Contains(t, out, "Eats seeds.")
}

// TestConvertWikiSections_ChaffinchStructure asserts the full canonical row set is
// emitted for a Common-Chaffinch-shaped article (the reported failing case), where
// Voice is nested under Description and Breeding/Feeding are nested under Behaviour.
func TestConvertWikiSections_ChaffinchStructure(t *testing.T) {
	t.Parallel()
	in := strings.Join([]string{
		"The common chaffinch is a small passerine bird.",
		"",
		"== Taxonomy ==",
		"Named by Linnaeus.",
		"=== Subspecies ===",
		"Several recognised.",
		"",
		"== Description ==",
		"The male has a blue-grey cap.",
		"=== Voice ===",
		"The song is a series of notes ending in a flourish.",
		"",
		"== Distribution and habitat ==",
		"Widespread across Europe.",
		"",
		"== Behaviour ==",
		"Forms flocks in winter.",
		"=== Breeding ===",
		"Builds a neat cup nest.",
	}, "\n")
	out := convertWikiSections(in)

	// All four canonical comparison rows are present as top-level splits.
	for _, h := range []string{"## Description", "## Voice", "## Distribution and habitat", "## Behaviour"} {
		assert.Containsf(t, out, h, "expected top-level %q", h)
	}
	// Non-canonical sub-sections stay flattened (no spurious rows the frontend drops).
	assert.NotContains(t, out, "## Subspecies")
	assert.NotContains(t, out, "## Breeding")
	// The voice prose is no longer trapped inside the Description body: splitting on
	// "## " yields a Voice segment whose body is the song prose, not the appearance.
	segments := strings.Split(out, "## ")
	var voiceBody string
	for _, seg := range segments {
		if strings.HasPrefix(seg, "Voice\n") {
			voiceBody = strings.TrimPrefix(seg, "Voice\n")
			break
		}
	}
	require.NotEmpty(t, voiceBody, "a distinct Voice section must exist")
	assert.Contains(t, voiceBody, "flourish")
	assert.NotContains(t, voiceBody, "blue-grey cap", "appearance prose must not leak into Voice")
}
