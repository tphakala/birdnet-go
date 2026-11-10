package processor

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestFormatUnlikelyComment_EveryLocale pins that every template renders both the CV
// and the threshold, in that order, and that an unknown locale falls back to English.
func TestFormatUnlikelyComment_EveryLocale(t *testing.T) {
	const (
		cv        = 0.123
		threshold = 0.15
	)
	for locale := range unlikelyCommentTemplates {
		t.Run(locale, func(t *testing.T) {
			got := formatUnlikelyComment(locale, cv, threshold)
			assert.Contains(t, got, "0.123")
			assert.Contains(t, got, "0.15")
			assert.NotContains(t, got, "%!")
			assert.Less(t, strings.Index(got, "0.123"), strings.Index(got, "0.15"), "CV comes before threshold")
		})
	}

	assert.Equal(t,
		formatUnlikelyComment(defaultUnlikelyCommentLocale, cv, threshold),
		formatUnlikelyComment("xx", cv, threshold))
}
