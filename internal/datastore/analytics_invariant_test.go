// analytics_invariant_test.go: tests for the hourly by-hour unparseable-time
// self-check (GitHub #3388), covering both GetHourlyDistribution and its
// sibling GetHourlyAnalyticsData.
package datastore

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/logger/logtest"
)

const hourlyUnparseableMsg = "detections have an unparseable time and were excluded from the by-hour chart"

// seedHourlyDetection inserts a detection with the given time value (which may
// be empty or malformed to exercise the unparseable-time path).
func seedHourlyDetection(t *testing.T, ds *DataStore, timeVal string) {
	t.Helper()
	require.NoError(t, ds.DB.Create(&Note{
		Date:           "2024-07-15",
		Time:           timeVal,
		ScientificName: "Turdus migratorius",
		CommonName:     "American Robin",
		Confidence:     0.9,
	}).Error)
}

// TestHourlyDistributionUnparseableTimeSelfCheck covers the #3388 invariant: the
// WARN fires only when matching detections have a time the hour expression
// cannot parse (NULL hour), and stays silent for well-formed times or an empty
// database. It goes through the real GetHourlyDistribution entry point so the
// trigger path (aggregation returns a bucket, but some rows are NULL-hour) is
// exercised, not just the helper in isolation.
func TestHourlyDistributionUnparseableTimeSelfCheck(t *testing.T) {
	// Not parallel: logtest.Capture swaps the process-global logger.

	t.Run("warns when some detections have an unparseable time", func(t *testing.T) {
		ds := setupTestDB(t)
		seedHourlyDetection(t, ds, "08:30:00") // valid
		seedHourlyDetection(t, ds, "")         // empty -> strftime NULL
		seedHourlyDetection(t, ds, "garbage")  // malformed -> strftime NULL

		var results []HourlyDistributionData
		out := logtest.Capture(t, func() {
			var err error
			results, err = ds.GetHourlyDistribution(t.Context(), "", "", "")
			require.NoError(t, err)
		})
		require.NotEmpty(t, results, "valid rows still produce a bucket, so the check runs")
		assert.Contains(t, out, "level=WARN")
		assert.Contains(t, out, hourlyUnparseableMsg)
		assert.Contains(t, out, "operation=get_hourly_distribution")
		assert.Contains(t, out, "unparseable_time_rows=2")
	})

	t.Run("warns even when every detection has an unparseable time", func(t *testing.T) {
		ds := setupTestDB(t)
		seedHourlyDetection(t, ds, "")         // NULL hour
		seedHourlyDetection(t, ds, "not-time") // NULL hour

		// All rows collapse into one NULL(->0) bucket, so results is non-empty
		// (this is the real #3388 shape) and the check still fires.
		out := logtest.Capture(t, func() {
			_, err := ds.GetHourlyDistribution(t.Context(), "", "", "")
			require.NoError(t, err)
		})
		assert.Contains(t, out, hourlyUnparseableMsg)
		assert.Contains(t, out, "unparseable_time_rows=2")
	})

	t.Run("silent on well-formed times", func(t *testing.T) {
		ds := setupTestDB(t)
		seedHourlyDetection(t, ds, "08:30:00")
		seedHourlyDetection(t, ds, "21:15:00")

		var results []HourlyDistributionData
		out := logtest.Capture(t, func() {
			var err error
			results, err = ds.GetHourlyDistribution(t.Context(), "", "", "")
			require.NoError(t, err)
		})
		require.NotEmpty(t, results, "well-formed times must produce buckets, so the check actually runs")
		assert.NotContains(t, out, hourlyUnparseableMsg,
			"well-formed times must not warn")
	})

	t.Run("silent on an empty database", func(t *testing.T) {
		ds := setupTestDB(t)

		var results []HourlyDistributionData
		out := logtest.Capture(t, func() {
			var err error
			results, err = ds.GetHourlyDistribution(t.Context(), "", "", "")
			require.NoError(t, err)
		})
		assert.Empty(t, results)
		assert.NotContains(t, out, hourlyUnparseableMsg,
			"an empty database is expected, not an anomaly")
	})
}

