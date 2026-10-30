package wallclock_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/tphakala/birdnet-go/internal/wallclock"
)

// TestNoon verifies that Noon anchors to the local calendar day at 12:00 in the input's own
// location, instead of time.Now().Truncate(24h), which rounds to UTC midnight and reports the
// UTC calendar day near the local day boundary.
func TestNoon(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		now     time.Time
		wantDay int
	}{
		{
			name:    "positive offset late evening keeps local day",
			now:     time.Date(2026, 7, 8, 23, 30, 0, 0, time.FixedZone("UTC+2", 2*60*60)),
			wantDay: 8,
		},
		{
			// 2026-07-08 22:30 UTC: local date (9th) != UTC date (8th).
			name:    "positive offset after local midnight keeps local day",
			now:     time.Date(2026, 7, 9, 0, 30, 0, 0, time.FixedZone("UTC+2", 2*60*60)),
			wantDay: 9,
		},
		{
			// 2026-07-08 10:00 UTC-5 = 15:00 UTC. Truncate(24h) rounds to UTC midnight, the
			// previous local day (7th) in a west zone; Noon keeps the local 8th.
			name:    "negative offset daytime keeps local day",
			now:     time.Date(2026, 7, 8, 10, 0, 0, 0, time.FixedZone("UTC-5", -5*60*60)),
			wantDay: 8,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := wallclock.Noon(tc.now)

			assert.Equal(t, tc.now.Year(), got.Year(), "year")
			assert.Equal(t, tc.now.Month(), got.Month(), "month")
			assert.Equal(t, tc.wantDay, got.Day(), "local calendar day")
			assert.Equal(t, 12, got.Hour(), "anchored to noon")
			assert.Equal(t, tc.now.Location(), got.Location(), "keeps input location")
		})
	}
}
