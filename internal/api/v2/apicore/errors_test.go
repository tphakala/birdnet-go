package apicore

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	// correlationIDAlphabet is the character set callers see in correlation IDs.
	// It is deliberately a copy of the implementation's set, so a change to
	// either one is noticed.
	correlationIDAlphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

	// correlationIDLength is the number of characters in a correlation ID.
	correlationIDLength = 8

	// correlationIDSamples is how many IDs the distribution test draws.
	correlationIDSamples = 50000

	// correlationIDTolerance is the allowed relative deviation of a character's
	// count from the uniform expectation. Honest sampling stays within about 4%;
	// the modulo mapping this replaced is off by more than 20%.
	correlationIDTolerance = 0.10
)

// TestGenerateCorrelationID_UniformOverCharset checks the shape of generated
// IDs and that every character of the alphabet is equally likely.
func TestGenerateCorrelationID_UniformOverCharset(t *testing.T) {
	t.Parallel()

	counts := make(map[rune]int, len(correlationIDAlphabet))
	for range correlationIDSamples {
		id := GenerateCorrelationID()
		require.Len(t, id, correlationIDLength)
		for _, r := range id {
			counts[r]++
		}
	}

	expected := float64(correlationIDSamples*correlationIDLength) / float64(len(correlationIDAlphabet))
	for _, r := range correlationIDAlphabet {
		assert.InDelta(t, expected, float64(counts[r]), correlationIDTolerance*expected, "character %q", r)
	}
	assert.Len(t, counts, len(correlationIDAlphabet), "characters outside the alphabet")
}
