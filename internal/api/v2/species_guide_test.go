package api

import (
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/conf/conftest"
)

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

	c := &Controller{}
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

	c := &Controller{}
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

func TestBuildExternalLinks(t *testing.T) {
	t.Parallel()

	links := buildExternalLinks("Common Blackbird", sciEurasianBlackbird)
	assert.NotEmpty(t, links)

	names := make(map[string]string, len(links))
	for _, l := range links {
		names[l.Name] = l.URL
	}
	assert.Contains(t, names, "Wikipedia")
	assert.Contains(t, names["Wikipedia"], "Turdus_merula")
	assert.Contains(t, names, "eBird")
	assert.Contains(t, names, "Xeno-canto")

	assert.Empty(t, buildExternalLinks("", ""))
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
