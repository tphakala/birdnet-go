// analytics_invariant_test.go: tests for the hourly-distribution
// unparseable-time self-check (GitHub #3388).
package datastore

import (
	"bytes"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/logger"
)

// captureDatastoreLogs swaps in a process-global logger that writes to a
// buffer, runs fn, restores the previous global, and returns what was logged.
func captureDatastoreLogs(t *testing.T, fn func()) string {
	t.Helper()
	var buf bytes.Buffer
	handler := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	cfg := &logger.LoggingConfig{
		DefaultLevel: "debug",
		Console:      &logger.ConsoleOutput{Enabled: false},
		FileOutput:   &logger.FileOutput{Enabled: false},
	}
	cl, err := logger.NewCentralLogger(cfg, handler)
	require.NoError(t, err, "failed to create test logger")

	oldGlobal := logger.Global()
	logger.SetGlobal(cl)
	t.Cleanup(func() { logger.SetGlobal(oldGlobal) })

	fn()
	return buf.String()
}

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
	// Not parallel: captureDatastoreLogs swaps the process-global logger.

	t.Run("warns when some detections have an unparseable time", func(t *testing.T) {
		ds := setupTestDB(t)
		seedHourlyDetection(t, ds, "08:30:00") // valid
		seedHourlyDetection(t, ds, "")         // empty -> strftime NULL
		seedHourlyDetection(t, ds, "garbage")  // malformed -> strftime NULL

		var results []HourlyDistributionData
		out := captureDatastoreLogs(t, func() {
			var err error
			results, err = ds.GetHourlyDistribution(t.Context(), "", "", "")
			require.NoError(t, err)
		})
		require.NotEmpty(t, results, "valid rows still produce a bucket, so the check runs")
		assert.Contains(t, out, "level=WARN")
		assert.Contains(t, out, hourlyUnparseableMsg)
		assert.Contains(t, out, "unparseable_time_rows=2")
	})

	t.Run("warns even when every detection has an unparseable time", func(t *testing.T) {
		ds := setupTestDB(t)
		seedHourlyDetection(t, ds, "")         // NULL hour
		seedHourlyDetection(t, ds, "not-time") // NULL hour

		// All rows collapse into one NULL(->0) bucket, so results is non-empty
		// (this is the real #3388 shape) and the check still fires.
		out := captureDatastoreLogs(t, func() {
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
		out := captureDatastoreLogs(t, func() {
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
		out := captureDatastoreLogs(t, func() {
			var err error
			results, err = ds.GetHourlyDistribution(t.Context(), "", "", "")
			require.NoError(t, err)
		})
		assert.Empty(t, results)
		assert.NotContains(t, out, hourlyUnparseableMsg,
			"an empty database is expected, not an anomaly")
	})
}