// TestHourlyAnalyticsUnparseableTimeSelfCheck covers the sibling self-check on
// GetHourlyAnalyticsData (GitHub #1587 follow-up): it must WARN under exactly
// the same unparseable-time condition, tagged with its own operation, and stay
// silent for well-formed times, an empty database, and a midnight-only case
// (hour-0 bucket present but no NULL rows).
func TestHourlyAnalyticsUnparseableTimeSelfCheck(t *testing.T) {
	// Not parallel: logtest.Capture swaps the process-global logger.

	t.Run("warns when some detections have an unparseable time", func(t *testing.T) {
		ds := setupTestDB(t)
		seedHourlyDetection(t, ds, "08:30:00") // valid
		seedHourlyDetection(t, ds, "")         // empty -> strftime NULL
		seedHourlyDetection(t, ds, "garbage")  // malformed -> strftime NULL

		var results []HourlyAnalyticsData
		out := logtest.Capture(t, func() {
			var err error
			results, err = ds.GetHourlyAnalyticsData(t.Context(), "", "")
			require.NoError(t, err)
		})
		require.NotEmpty(t, results, "valid rows still produce a bucket, so the check runs")
		assert.Contains(t, out, "level=WARN")
		assert.Contains(t, out, hourlyUnparseableMsg)
		assert.Contains(t, out, "operation=get_hourly_analytics_data")
		assert.Contains(t, out, "unparseable_time_rows=2")
	})

	t.Run("date and species filter each scope the self-check to matching rows", func(t *testing.T) {
		ds := setupTestDB(t)
		// The queried species on the queried date, unparseable time -> counted.
		require.NoError(t, ds.DB.Create(&Note{
			Date: "2024-07-15", Time: "", ScientificName: "Turdus migratorius",
			CommonName: "American Robin", Confidence: 0.9,
		}).Error)
		// Same species, wrong date, also unparseable -> excluded by the DATE
		// filter alone.
		require.NoError(t, ds.DB.Create(&Note{
			Date: "2024-07-16", Time: "garbage", ScientificName: "Turdus migratorius",
			CommonName: "American Robin", Confidence: 0.9,
		}).Error)
		// Right date, wrong species, also unparseable -> excluded by the SPECIES
		// filter alone. Together these prove the self-check applies BOTH filters,
		// exactly as the aggregation does, rather than counting every
		// unparseable row in the table.
		require.NoError(t, ds.DB.Create(&Note{
			Date: "2024-07-15", Time: "not-a-time", ScientificName: "Cyanocitta cristata",
			CommonName: "Blue Jay", Confidence: 0.9,
		}).Error)

		out := logtest.Capture(t, func() {
			_, err := ds.GetHourlyAnalyticsData(t.Context(), "2024-07-15", "Turdus migratorius")
			require.NoError(t, err)
		})
		assert.Contains(t, out, hourlyUnparseableMsg)
		assert.Contains(t, out, "operation=get_hourly_analytics_data")
		assert.Contains(t, out, "unparseable_time_rows=1",
			"only the row matching BOTH the date and species filter must be counted, "+
				"not the rows excluded by date-only or species-only")
	})

	t.Run("silent on well-formed times", func(t *testing.T) {
		ds := setupTestDB(t)
		seedHourlyDetection(t, ds, "08:30:00")
		seedHourlyDetection(t, ds, "21:15:00")

		var results []HourlyAnalyticsData
		out := logtest.Capture(t, func() {
			var err error
			results, err = ds.GetHourlyAnalyticsData(t.Context(), "", "")
			require.NoError(t, err)
		})
		require.NotEmpty(t, results, "well-formed times must produce buckets, so the check actually runs")
		assert.NotContains(t, out, hourlyUnparseableMsg, "well-formed times must not warn")
	})

	t.Run("silent on a midnight-only dataset (hour-0 bucket, no NULL rows)", func(t *testing.T) {
		ds := setupTestDB(t)
		seedHourlyDetection(t, ds, "00:10:00") // legitimate midnight -> hour 0
		seedHourlyDetection(t, ds, "00:45:00")

		var results []HourlyAnalyticsData
		out := logtest.Capture(t, func() {
			var err error
			results, err = ds.GetHourlyAnalyticsData(t.Context(), "", "")
			require.NoError(t, err)
		})
		require.NotEmpty(t, results, "midnight detections still produce an hour-0 bucket")
		assert.NotContains(t, out, hourlyUnparseableMsg,
			"a real midnight bucket must not be mistaken for unparseable times")
	})

	t.Run("silent on an empty database", func(t *testing.T) {
		ds := setupTestDB(t)

		var results []HourlyAnalyticsData
		out := logtest.Capture(t, func() {
			var err error
			results, err = ds.GetHourlyAnalyticsData(t.Context(), "", "")
			require.NoError(t, err)
		})
		assert.Empty(t, results)
		assert.NotContains(t, out, hourlyUnparseableMsg,
			"an empty database is expected, not an anomaly")
	})
}
