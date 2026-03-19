package wikipedia

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestIntegration_RealWikipediaAPI tests against the real Wikipedia API.
// Skipped unless WIKIPEDIA_INTEGRATION=1 is set.
func TestIntegration_RealWikipediaAPI(t *testing.T) {
	if os.Getenv("WIKIPEDIA_INTEGRATION") != "1" {
		t.Skip("Skipping integration test (set WIKIPEDIA_INTEGRATION=1 to run)")
	}

	client := NewClient()
	ctx := context.Background()

	tests := []struct {
		commonName     string
		scientificName string
	}{
		{"Masked Lapwing", "Vanellus miles"},
		{"Galah", "Eolophus roseicapilla"},
		{"Barn Owl", "Tyto alba"},
		{"Little Corella", "Cacatua sanguinea"},
		{"Australian Magpie", "Gymnorhina tibicen"},
	}

	for _, tt := range tests {
		t.Run(tt.commonName, func(t *testing.T) {
			summary, err := client.GetSummary(ctx, tt.commonName, tt.scientificName)
			require.NoError(t, err, "Failed to fetch summary for %s", tt.commonName)
			assert.NotEmpty(t, summary.Title, "Title should not be empty")
			assert.NotEmpty(t, summary.Extract, "Extract should not be empty")
			assert.NotEmpty(t, summary.ArticleURL(), "Article URL should not be empty")
			t.Logf("✓ %s: %s (%.80s...)", tt.commonName, summary.Description, summary.Extract)
		})
	}
}
