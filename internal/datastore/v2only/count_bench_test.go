package v2only

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// benchDay is the day the single-day benchmarks query; see benchDBEnv for the
// date range the benchmark database must cover.
const benchDay = "2025-05-15"

// BenchmarkCountSpeciesDetections measures the species detection count behind
// /api/v2/detections?queryType=species against the BIRDNET_BENCH_DB database.
func BenchmarkCountSpeciesDetections(b *testing.B) {
	ds := openBenchDatastore(b)
	species := pickBenchSpecies(b, ds)

	cases := []struct {
		name, species, date, hour string
		duration                  int
	}{
		{"dominant_all_time", species.dominant, "", "", 0},
		{"dominant_day", species.dominant, benchDay, "", 0},
		{"dominant_hour", species.dominant, benchDay, "06", 1},
		{"rare_all_time", species.rare, "", "", 0},
	}
	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			// A database that does not cover the interval would time an empty count.
			n, err := ds.CountSpeciesDetections(tc.species, tc.date, tc.hour, tc.duration)
			require.NoError(b, err)
			require.Positive(b, n, "benchmark database has no detections for this case")
			b.ReportAllocs()
			for b.Loop() {
				_, err := ds.CountSpeciesDetections(tc.species, tc.date, tc.hour, tc.duration)
				require.NoError(b, err)
			}
		})
	}
}

// BenchmarkCountHourlyDetections measures the hourly detection count behind
// /api/v2/detections?queryType=hourly against the BIRDNET_BENCH_DB database.
func BenchmarkCountHourlyDetections(b *testing.B) {
	ds := openBenchDatastore(b)

	cases := []struct {
		name, date, hour string
		duration         int
	}{
		{"one_hour", benchDay, "06", 1},
		{"full_day", benchDay, "00", 24},
	}
	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			// A database that does not cover the interval would time an empty count.
			n, err := ds.CountHourlyDetections(tc.date, tc.hour, tc.duration)
			require.NoError(b, err)
			require.Positive(b, n, "benchmark database has no detections for this case")
			b.ReportAllocs()
			for b.Loop() {
				_, err := ds.CountHourlyDetections(tc.date, tc.hour, tc.duration)
				require.NoError(b, err)
			}
		})
	}
}
