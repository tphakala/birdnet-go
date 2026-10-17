package analytics

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tphakala/birdnet-go/internal/datastore"
)

// TestDailySummary_TaxonomicClass pins the note's taxonomic class through aggregation into the
// daily summary response, where the dashboard groups rows by it.
func TestDailySummary_TaxonomicClass(t *testing.T) {
	t.Parallel()

	agg := map[string]aggregatedBirdInfo{}
	note := datastore.Note{ScientificName: "Pipistrellus pipistrellus", Time: "22:10:00", TaxonomicClass: "Chiroptera"}
	counts := [24]int{22: 1}
	(&Handler{}).updateAggregatedData(agg, &note, &counts)

	data := agg[note.ScientificName]
	summary := buildSpeciesSummaryFromData(&data, "")
	assert.Equal(t, "Chiroptera", summary.TaxonomicClass)
}
