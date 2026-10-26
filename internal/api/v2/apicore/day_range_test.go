package apicore

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/testutil"
)

func TestResolveDayRangeErrors(t *testing.T) {
	t.Parallel()

	t.Run("an unparseable start names start_date and keeps the parse error", func(t *testing.T) {
		t.Parallel()
		_, err := ResolveDayRange("2026-02-30", "", 30, time.UTC)
		var parseErr *time.ParseError
		require.ErrorAs(t, err, &parseErr)
		assert.Equal(t, "start_date: "+parseErr.Error(), err.Error())
	})

	t.Run("an unparseable end names end_date", func(t *testing.T) {
		t.Parallel()
		_, err := ResolveDayRange("2026-03-01", "2026-3-02", 30, time.UTC)
		require.Error(t, err)
		assert.ErrorContains(t, err, "end_date: parsing time")
	})

	t.Run("an end before the start reads end_date before start_date", func(t *testing.T) {
		t.Parallel()
		_, err := ResolveDayRange("2026-03-10", "2026-03-01", 30, time.UTC)
		require.Error(t, err)
		assert.Equal(t, "end_date before start_date", err.Error())
	})
}

func TestResolveDayRangeBounds(t *testing.T) {
	t.Parallel()
	plus3 := time.FixedZone("UTC+3", 3*3600)

	r, err := ResolveDayRange("2026-03-01", "2026-03-02", 30, plus3)
	require.NoError(t, err)
	assert.Equal(t, "2026-03-02", r.EndDate.String())
	assert.Equal(t, 2, r.Days)
	assert.Equal(t, time.Date(2026, 3, 1, 0, 0, 0, 0, plus3).Unix(), r.Start.Unix())
	assert.Equal(t, time.Date(2026, 3, 3, 0, 0, 0, 0, plus3).Unix(), r.End.Unix())

	r, err = ResolveDayRange("2026-03-01", "", 30, time.UTC)
	require.NoError(t, err)
	assert.Equal(t, "2026-03-31", r.EndDate.String(), "an empty end adds the default days")
	assert.Equal(t, 31, r.Days)
}

// TestResolveDayRangeSkippedDate pins that a date which does not exist in the zone (Samoa skipped
// 2011-12-30 when it crossed the date line) resolves to an empty window of one day.
func TestResolveDayRangeSkippedDate(t *testing.T) {
	t.Parallel()
	apia := testutil.ToolchainZones(t)["Pacific/Apia"]
	require.NotNil(t, apia)

	r, err := ResolveDayRange("2011-12-30", "2011-12-30", 1, apia)
	require.NoError(t, err)
	assert.True(t, r.Start.Equal(r.End), "a skipped date is an empty window")
	assert.Equal(t, 1, r.Days)
}
