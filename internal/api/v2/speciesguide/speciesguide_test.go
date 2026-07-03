// speciesguide_test.go: pure-helper tests for the species guide domain (quality
// classification, expectedness mapping, rarity memoization, season computation,
// external-link assembly, locale handling, and description summarization).
package speciesguide

import (
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/conf/conftest"
)

// withRestoredGlobalSettings snapshots the package-global settings pointer and
// restores it on cleanup so a test that publishes its own snapshot via
// conftest.SetTestSettings does not leak into sibling tests.
func withRestoredGlobalSettings(t *testing.T) {
	t.Helper()
	orig := conf.GetSettings()
	t.Cleanup(func() { conftest.SetTestSettings(orig) })
}

func TestClassifyGuideQuality(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		description string
		partial     bool
		want        string
	}{
		{"empty is stub", "", false, guideQualityStub},
		{"very short is stub", "tiny", false, guideQualityStub},
		{"intro without sections", "A reasonably long introduction paragraph about the bird with no markdown section headers at all here.", false, guideQualityIntroOnly},
		{"partial downgrades to intro", "A long description.\n\n## Voice\nSings beautifully across the meadow at dawn each day.", true, guideQualityIntroOnly},
		{"full with sections", "A long description of the species here that exceeds the threshold.\n\n## Voice\nSings beautifully across the meadow.", false, guideQualityFull},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, classifyGuideQuality(tt.description, tt.partial))
		})
	}
}

func TestScoreToExpectedness(t *testing.T) {
	t.Parallel()

	tests := []struct {
		score float64
		want  string
	}{
		{0.9, expectednessExpected},
		{0.51, expectednessExpected},
		{0.3, expectednessUncommon},
		{0.1, expectednessRare},
		{0.01, expectednessUnexpected},
		{0.0, expectednessUnexpected},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, scoreToExpectedness(tt.score), "score=%v", tt.score)
	}
}

// fakePredictor is a probableSpeciesPredictor that records how many times
// GetProbableSpecies was actually invoked, so the memoization can be asserted.
type fakePredictor struct {
	mu     sync.Mutex
	calls  int
	scores []classifier.SpeciesScore
}

func (p *fakePredictor) GetProbableSpecies(_ time.Time, _ float32) ([]classifier.SpeciesScore, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	return p.scores, nil
}

func (p *fakePredictor) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

// TestProbableSpeciesScores_MemoizesUnderConcurrency verifies the double-checked
// lock collapses a concurrent burst of guide requests to a single geomodel
// prediction, and that every caller gets the (shared, read-only) score map. Run
// under -race to catch a regression in the memoization locking.
func TestProbableSpeciesScores_MemoizesUnderConcurrency(t *testing.T) {
	withRestoredGlobalSettings(t)
	s := &conf.Settings{}
	s.BirdNET.Latitude = 60.17
	s.BirdNET.Longitude = 24.94
	conftest.SetTestSettings(s)

	c := New(&apicore.Core{})
	c.Settings.Store(s)
	pred := &fakePredictor{scores: []classifier.SpeciesScore{{Label: sciEurasianBlackbird, Score: 0.9}}}

	const callers = 32
	var wg sync.WaitGroup
	results := make([]map[string]float64, callers)
	for i := range callers {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			results[idx] = c.probableSpeciesScores(pred)
		}(i)
	}
	wg.Wait()

	assert.Equal(t, 1, pred.callCount(), "concurrent callers must share one geomodel prediction")
	for _, r := range results {
		require.NotNil(t, r)
		assert.InDelta(t, 0.9, r["turdus merula"], 1e-9)
	}
}

// TestProbableSpeciesScores_InvalidatesOnLocationChange verifies the memo is
// reused within the TTL for the same location but invalidated immediately when the
// configured location changes (the location-keyed cache).
func TestProbableSpeciesScores_InvalidatesOnLocationChange(t *testing.T) {
	withRestoredGlobalSettings(t)
	s := &conf.Settings{}
	s.BirdNET.Latitude = 10.0
	s.BirdNET.Longitude = 20.0
	conftest.SetTestSettings(s)

	c := New(&apicore.Core{})
	c.Settings.Store(s)
	pred := &fakePredictor{scores: []classifier.SpeciesScore{{Label: "X", Score: 0.5}}}

	c.probableSpeciesScores(pred)
	c.probableSpeciesScores(pred)
	assert.Equal(t, 1, pred.callCount(), "same location within TTL must be memoized")

	// Change location: the memo must invalidate and re-predict despite a live TTL.
	s2 := &conf.Settings{}
	s2.BirdNET.Latitude = 11.0
	s2.BirdNET.Longitude = 20.0
	conftest.SetTestSettings(s2)

	c.probableSpeciesScores(pred)
	assert.Equal(t, 2, pred.callCount(), "location change must invalidate the memo")
}

