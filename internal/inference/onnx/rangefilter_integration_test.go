//go:build onnx

package onnx

import (
	"math"
	"math/rand/v2"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	defaultGeomodelPath = "testdata/geomodel.onnx"
	defaultLabelsPath   = "testdata/labels.txt"
	envGeomodelPath     = "BIRDNET_GEOMODEL_PATH"
	envLabelsPath       = "BIRDNET_GEOMODEL_LABELS_PATH"
)

func geomodelPath(t *testing.T) string {
	t.Helper()
	if p := os.Getenv(envGeomodelPath); p != "" {
		return p
	}
	return defaultGeomodelPath
}

func labelsPath(t *testing.T) string {
	t.Helper()
	if p := os.Getenv(envLabelsPath); p != "" {
		return p
	}
	return defaultLabelsPath
}

func skipIfNoModel(t *testing.T) {
	t.Helper()
	path := geomodelPath(t)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Skipf("geomodel not found at %s (set %s to override)", path, envGeomodelPath)
	}
	lpath := labelsPath(t)
	if _, err := os.Stat(lpath); os.IsNotExist(err) {
		t.Skipf("labels not found at %s (set %s to override)", lpath, envLabelsPath)
	}
}

func loadTestRangeFilter(t *testing.T) *RangeFilter {
	t.Helper()
	skipIfNoModel(t)

	MustInitORT("")
	t.Cleanup(func() { _ = DestroyORT() })

	rf, err := NewRangeFilter(geomodelPath(t),
		WithRangeFilterLabelsPath(labelsPath(t)),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = rf.Close() })
	return rf
}

func TestPredictBatchRaw_MatchesSinglePredict(t *testing.T) {
	rf := loadTestRangeFilter(t)

	const numPoints = 10
	type point struct {
		lat, lon, week float32
	}

	rng := rand.New(rand.NewPCG(42, 0))
	points := make([]point, numPoints)
	for i := range points {
		points[i] = point{
			lat:  rng.Float32()*180 - 90,
			lon:  rng.Float32()*360 - 180,
			week: float32(rng.IntN(48) + 1),
		}
	}

	singleResults := make([][]float32, numPoints)
	for i, p := range points {
		scores, err := rf.PredictRaw(p.lat, p.lon, p.week)
		require.NoError(t, err, "PredictRaw failed for point %d", i)
		singleResults[i] = scores
	}

	batchInput := make([]float32, 0, numPoints*3)
	for _, p := range points {
		batchInput = append(batchInput, p.lat, p.lon, p.week)
	}
	batchScores, err := rf.PredictBatchRaw(batchInput, numPoints)
	require.NoError(t, err)

	numSpecies := len(rf.labels)
	for i := range numPoints {
		start := i * numSpecies
		end := start + numSpecies
		batchSlice := batchScores[start:end]

		for j := range numSpecies {
			diff := float64(batchSlice[j] - singleResults[i][j])
			assert.LessOrEqual(t, math.Abs(diff), 1e-6,
				"point %d, species %d: batch=%.8f, single=%.8f",
				i, j, batchSlice[j], singleResults[i][j])
		}
	}
}

func TestPredictBatchRaw_SingleElement(t *testing.T) {
	rf := loadTestRangeFilter(t)

	lat, lon, week := float32(60.17), float32(24.94), float32(20)

	singleScores, err := rf.PredictRaw(lat, lon, week)
	require.NoError(t, err)

	batchScores, err := rf.PredictBatchRaw([]float32{lat, lon, week}, 1)
	require.NoError(t, err)

	require.Equal(t, len(singleScores), len(batchScores))
	for i := range singleScores {
		diff := float64(batchScores[i] - singleScores[i])
		assert.LessOrEqual(t, math.Abs(diff), 1e-6,
			"species %d: batch=%.8f, single=%.8f",
			i, batchScores[i], singleScores[i])
	}
}

func TestPredict_DelegationChain(t *testing.T) {
	rf := loadTestRangeFilter(t)

	lat, lon := float32(60.17), float32(24.94)
	month, day := 6, 15

	scores, err := rf.Predict(lat, lon, month, day)
	require.NoError(t, err)
	require.Equal(t, len(rf.labels), len(scores))

	week := CalculateWeek(month, day)
	rawScores, err := rf.PredictRaw(lat, lon, week)
	require.NoError(t, err)

	for i, ls := range scores {
		assert.Equal(t, rf.labels[i], ls.Species, "species mismatch at index %d", i)
		assert.Equal(t, i, ls.Index, "index mismatch at index %d", i)
		diff := float64(ls.Score - rawScores[i])
		assert.LessOrEqual(t, math.Abs(diff), 1e-6,
			"score mismatch at index %d: predict=%.8f, raw=%.8f",
			i, ls.Score, rawScores[i])
	}
}

func BenchmarkPredictBatchRaw(b *testing.B) {
	path := os.Getenv(envGeomodelPath)
	if path == "" {
		path = defaultGeomodelPath
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		b.Skipf("geomodel not found at %s", path)
	}
	lpath := os.Getenv(envLabelsPath)
	if lpath == "" {
		lpath = defaultLabelsPath
	}
	if _, err := os.Stat(lpath); os.IsNotExist(err) {
		b.Skipf("labels not found at %s", lpath)
	}

	MustInitORT("")
	b.Cleanup(func() { _ = DestroyORT() })

	rf, err := NewRangeFilter(path, WithRangeFilterLabelsPath(lpath))
	require.NoError(b, err)
	b.Cleanup(func() { _ = rf.Close() })

	const batchSize = 500
	rng := rand.New(rand.NewPCG(42, 0))

	type point struct {
		lat, lon, week float32
	}
	points := make([]point, batchSize)
	for i := range points {
		points[i] = point{
			lat:  rng.Float32()*180 - 90,
			lon:  rng.Float32()*360 - 180,
			week: float32(rng.IntN(48) + 1),
		}
	}

	batchInput := make([]float32, 0, batchSize*3)
	for _, p := range points {
		batchInput = append(batchInput, p.lat, p.lon, p.week)
	}

	b.Run("Sequential_500", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			for _, p := range points {
				_, err := rf.PredictRaw(p.lat, p.lon, p.week)
				if err != nil {
					b.Fatal(err)
				}
			}
		}
	})

	b.Run("Batch_500", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_, err := rf.PredictBatchRaw(batchInput, batchSize)
			if err != nil {
				b.Fatal(err)
			}
		}
	})
}
