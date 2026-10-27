package wallclock_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/tphakala/birdnet-go/internal/testutil"
	"github.com/tphakala/birdnet-go/internal/wallclock"
)

// TestResolveNilLocationIsLocal swaps time.Local, so it must not run in parallel.
func TestResolveNilLocationIsLocal(t *testing.T) {
	newYork := testutil.ToolchainZones(t)["America/New_York"]
	previous := time.Local
	time.Local = newYork
	t.Cleanup(func() { time.Local = previous })

	// 01:30 on the New York fall-back night happens twice; the first occurrence is EDT.
	d := date(2026, time.November, 1)
	got := wallclock.At(d, 1, 30, 0, nil)
	assert.Equal(t, wallclock.At(d, 1, 30, 0, newYork).Unix(), got.Unix())
	assert.Equal(t, "2026-11-01T01:30:00-04:00", got.Format(time.RFC3339))

	startNil, endNil := wallclock.DayBounds(d, nil)
	startNY, endNY := wallclock.DayBounds(d, newYork)
	assert.Equal(t, startNY.Unix(), startNil.Unix())
	assert.Equal(t, endNY.Unix(), endNil.Unix())
}

// TestAtInZonesWithoutTransitions pins the result in zones that have no zone period before the
// instant: UTC, a fixed zone, and a date before the first transition of a named zone. At returns
// the plain wall time there.
func TestAtInZonesWithoutTransitions(t *testing.T) {
	t.Parallel()
	helsinki := testutil.ToolchainZones(t)["Europe/Helsinki"]

	tests := []struct {
		name string
		loc  *time.Location
		d    wallclock.Date
		want string
	}{
		{"UTC", time.UTC, date(2026, time.June, 1), "2026-06-01T12:30:00Z"},
		{"fixed zone", time.FixedZone("X", 5*secondsPerHour+30*secondsPerMinute), date(2026, time.June, 1), "2026-06-01T12:30:00+05:30"},
		{"before the first transition of a named zone", helsinki, date(1800, time.June, 1), "1800-06-01T12:30:00+01:39"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := wallclock.At(tt.d, 12, 30, 0, tt.loc)
			assert.Equal(t, tt.want, got.Format(time.RFC3339))
			start, end := wallclock.DayBounds(tt.d, tt.loc)
			assert.Equal(t, secondsPerDay, int(end.Unix()-start.Unix()), "a day in a zone without transitions is 24 hours")
		})
	}
}
