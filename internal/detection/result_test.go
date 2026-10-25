package detection

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestResultModelContrib_HasHitStats pins each condition of the extended-statistics
// unit: a missing hit count or either missing hit time must read as "no stats", so
// the datastore stores NULL instead of a year-0001 Unix millisecond value.
func TestResultModelContrib_HasHitStats(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		c    ResultModelContrib
		want bool
	}{
		{"all present", ResultModelContrib{HitCount: 1, FirstHitAt: at, LastHitAt: at}, true},
		{"no hits", ResultModelContrib{FirstHitAt: at, LastHitAt: at}, false},
		{"first time missing", ResultModelContrib{HitCount: 1, LastHitAt: at}, false},
		{"last time missing", ResultModelContrib{HitCount: 1, FirstHitAt: at}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, tt.c.HasHitStats())
		})
	}
}
