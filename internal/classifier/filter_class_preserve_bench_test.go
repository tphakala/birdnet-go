package classifier

import (
	"bufio"
	"math/rand/v2"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/datastore"
)

// perchLabelCount is the size of the Perch v2 label set, the largest full
// prediction set getTopKResults scans per inference window.
const perchLabelCount = 14795

// benchTopK is the K every model passes to getTopKResults.
const benchTopK = defaultTopKResults

// readBenchLabels loads a committed BirdNET v2.4 label file for benchmarks.
func readBenchLabels(b *testing.B, locale string) []string {
	b.Helper()
	f, err := os.Open(filepath.Join("data", "labels", "V2.4", "BirdNET_GLOBAL_6K_V2.4_Labels_"+locale+".txt"))
	require.NoError(b, err)
	defer func() { _ = f.Close() }()

	var labels []string
	s := bufio.NewScanner(f)
	for s.Scan() {
		labels = append(labels, s.Text())
	}
	require.NoError(b, s.Err())
	return labels
}

// benchResults pairs labels with deterministic pseudo-random confidences.
func benchResults(labels []string) []datastore.Results {
	r := rand.New(rand.NewPCG(1, 2)) //nolint:gosec // deterministic benchmark data, not security sensitive
	out := make([]datastore.Results, len(labels))
	for i := range out {
		out[i] = datastore.Results{Species: labels[i], Confidence: r.Float32()}
	}
	return out
}

// BenchmarkGetTopKResultsRealLabels measures the per-inference top-K step,
// including the human/dog filter-class scan over the full prediction set, on
// real BirdNET label files. "perch_scale" repeats the English and German
// BirdNET labels up to the Perch v2 label count, since the Perch label file is
// downloaded at runtime and not committed.
func BenchmarkGetTopKResultsRealLabels(b *testing.B) {
	enUK := readBenchLabels(b, "en_uk")
	de := readBenchLabels(b, "de")
	perchScale := make([]string, 0, perchLabelCount)
	for len(perchScale) < perchLabelCount {
		perchScale = append(perchScale, enUK...)
		perchScale = append(perchScale, de...)
	}
	perchScale = perchScale[:perchLabelCount]

	sets := []struct {
		name   string
		labels []string
	}{
		{"birdnet_en_uk", enUK},
		{"birdnet_de", de},
		{"birdnet_ja", readBenchLabels(b, "ja")},
		{"perch_scale", perchScale},
	}

	for _, set := range sets {
		base := benchResults(set.labels)
		work := make([]datastore.Results, len(base))
		b.Run(set.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				copy(work, base)
				_ = getTopKResults(work, benchTopK)
			}
		})
	}
}
