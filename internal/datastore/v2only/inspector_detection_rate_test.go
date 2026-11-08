package v2only

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	detectionRateHourLayout = "2006-01-02T15:00:00Z"
	detectionRateDayLayout  = "2006-01-02"
	detectionRateSpecies    = "Turdus merula"
	detectionRateDailyDays  = 7
)

// TestV2OnlyDatastore_DetectionRateLabelsUTC pins that the detection rate
// buckets are labelled by the UTC hour and UTC date of each detection's instant.
func TestV2OnlyDatastore_DetectionRateLabelsUTC(t *testing.T) {
	t.Parallel()

	ds, cleanup := setupTestDatastore(t)
	defer cleanup()
	assertDetectionRateLabelsUTC(t, ds)
}

// assertDetectionRateLabelsUTC seeds detections around hour and day boundaries
// and checks GetDetectionRate24h and GetDetectionRateDaily. It is shared by the
// SQLite test and the MySQL/MariaDB integration test, which runs it with a
// non-UTC server time zone.
func assertDetectionRateLabelsUTC(t *testing.T, ds *Datastore) {
	t.Helper()

	now := time.Now().UTC()

	// Hourly: the UTC hour containing now-5h, plus both edges of that hour.
	ref := now.Add(-5 * time.Hour)
	h := time.Date(ref.Year(), ref.Month(), ref.Day(), ref.Hour(), 0, 0, 0, time.UTC)
	inWindow := []time.Time{
		h,
		h.Add(time.Hour - time.Second),
		h.Add(-time.Second),
		now.Add(-23 * time.Hour),
	}
	for _, at := range inWindow {
		seedDetection(t, ds, detectionRateSpecies, at)
	}
	outsideHourly := now.Add(-25 * time.Hour) // outside the 24 h window, inside the daily one
	seedDetection(t, ds, detectionRateSpecies, outsideHourly)

	wantHours := map[string]int64{}
	for _, at := range inWindow {
		wantHours[at.UTC().Format(detectionRateHourLayout)]++
	}

	hourly, err := ds.GetDetectionRate24h()
	require.NoError(t, err)
	gotHours := map[string]int64{}
	for i, r := range hourly {
		gotHours[r.Hour] = r.Count
		if i > 0 {
			assert.Less(t, hourly[i-1].Hour, r.Hour, "hour labels must be ascending")
		}
	}
	assert.Equal(t, wantHours, gotHours)

	// Daily: two rows on one UTC date that fall on different dates at +05:45,
	// and one row outside the window. The hourly rows above count too.
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	d3 := today.AddDate(0, 0, -3)
	d3Rows := []time.Time{d3.Add(2 * time.Hour), d3.Add(20 * time.Hour)}
	for _, at := range d3Rows {
		seedDetection(t, ds, detectionRateSpecies, at)
	}
	inDaily := append(append([]time.Time{outsideHourly}, d3Rows...), inWindow...)
	seedDetection(t, ds, detectionRateSpecies, today.AddDate(0, 0, -10).Add(12*time.Hour))
	wantDays := map[string]int64{}
	for _, at := range inDaily {
		wantDays[at.UTC().Format(detectionRateDayLayout)]++
	}

	daily, err := ds.GetDetectionRateDaily(detectionRateDailyDays)
	require.NoError(t, err)
	gotDays := map[string]int64{}
	for _, r := range daily {
		gotDays[r.Date] = r.Count
	}
	assert.Equal(t, wantDays, gotDays)
}
