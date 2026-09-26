// species_bench_test.go: benchmark and allocation ceiling for GET /api/v2/species.
//
// Before the species-index snapshot refactor the endpoint made tens of thousands of
// short-lived allocations per request, one openfauna.CanonicalName (a TrimSpace +
// ToLower) per label across three linear passes over the full label union and the
// geomodel vocabulary. These pin that the snapshot memo maps removed that storm.
package species

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/speciesindex"
)

// benchLabelCount and benchGeomodelCount size the worst-case working set: a 21K-label
// union (a v2.4 + Perch + bat install padded) and a 12K-entry geomodel vocabulary.
const (
	benchLabelCount    = 21000
	benchGeomodelCount = 12000
)

// benchLabels pads the real corpus with synthetic scientific-only labels up to
// benchLabelCount so the resolver walks a production-scale union.
func benchLabels(tb testing.TB) []string {
	tb.Helper()
	base := goldenCorpus(tb)
	labels := make([]string, 0, benchLabelCount)
	labels = append(labels, base...)
	for i := len(labels); i < benchLabelCount; i++ {
		labels = append(labels, fmt.Sprintf("Synthetica species%d_Synthetic Bird %d", i, i))
	}
	return labels
}

// benchUniversalContext builds a universal-geomodel rarity context whose vocabulary
// has benchGeomodelCount entries, covering and scoring the benchmark request species.
func benchUniversalContext() classifier.RarityContext {
	vocab := make([]string, 0, benchGeomodelCount)
	vocab = append(vocab, "Turdus merula_syn", "Spilopelia senegalensis_syn")
	for i := len(vocab); i < benchGeomodelCount; i++ {
		vocab = append(vocab, fmt.Sprintf("Geomodelica species%d_Geo %d", i, i))
	}
	return classifier.RarityContext{
		Scores: []classifier.SpeciesScore{
			{Label: "Turdus merula_syn", Score: 0.42},
			{Label: "Spilopelia senegalensis_syn", Score: 0.1},
		},
		Geomodel:     classifier.NewLabelVocabulary(vocab),
		FilterActive: true,
		Settings:     goldenSettings(),
	}
}

// benchLegacyContext builds a legacy (no geomodel) context: coverage falls back to a
// full scan of the classifier labels through the snapshot memo, allocation-free.
func benchLegacyContext(classifierLabels []string) classifier.RarityContext {
	return classifier.RarityContext{
		Scores:           []classifier.SpeciesScore{{Label: "Turdus merula_syn", Score: 0.42}},
		Geomodel:         nil,
		ClassifierLabels: classifierLabels,
		FilterActive:     true,
		Settings:         goldenSettings(),
	}
}

// newSnapshotHandler builds a species Handler over a prebuilt snapshot and a fake
// backend, with no taxonomy database, so the measured cost is the name-resolution and
// rarity path the refactor touches (the taxonomy lookup is unchanged and orthogonal).
func newSnapshotHandler(tb testing.TB, labels []string, rc classifier.RarityContext) *Handler {
	tb.Helper()
	snap := speciesindex.Build(labels, nil, "en")
	h := &Handler{
		Core:                   &apicore.Core{},
		speciesSnapshot:        func() *speciesindex.Snapshot { return snap },
		speciesBackendOverride: &goldenBackend{rc: rc},
	}
	h.Settings.Store(goldenSettings())
	return h
}

func benchmarkGetSpeciesInfo(b *testing.B, rc classifier.RarityContext, query string) {
	b.Helper()
	labels := benchLabels(b)
	h := newSnapshotHandler(b, labels, rc)
	ctx := b.Context()
	b.ReportAllocs()
	for b.Loop() {
		_, _ = h.getSpeciesInfo(ctx, query)
	}
}

func BenchmarkGetSpeciesInfo(b *testing.B) {
	benchmarkGetSpeciesInfo(b, benchUniversalContext(), "Turdus merula")
}

func BenchmarkGetSpeciesInfo_Alias(b *testing.B) {
	benchmarkGetSpeciesInfo(b, benchUniversalContext(), "Spilopelia senegalensis")
}

func BenchmarkGetSpeciesInfo_Legacy(b *testing.B) {
	benchmarkGetSpeciesInfo(b, benchLegacyContext(benchLabels(b)), "Turdus merula")
}

func BenchmarkGetSpeciesInfo_NotFound(b *testing.B) {
	benchmarkGetSpeciesInfo(b, benchUniversalContext(), "Nonexistent species")
}

// TestGetSpeciesInfo_AllocationCeiling fails CI if the per-request allocation count
// regresses toward the pre-refactor storm. It measures getSpeciesInfo (the resolution
// and rarity path), not the HTTP JSON encoding, which is measured by the benchmark.
func TestGetSpeciesInfo_AllocationCeiling(t *testing.T) {
	h := newSnapshotHandler(t, benchLabels(t), benchUniversalContext())
	ctx := t.Context()

	// Confirm the request resolves before measuring, so a silent 404 does not make the
	// ceiling look artificially low.
	info, err := h.getSpeciesInfo(ctx, "Turdus merula")
	require.NoError(t, err)
	require.NotNil(t, info)

	avg := testing.AllocsPerRun(200, func() {
		_, _ = h.getSpeciesInfo(ctx, "Turdus merula")
	})
	assert.Less(t, avg, 100.0, "species info resolution must stay well under 100 allocs/op (was ~25-35k before the snapshot memo)")
}
