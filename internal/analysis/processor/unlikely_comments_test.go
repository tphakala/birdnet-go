package processor

import (
	"maps"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/detection"
)

// frontendStaticDir is the frontend static tree, relative to this package, whose
// messages directory holds one file per UI locale.
const frontendStaticDir = "../../../frontend/static"

// TestUnlikelyCommentTemplates_CoverEveryUILocale pins that the comment is
// written in every UI locale and in no other: the comment language follows the
// dashboard locale, and a missing template silently falls back to English.
func TestUnlikelyCommentTemplates_CoverEveryUILocale(t *testing.T) {
	_, err := os.Stat(frontendStaticDir + "/messages/en.json")
	require.NoError(t, err, "DiscoverUILocales falls back silently when the path is wrong")

	assert.ElementsMatch(t,
		conf.DiscoverUILocales(os.DirFS(frontendStaticDir)),
		slices.Collect(maps.Keys(unlikelyCommentTemplates)))
}

// TestFormatUnlikelyComment_EveryLocale pins that every template renders both the CV
// and the threshold, in that order, and that an unknown locale falls back to English.
func TestFormatUnlikelyComment_EveryLocale(t *testing.T) {
	const (
		cv        = 0.123
		threshold = 0.15
	)
	result := detection.Result{
		Unlikely:              true,
		UnlikelySources:       detection.UnlikelySourceUltrasonicCV,
		UltrasonicCV:          cv,
		UltrasonicCVThreshold: threshold,
	}
	for locale := range unlikelyCommentTemplates {
		t.Run(locale, func(t *testing.T) {
			got := formatUnlikelyComment(locale, &result)
			assert.Contains(t, got, "0.123")
			assert.Contains(t, got, "0.15")
			assert.NotContains(t, got, "%!")
			assert.Less(t, strings.Index(got, "0.123"), strings.Index(got, "0.15"), "CV comes before threshold")
		})
	}

	assert.Equal(t,
		formatUnlikelyComment(defaultUnlikelyCommentLocale, &result),
		formatUnlikelyComment("xx", &result))
}
