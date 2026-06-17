package api

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
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

	links := buildExternalLinks("Common Blackbird", "Turdus merula")
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

func TestSplitGuideSections(t *testing.T) {
	t.Parallel()

	desc := "Intro paragraph.\n\n## Voice\nSings.\n\n## Habitat\nForests."
	sections := splitGuideSections(desc)
	require := assert.New(t)
	require.Len(sections, 3)
	require.Empty(sections[0].heading)
	require.Equal("Intro paragraph.", sections[0].body)
	require.Equal("Voice", sections[1].heading)
	require.Equal("Sings.", sections[1].body)
	require.Equal("Habitat", sections[2].heading)
}

func TestExtractSections(t *testing.T) {
	t.Parallel()

	desc := "An intro about the bird.\n\n## Voice\nA melodic song.\n\n## Habitat\nWoodlands."
	secs := extractSections(desc, []string{"Turdus pilaris"}, "en")
	assert.NotNil(t, secs)
	assert.Equal(t, "An intro about the bird.", secs.Description)
	assert.Equal(t, "A melodic song.", secs.SongsAndCalls)
	assert.Equal(t, []string{"Turdus pilaris"}, secs.SimilarSpecies)

	// Localized songs heading (German "Stimme") is matched.
	deDesc := "Einleitung.\n\n## Stimme\nSchöner Gesang."
	deSecs := extractSections(deDesc, nil, "de")
	assert.Equal(t, "Schöner Gesang.", deSecs.SongsAndCalls)

	assert.Nil(t, extractSections("", nil, "en"))
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
}
