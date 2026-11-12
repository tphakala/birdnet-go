package processor

import (
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/analysis/noisebank"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/detection"
)

// frontendStaticDir is the frontend static tree, relative to this package, whose
// messages directory holds one file per UI locale.
const frontendStaticDir = "../../../frontend/static"

// The figures every comment test renders.
const (
	commentTestCV         = 0.123
	commentTestCVLimit    = 0.15
	commentTestSimilarity = 0.876
)

// unlikelyCommentMaps lists the template maps by the sources they serve.
var unlikelyCommentMaps = map[string]map[string]string{
	"ultrasonic CV":           unlikelyCommentTemplates,
	"noise examples":          unlikelyNoiseCommentTemplates,
	"ultrasonic CV and noise": unlikelyCombinedCommentTemplates,
}

// TestUnlikelyCommentTemplates_CoverEveryUILocale pins that every comment is
// written in every UI locale and in no other: the comment language follows the
// dashboard locale, and a missing template silently falls back to English.
func TestUnlikelyCommentTemplates_CoverEveryUILocale(t *testing.T) {
	_, err := os.Stat(frontendStaticDir + "/messages/en.json")
	require.NoError(t, err, "DiscoverUILocales falls back silently when the path is wrong")

	locales := conf.DiscoverUILocales(os.DirFS(frontendStaticDir))
	for name, templates := range unlikelyCommentMaps {
		t.Run(name, func(t *testing.T) {
			assert.ElementsMatch(t, locales, slices.Collect(maps.Keys(templates)))
		})
	}
}

// TestFormatUnlikelyComment_EveryLocale pins that every template of every source
// set renders its figures in the order of the sentence (CV and its threshold, then
// the noise similarity and its threshold), and that an unknown locale falls back to
// English.
func TestFormatUnlikelyComment_EveryLocale(t *testing.T) {
	noiseLimit := fmt.Sprintf("%.2f", noisebank.MatchSimilarity)

	tests := []struct {
		name    string
		sources detection.UnlikelySource
		maps    string
		want    []string // rendered figures, in order
	}{
		{"ultrasonic CV", detection.UnlikelySourceUltrasonicCV, "ultrasonic CV", []string{"0.123", "0.15"}},
		{"noise examples", detection.UnlikelySourceNoiseExamples, "noise examples", []string{"0.876", noiseLimit}},
		{"both", detection.UnlikelySourceUltrasonicCV | detection.UnlikelySourceNoiseExamples, "ultrasonic CV and noise", []string{"0.123", "0.15", "0.876", noiseLimit}},
	}
	for _, tt := range tests {
		result := detection.Result{
			Unlikely:              true,
			UnlikelySources:       tt.sources,
			UltrasonicCV:          commentTestCV,
			UltrasonicCVThreshold: commentTestCVLimit,
			NoiseSimilarity:       commentTestSimilarity,
		}
		for locale, template := range unlikelyCommentMaps[tt.maps] {
			t.Run(tt.name+"/"+locale, func(t *testing.T) {
				got := formatUnlikelyComment(locale, &result)
				assert.Equal(t, fmt.Sprintf(template, figures(tt.sources)...), got, "the template of the sources is the one used")
				assert.NotContains(t, got, "%!")
				last := -1
				for _, figure := range tt.want {
					at := strings.Index(got, figure)
					require.GreaterOrEqual(t, at, 0, "figure %s is rendered", figure)
					assert.Greater(t, at, last, "figure %s comes after the one before it", figure)
					last = at
				}
			})
		}
		assert.Equal(t,
			formatUnlikelyComment(defaultUnlikelyCommentLocale, &result),
			formatUnlikelyComment("xx", &result))
	}
}

// figures returns the format arguments a template of the given sources takes.
func figures(sources detection.UnlikelySource) []any {
	switch sources {
	case detection.UnlikelySourceNoiseExamples:
		return []any{commentTestSimilarity, noisebank.MatchSimilarity}
	case detection.UnlikelySourceUltrasonicCV:
		return []any{commentTestCV, commentTestCVLimit}
	default:
		return []any{commentTestCV, commentTestCVLimit, commentTestSimilarity, noisebank.MatchSimilarity}
	}
}
