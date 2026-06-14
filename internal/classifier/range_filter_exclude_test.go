package classifier

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/conf/conftest"
)

// TestExcludeMatcher_ForwardMatch characterizes the forward matching (scientific or
// common name equality) that the matcher preserves from the old
// isSpeciesExcluded/matchesSpecies path.
func TestExcludeMatcher_ForwardMatch(t *testing.T) {
	t.Parallel()
	m := newExcludeMatcher([]string{"Turdus merula", "Great Tit"}, "en")
	assert.True(t, m.matches("Turdus merula_Common Blackbird"), "scientific-name forward match")
	assert.True(t, m.matches("Parus major_Great Tit"), "common-name forward match")
	assert.False(t, m.matches("Corvus corax_Northern Raven"), "unrelated species not excluded")
}

// TestExcludeMatcher_LocalizedCommonName_ReverseResolves is the core gap this fixes:
// a non-primary model emits a scientific-only label ("Vulpes vulpes"), whose parsed
// common name falls back to the scientific name, so a localized exclude entry (the
// Finnish "Kettu") cannot forward-match. The localized name lives only in OpenFauna, so
// the matcher must reverse-resolve "Kettu" -> "Vulpes vulpes" to exclude the label.
func TestExcludeMatcher_LocalizedCommonName_ReverseResolves(t *testing.T) {
	t.Parallel()
	m := newExcludeMatcher([]string{"Kettu"}, "fi")
	assert.True(t, m.matches("Vulpes vulpes"),
		"a localized common-name exclude must reverse-resolve to the scientific-only label")
	assert.False(t, m.matches("Turdus merula_Common Blackbird"),
		"reverse resolution must not over-exclude an unrelated species")
}

// TestExcludeMatcher_Empty verifies a zero/empty exclude list yields a cheap no-op
// matcher (no matches).
func TestExcludeMatcher_Empty(t *testing.T) {
	t.Parallel()
	m := newExcludeMatcher(nil, "fi")
	assert.False(t, m.matches("Turdus merula_Common Blackbird"))
	assert.False(t, m.matches("Vulpes vulpes"))
}

// TestExcludeMatcher_NoReverseResolution_LeavesReverseSciNil verifies that when a
// non-empty exclude list reverse-resolves to nothing (e.g. a scientific-name-only
// entry that is not a localized common name), reverseSci stays nil. This preserves
// the matches() nil-guard fast path so it skips the per-label ToLower + map lookup on
// the hot rebuild loop instead of probing an empty map for every label.
func TestExcludeMatcher_NoReverseResolution_LeavesReverseSciNil(t *testing.T) {
	t.Parallel()
	m := newExcludeMatcher([]string{"Zzz Notaspecies"}, "fi")
	assert.Nil(t, m.reverseSci, "reverseSci must stay nil when nothing reverse-resolves")
	// The forward path is unaffected: the unresolved entry simply never matches.
	assert.False(t, m.matches("Turdus merula_Common Blackbird"))
}

// TestExcludeMatcher_CaseInsensitive proves matching normalizes case on both paths:
// EqualFold on the forward (scientific/common) match, and a lower-cased scientific-name
// set on the reverse match (where the entry is also case-folded by OpenFauna's lookup).
func TestExcludeMatcher_CaseInsensitive(t *testing.T) {
	t.Parallel()
	// Lower-cased localized entry still reverse-resolves; upper-cased label still matches.
	m := newExcludeMatcher([]string{"kettu"}, "fi")
	assert.True(t, m.matches("VULPES VULPES"),
		"reverse match must be case-insensitive on both the entry and the label")

	// Forward scientific-name match stays case-insensitive, matching the old behavior.
	mf := newExcludeMatcher([]string{"TURDUS MERULA"}, "fi")
	assert.True(t, mf.matches("Turdus merula_Mustarastas"),
		"forward scientific-name match must be case-insensitive")
}

