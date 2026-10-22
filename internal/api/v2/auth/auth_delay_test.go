package authapi

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// randomDelayDraws is how many delays each case draws, so a bound that is
// off by one has many chances to show.
const randomDelayDraws = 1000

// TestRandomDelay_Bounds checks that randomDelay sleeps for a duration in
// [minMs, maxMs), and that a range that is empty or reversed falls back to
// the default auth delay range instead of reaching rand.Int with a
// non-positive bound, which panics.
func TestRandomDelay_Bounds(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		minMs, maxMs int64
		wantLo       time.Duration // inclusive
		wantHi       time.Duration // exclusive
	}{
		{"normal range", 10, 20, 10 * time.Millisecond, 20 * time.Millisecond},
		{"one millisecond range", 5, 6, 5 * time.Millisecond, 6 * time.Millisecond},
		{"equal bounds use the defaults", 30, 30, authDelayMinMs * time.Millisecond, authDelayMaxMs * time.Millisecond},
		{"reversed bounds use the defaults", 200, 100, authDelayMinMs * time.Millisecond, authDelayMaxMs * time.Millisecond},
		{"zero bounds use the defaults", 0, 0, authDelayMinMs * time.Millisecond, authDelayMaxMs * time.Millisecond},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			// Inside the synctest bubble the clock is fake, so the elapsed
			// time is exactly the duration randomDelay chose.
			synctest.Test(t, func(t *testing.T) {
				seen := make(map[time.Duration]struct{})
				for range randomDelayDraws {
					start := time.Now()
					require.NotPanics(t, func() { randomDelay(t.Context(), tt.minMs, tt.maxMs) })
					elapsed := time.Since(start)
					assert.GreaterOrEqual(t, elapsed, tt.wantLo)
					assert.Less(t, elapsed, tt.wantHi)
					seen[elapsed] = struct{}{}
				}
				// A range wider than one value must produce more than one delay,
				// otherwise the jitter is constant and the mitigation is gone.
				if tt.wantHi-tt.wantLo > time.Millisecond {
					assert.Greater(t, len(seen), 1, "the delay never varied")
				}
			})
		})
	}
}

// TestRandomDelay_CancelledContext checks that a cancelled context ends the
// delay without waiting.
func TestRandomDelay_CancelledContext(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		start := time.Now()
		randomDelay(ctx, authDelayMinMs, authDelayMaxMs)
		assert.Zero(t, time.Since(start))
	})
}
