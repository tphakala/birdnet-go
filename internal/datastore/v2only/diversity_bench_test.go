package v2only

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// BenchmarkGetSpeciesDiversityData measures the per-day distinct species count
// against the BIRDNET_BENCH_DB database for several window sizes.
func BenchmarkGetSpeciesDiversityData(b *testing.B) {
	ds := openBenchDatastore(b)

	cases := []struct{ name, start, end string }{
		{"week", "2025-05-09", "2025-05-15"},
		{"month", "2025-04-16", "2025-05-15"},
		{"year", "2024-05-16", "2025-05-15"},
		{"all_time", "", ""},
	}
	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				_, err := ds.GetSpeciesDiversityData(b.Context(), tc.start, tc.end)
				require.NoError(b, err)
			}
		})
	}
}