func TestComputeCurrentSeason(t *testing.T) {
	t.Parallel()

	northernJuly := time.Date(2026, time.July, 15, 0, 0, 0, 0, time.UTC)
	southernJuly := northernJuly

	assert.Equal(t, "summer", computeCurrentSeason(52.0, northernJuly), "northern July is summer")
	assert.Equal(t, "winter", computeCurrentSeason(-33.0, southernJuly), "southern July is winter")

	january := time.Date(2026, time.January, 15, 0, 0, 0, 0, time.UTC)
	assert.Equal(t, "winter", computeCurrentSeason(52.0, january), "northern January is winter")
	assert.Equal(t, "summer", computeCurrentSeason(-33.0, january), "southern January is summer")

	// Equatorial band returns wet/dry tokens.
	assert.Equal(t, "wet1", computeCurrentSeason(2.0, time.Date(2026, time.April, 1, 0, 0, 0, 0, time.UTC)))
	assert.Equal(t, "dry1", computeCurrentSeason(0.0, time.Date(2026, time.July, 1, 0, 0, 0, 0, time.UTC)))
}

func TestExternalLinksForGuide(t *testing.T) {
	t.Parallel()

	// With eBird code and German locale: expect wikipedia, inaturalist (from OpenFauna
	// Tier-1 for Turdus merula), and eBird appended; Xeno-canto only with supplementary.
	links := externalLinksForGuide(sciEurasianBlackbird, "eurbla", "de", false)
	assert.NotEmpty(t, links)

	byIcon := make(map[string]string, len(links))
	for _, l := range links {
		byIcon[l.Icon] = l.URL
		assert.NotEmpty(t, l.Icon, "every link should carry an icon hint")
	}
	// Wikipedia comes from OpenFauna via Wikidata GoToLinkedPage redirect.
	assert.Contains(t, byIcon, "wikipedia")
	assert.Contains(t, byIcon["wikipedia"], "wikidata.org")
	assert.Contains(t, byIcon["wikipedia"], "dewiki")
	// iNaturalist comes from OpenFauna (taxon id) with the UI language as ?locale=.
	assert.Contains(t, byIcon, "inaturalist")
	assert.Contains(t, byIcon["inaturalist"], "inaturalist.org/taxa/")
	assert.Contains(t, byIcon["inaturalist"], "locale=de")
	// eBird links must point at the code-based species page, not a (broken) search.
	assert.Contains(t, byIcon, "ebird")
	assert.Contains(t, byIcon["ebird"], "ebird.org/species/eurbla")
	assert.NotContains(t, byIcon["ebird"], "search?q=")
	// Xeno-canto absent when supplementary is off.
	assert.NotContains(t, byIcon, "xeno-canto")

	// With supplementary on, Xeno-canto is included.
	withSupp := externalLinksForGuide(sciEurasianBlackbird, "eurbla", "de", true)
	suppByIcon := make(map[string]string, len(withSupp))
	for _, l := range withSupp {
		suppByIcon[l.Icon] = l.URL
	}
	assert.Contains(t, suppByIcon, "xeno-canto")

	// An empty/invalid locale falls back to the English Wikipedia subdomain; without a
	// resolved eBird code the eBird link is omitted rather than emitting a dead URL.
	noCode := externalLinksForGuide(sciEurasianBlackbird, "", "", false)
	noCodeByIcon := make(map[string]string, len(noCode))
	for _, l := range noCode {
		noCodeByIcon[l.Icon] = l.URL
	}
	assert.Contains(t, noCodeByIcon, "wikipedia")
	assert.Contains(t, noCodeByIcon["wikipedia"], "enwiki")
	assert.NotContains(t, noCodeByIcon, "ebird")

	assert.Empty(t, externalLinksForGuide("", "", "en", false))
}

func TestBaseLanguage(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"de":      "de",
		"pt-br":   "pt",
		"pt_pt":   "pt",
		"zh_cn":   "zh",
		"en_uk":   "en",
		"EN":      "en",
		"":        "en", // empty -> default
		"x":       "en", // too short
		"english": "en", // too long
		"e1":      "en", // non-alpha
	}
	for in, want := range cases {
		assert.Equalf(t, want, baseLanguage(in), "baseLanguage(%q)", in)
	}
}

