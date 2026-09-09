package analytics

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tphakala/birdnet-go/internal/datastore"
)

// TestUpdateAggregatedData_FirstHeard pins the daily summary's first_heard to the note's
// FirstTime when the datastore supplies one, and to Time otherwise. The summary queries return
// one note per species with Time = latest detection, so seeding first_heard from Time made the
// daily summary report the last call of the day as the first.
func TestUpdateAggregatedData_FirstHeard(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		note      datastore.Note
		wantFirst string
		wantLast  string
	}{
		{
			name:      "summary note carries both times",
			note:      datastore.Note{ScientificName: "Megascops asio", Time: "07:30:00", FirstTime: "05:00:00"},
			wantFirst: "05:00:00",
			wantLast:  "07:30:00",
		},
		{
			name:      "plain note has one time",
			note:      datastore.Note{ScientificName: "Megascops asio", Time: "07:30:00"},
			wantFirst: "07:30:00",
			wantLast:  "07:30:00",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			agg := map[string]aggregatedBirdInfo{}
			counts := [24]int{5: 1, 7: 2}
			(&Handler{}).updateAggregatedData(agg, &tt.note, &counts)
			got := agg[tt.note.ScientificName]
			assert.Equal(t, tt.wantFirst, got.First)
			assert.Equal(t, tt.wantLast, got.Latest)
			assert.Equal(t, 3, got.Count)
		})
	}
}
