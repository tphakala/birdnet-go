package wallclock_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/tphakala/birdnet-go/internal/testutil"
	"github.com/tphakala/birdnet-go/internal/wallclock"
)

func TestFloorToHour(t *testing.T) {
	t.Parallel()
	zones := testutil.ToolchainZones(t)
	newYork := zones["America/New_York"]

	// The two occurrences of 01:30 on the New York fall-back night, an hour apart.
	firstOccurrence := time.Date(2026, time.November, 1, 5, 30, 0, 0, time.UTC).In(newYork)
	secondOccurrence := firstOccurrence.Add(time.Hour)

	tests := []struct {
		name  string
		input time.Time
		want  string
	}{
		{"Kolkata +05:30 first half hour", time.Date(2026, 6, 1, 10, 15, 7, 9, zones["Asia/Kolkata"]), "2026-06-01T10:00:00+05:30"},
		{"Kolkata +05:30 second half hour", time.Date(2026, 6, 1, 10, 45, 0, 0, zones["Asia/Kolkata"]), "2026-06-01T10:00:00+05:30"},
		{"Kathmandu +05:45", time.Date(2026, 6, 1, 10, 50, 0, 0, zones["Asia/Kathmandu"]), "2026-06-01T10:00:00+05:45"},
		{"St Johns -03:30 (NDT in summer)", time.Date(2026, 7, 1, 10, 50, 0, 0, zones["America/St_Johns"]), "2026-07-01T10:00:00-02:30"},
		{"New York first occurrence of a repeated hour", firstOccurrence, "2026-11-01T01:00:00-04:00"},
		{"New York second occurrence of a repeated hour", secondOccurrence, "2026-11-01T01:00:00-05:00"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := wallclock.FloorToHour(tt.input)
			assert.Equal(t, tt.want, got.Format(time.RFC3339))
			assert.False(t, got.After(tt.input), "the floor never lies after the input")
		})
	}
}

// TestFloorToHourKeepsOffsetAcrossAShiftInsideTheHour pins the 30 minute shift of Lord Howe:
// 01:50 after the clocks went back is shown at its own offset (+10:30), although an hour start
// computed in the previous period would read +11:00.
func TestFloorToHourKeepsOffsetAcrossAShiftInsideTheHour(t *testing.T) {
	t.Parallel()
	lordHowe := testutil.ToolchainZones(t)["Australia/Lord_Howe"]
	// 2026-04-05 02:00 +11:00 becomes 01:30 +10:30: the wall clock 01:30-02:00 occurs twice.
	instant := time.Date(2026, time.April, 4, 15, 20, 0, 0, time.UTC).In(lordHowe) // 01:50 +10:30
	got := wallclock.FloorToHour(instant)
	assert.Equal(t, "2026-04-05T01:00:00+10:30", got.Format(time.RFC3339))
	assert.Equal(t, 1, got.Hour())
	assert.Equal(t, 0, got.Minute())
}
