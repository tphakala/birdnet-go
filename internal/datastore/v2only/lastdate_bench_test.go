package v2only

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// benchLastDateCutoff is the "before" date used by the last-detection benchmarks,
// late in the benchmark database's range so every species has earlier history.
const benchLastDateCutoff = "2026-06-01"

// BenchmarkGetSpeciesLastDetectionDateBefore measures the per-species lookup the
// species tracker runs for each species with an active novelty window, against the
// BIRDNET_BENCH_DB database.
func BenchmarkGetSpeciesLastDetectionDateBefore(b *testing.B) {
	ds := openBenchDatastore(b)
	species := pickBenchSpecies(b, ds)

	cases := []struct{ name, species string }{
		{"dominant", species.dominant},
		{"common", species.common},
		{"rare", species.rare},
		{"unknown", "Nonexistent species"},
	}
	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				_, err := ds.GetSpeciesLastDetectionDateBefore(b.Context(), tc.species, benchLastDateCutoff)
				require.NoError(b, err)
			}
		})
	}

	// all_species looks up every label in turn, the shape of a tracker sync that
	// finds every species inside its novelty window.
	b.Run("all_species", func(b *testing.B) {
		var names []string
		require.NoError(b, ds.manager.DB().Table(ds.manager.TablePrefix()+"labels").Order("id").Pluck("scientific_name", &names).Error)
		b.ReportAllocs()
		for b.Loop() {
			for _, name := range names {
				_, err := ds.GetSpeciesLastDetectionDateBefore(b.Context(), name, benchLastDateCutoff)
				require.NoError(b, err)
			}
		}
	})
}
