package speciesindex

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/openfauna"
)

// v2.4 corpus embedded by the classifier; read at test time rather than importing
// classifier (which would pull the cgo inference backends into this leaf test
// binary) or duplicating ~250 KB under testdata. A missing file is a hard failure.
const corpusPath = "../classifier/data/labels/V2.4/BirdNET_GLOBAL_6K_V2.4_Labels_en_uk.txt"

// readLabelFile loads a newline-separated label file, trimming a trailing CR per
// line and dropping blank lines. A missing file fails the test.
func readLabelFile(tb testing.TB, path string) []string {
	tb.Helper()
	data, err := os.ReadFile(path) //nolint:gosec // fixed test paths, no user input
	require.NoErrorf(tb, err, "label file %s must be present", path)
	lines := strings.Split(string(data), "\n")
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		l = strings.TrimSuffix(l, "\r")
		if strings.TrimSpace(l) == "" {
			continue
		}
		out = append(out, l)
	}
	return out
}

// loadCorpus returns the 6522-label v2.4 en_uk corpus.
func loadCorpus(tb testing.TB) []string {
	tb.Helper()
	return readLabelFile(tb, corpusPath)
}

// loadTestdata returns a synthetic label fixture from testdata/.
func loadTestdata(tb testing.TB, name string) []string {
	tb.Helper()
	return readLabelFile(tb, filepath.Join("testdata", name))
}

// corpusScientificNames extracts the deduplicated scientific-name column of a
// label list (for seeding a real OpenFauna resolver working set).
func corpusScientificNames(labels []string) []string {
	seen := make(map[string]struct{}, len(labels))
	out := make([]string, 0, len(labels))
	for _, l := range labels {
		sci, _, _ := strings.Cut(l, "_")
		sci = strings.TrimSpace(sci)
		if sci == "" {
			continue
		}
		if _, dup := seen[sci]; dup {
			continue
		}
		seen[sci] = struct{}{}
		out = append(out, sci)
	}
	return out
}

// localResolver satisfies datastore.SpeciesNameResolver from an in-memory map,
// serving names only for scientific names resident in that map (ResolveLocal).
type localResolver struct{ m map[string]string }

func (r localResolver) Resolve(sci, _ string) string { return r.m[sci] }
func (r localResolver) ResolveLocal(sci string) (string, bool) {
	v, ok := r.m[sci]
	return v, ok
}

// batchAllResolver never resolves locally, so every scientific-only label falls
// to the cold-path batch localizer, which it satisfies for every requested name.
// This exercises the batchLocalizer seam in ResolveLabelNames.
type batchAllResolver struct{}

func (batchAllResolver) Resolve(string, string) string      { return "" }
func (batchAllResolver) ResolveLocal(string) (string, bool) { return "", false }
func (batchAllResolver) ResolveLocalizedBatch(names []string) map[string]string {
	out := make(map[string]string, len(names))
	for _, n := range names {
		out[n] = "batch " + n
	}
	return out
}

// firstAliasPair returns a (legacy, canonical) taxonomic-alias pair read from the
// openfauna dataset at test time, where CanonicalName(legacy) == canonical and
// canonical is its own canonical. Picked from the data rather than hard-coded so a
// dataset refresh cannot silently invalidate the memo/alias tests.
func firstAliasPair(tb testing.TB) (legacy, canonical string) {
	tb.Helper()
	data, err := os.ReadFile("../openfauna/data/aliases.json") //nolint:gosec // fixed test path
	require.NoError(tb, err)
	var m map[string]string
	require.NoError(tb, json.Unmarshal(data, &m))
	require.NotEmpty(tb, m, "alias dataset must not be empty")

	keys := slices.Collect(maps.Keys(m))
	slices.Sort(keys) // deterministic pick
	for _, k := range keys {
		c := m[k]
		if k == c {
			continue
		}
		if openfauna.CanonicalName(k) == c && openfauna.CanonicalName(c) == c {
			return k, c
		}
	}
	tb.Fatalf("no usable alias pair found in openfauna dataset")
	return "", ""
}