// TestExcludeMatcher_ForwardAndReverseEntry proves a single exclude entry that both
// forward-matches a localized label and reverse-resolves to a scientific name drops both
// label forms: the localized "Scientific_Common" label (forward) and a scientific-only
// label a non-primary model would emit (reverse). The two paths coexist without
// interfering.
func TestExcludeMatcher_ForwardAndReverseEntry(t *testing.T) {
	t.Parallel()
	m := newExcludeMatcher([]string{"Great Tit"}, "fi")
	assert.True(t, m.matches("Parus major_Great Tit"),
		"forward common-name match")
	assert.True(t, m.matches("Parus major"),
		"the same entry must also drop the scientific-only label via reverse resolution")
}

// excludeTestSettings builds a v3-geomodel snapshot like overrideTestSettings but
// configured for the exclude side: the geomodel surfaces a scientific-only non-primary
// label ("Vulpes vulpes", the fox) that a localized Finnish exclude entry ("Kettu")
// must drop via OpenFauna reverse resolution.
func excludeTestSettings(t *testing.T) (*conf.Settings, *fakeUniversalRangeFilter) {
	t.Helper()
	settings := conftest.GetTestSettings()
	settings.BirdNET.Latitude = 60.0
	settings.BirdNET.Longitude = 25.0
	settings.BirdNET.LocationConfigured = true
	settings.BirdNET.RangeFilter.Threshold = 0.01
	settings.BirdNET.Locale = "fi"
	settings.BirdNET.Labels = []string{
		"Turdus merula_Mustarastas",
		"Parus major_Talitiainen",
	}
	settings.Realtime.Species.Exclude = []string{"Kettu"}

	rf := &fakeUniversalRangeFilter{
		geoLabels: []string{"Turdus merula_Common Blackbird", "Vulpes vulpes"},
		scores: []SpeciesScore{
			{Score: 0.9, Label: "Turdus merula_Common Blackbird"},
			{Score: 0.8, Label: "Vulpes vulpes"},
		},
		rawScores: []float32{0.9, 0.8},
	}
	return settings, rf
}

// TestBuildRangeFilter_LocalizedExclude_DropsScientificOnlyNonPrimaryLabel proves the
// wiring of the exclude reverse-resolution on the BuildRangeFilter path: a localized
// exclude entry must reverse-resolve and remove the scientific-only label from the
// inclusion working set.
func TestBuildRangeFilter_LocalizedExclude_DropsScientificOnlyNonPrimaryLabel(t *testing.T) {
	settings, rf := excludeTestSettings(t)
	conftest.SetTestSettings(settings)
	t.Cleanup(func() { conftest.SetTestSettings(nil) })

	o := buildTestOrchestrator(t, settings, rf)
	require.NoError(t, BuildRangeFilter(o))

	included := conf.GetSettings().GetIncludedSpecies()
	assert.NotContains(t, included, "Vulpes vulpes",
		"a localized exclude entry must reverse-resolve and drop the scientific-only non-primary label")
	assert.Contains(t, included, "Turdus merula_Common Blackbird",
		"an unrelated in-range species must stay included")
}

// TestGetProbableSpecies_LocalizedExclude_DropsScientificOnlyNonPrimaryLabel proves the
// same on the getProbableSpecies path (daily UpdateRangeFilterAction / species-list API).
func TestGetProbableSpecies_LocalizedExclude_DropsScientificOnlyNonPrimaryLabel(t *testing.T) {
	settings, rf := excludeTestSettings(t)

	bn := &BirdNET{
		Settings:     settings,
		rangeFilter:  rf,
		speciesCache: make(map[string]*speciesCacheEntry),
	}

	scores, _, err := bn.getProbableSpecies(time.Now(), 0, settings)
	require.NoError(t, err)

	labels := make([]string, 0, len(scores))
	for _, ss := range scores {
		labels = append(labels, ss.Label)
	}
	assert.NotContains(t, labels, "Vulpes vulpes",
		"the daily path must also reverse-resolve a localized exclude entry")
	assert.Contains(t, labels, "Turdus merula_Common Blackbird",
		"an unrelated in-range species must stay included")
}