// TestExternalLinksForGuide_NorwegianLangMapping verifies the Wikipedia-specific
// nb->no remap is confined to Wikipedia: the Wikidata redirect uses "nowiki" (not
// "nbwiki"), while iNaturalist correctly receives the base ISO subtag ?locale=nb
// (the "no" Wikipedia project name is meaningless to iNaturalist).
func TestExternalLinksForGuide_NorwegianLangMapping(t *testing.T) {
	t.Parallel()

	links := externalLinksForGuide(sciEurasianBlackbird, "", "nb", false)
	byIcon := make(map[string]string, len(links))
	for _, l := range links {
		byIcon[l.Icon] = l.URL
	}
	// Wikipedia gets the "no" project via the source's lang_map.
	assert.Contains(t, byIcon["wikipedia"], "nowiki")
	assert.NotContains(t, byIcon["wikipedia"], "nbwiki")
	// iNaturalist keeps the base subtag — the Wikipedia mapping must NOT leak here.
	if inat, ok := byIcon["inaturalist"]; ok {
		assert.Contains(t, inat, "locale=nb")
		assert.NotContains(t, inat, "locale=no")
	}
}

func TestExternalLinksForGuideAppendsEbirdAndIcons(t *testing.T) {
	links := externalLinksForGuide("Aquila chrysaetos", "agldea", "en", false)
	var haveEbird, haveWiki bool
	for _, l := range links {
		if l.Icon == "ebird" {
			haveEbird = true
			assert.Equal(t, "https://ebird.org/species/agldea", l.URL)
		}
		if l.Icon == "wikipedia" {
			haveWiki = true
		}
		assert.NotEmpty(t, l.Icon, "every link should carry an icon hint")
	}
	assert.True(t, haveEbird, "eBird link should be appended when code present")
	assert.True(t, haveWiki, "wikipedia link should come from OpenFauna")
}

func TestExternalLinksForGuideOmitsEbirdWhenNoCode(t *testing.T) {
	links := externalLinksForGuide("Aquila chrysaetos", "", "en", false)
	for _, l := range links {
		assert.NotEqual(t, "ebird", l.Icon, "no eBird link without a code")
	}
}

func TestExternalLinksForGuideSupplementaryAddsXenoCanto(t *testing.T) {
	links := externalLinksForGuide("Aquila chrysaetos", "", "en", true)
	var haveXC bool
	for _, l := range links {
		if l.Icon == "xeno-canto" {
			haveXC = true
		}
	}
	assert.True(t, haveXC, "supplementary on should add xeno-canto")
}

func TestSummarizeDescription(t *testing.T) {
	t.Parallel()

	desc := "A short intro.\n\n## Voice\nSings."
	assert.Equal(t, "A short intro.", summarizeDescription(desc))

	long := make([]byte, guideSummaryMaxLength+50)
	for i := range long {
		long[i] = 'a'
	}
	got := summarizeDescription(string(long))
	assert.LessOrEqual(t, len(got), guideSummaryMaxLength)

	// Multi-byte input must be cut on a rune boundary, never mid-rune, so the
	// summary is always valid UTF-8 (no trailing replacement character). "é" is
	// two bytes, so a naive byte slice at the cap could land inside a rune.
	multibyte := strings.Repeat("é", guideSummaryMaxLength)
	gotMB := summarizeDescription(multibyte)
	assert.LessOrEqual(t, len(gotMB), guideSummaryMaxLength)
	assert.True(t, utf8.ValidString(gotMB), "summary must remain valid UTF-8")
}

// TestComputeCurrentSeason_AllBranches covers every hemisphere/season window and
// the equatorial bimodal wet/dry tokens.
func TestComputeCurrentSeason_AllBranches(t *testing.T) {
	t.Parallel()
	cases := []struct {
		lat   float64
		month time.Month
		want  string
	}{
		// Northern hemisphere.
		{52, time.April, "spring"}, {52, time.July, "summer"},
		{52, time.October, "autumn"}, {52, time.January, "winter"},
		// Southern hemisphere (opposite).
		{-33, time.April, "autumn"}, {-33, time.July, "winter"},
		{-33, time.October, "spring"}, {-33, time.January, "summer"},
		// Equatorial band: two wet and two dry seasons.
		{2, time.April, "wet1"}, {2, time.July, "dry1"},
		{2, time.October, "wet2"}, {2, time.January, "dry2"},
	}
	for _, tc := range cases {
		got := computeCurrentSeason(tc.lat, time.Date(2026, tc.month, 15, 0, 0, 0, 0, time.UTC))
		assert.Equalf(t, tc.want, got, "lat=%v month=%v", tc.lat, tc.month)
	}
}

// TestExpectednessAndEbirdCode_NoProcessor covers the nil-Processor guards: with
// no loaded classifier, expectedness and the eBird code resolve to empty (the
// success paths need a loaded geomodel, out of unit-test scope).
func TestExpectednessAndEbirdCode_NoProcessor(t *testing.T) {
	t.Parallel()
	c := New(&apicore.Core{}) // Processor is nil
	assert.Empty(t, c.guideExpectedness(sciEurasianBlackbird))
	assert.Empty(t, c.ebirdSpeciesCode(sciEurasianBlackbird))
}
