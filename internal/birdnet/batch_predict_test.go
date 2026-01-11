package birdnet

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
)

func TestBirdNET_PredictBatch(t *testing.T) {
	// Skip if no model available (CI environment)
	settings := conf.NewTestSettings().Build()
	bn, err := NewBirdNET(settings)
	if err != nil {
		t.Skipf("Skipping test, BirdNET initialization failed: %v", err)
	}
	defer bn.Delete()

	t.Run("batch of 1", func(t *testing.T) {
		samples := [][]float32{make([]float32, SampleSize)}
		results, err := bn.PredictBatch(samples)

		require.NoError(t, err)
		require.Len(t, results, 1)
		assert.NotEmpty(t, results[0], "should have predictions for first sample")
	})

	t.Run("batch of 4", func(t *testing.T) {
		samples := make([][]float32, 4)
		for i := range samples {
			samples[i] = make([]float32, SampleSize)
		}

		results, err := bn.PredictBatch(samples)

		require.NoError(t, err)
		require.Len(t, results, 4, "should return results for all 4 samples")
		for i, r := range results {
			assert.NotEmpty(t, r, "sample %d should have predictions", i)
		}
	})

	t.Run("empty batch returns error", func(t *testing.T) {
		samples := [][]float32{}
		_, err := bn.PredictBatch(samples)

		assert.Error(t, err)
	})

	t.Run("nil batch returns error", func(t *testing.T) {
		_, err := bn.PredictBatch(nil)

		assert.Error(t, err)
	})

	t.Run("mismatched sample sizes returns error", func(t *testing.T) {
		samples := [][]float32{
			make([]float32, SampleSize),
			make([]float32, 1000), // Wrong size
		}
		_, err := bn.PredictBatch(samples)

		assert.Error(t, err)
	})
}
