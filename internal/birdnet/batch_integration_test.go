package birdnet

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
)

func TestBirdNET_SubmitBatch(t *testing.T) {
	settings := conf.NewTestSettings().Build()
	settings.BirdNET.Overlap = 2.0 // Enable batching (batch size = 4)

	bn, err := NewBirdNET(settings)
	if err != nil {
		t.Skipf("Skipping test, BirdNET initialization failed: %v", err)
	}
	defer bn.Delete()

	require.True(t, bn.IsBatchingEnabled(), "batching should be enabled with overlap >= 2.0")
	require.Equal(t, 4, bn.GetBatchSize(), "batch size should be 4 for overlap 2.0")

	t.Run("batched submission", func(t *testing.T) {
		// Submit 4 requests to fill a batch
		resultChans := make([]chan BatchResponse, 4)
		for i := range resultChans {
			resultChans[i] = make(chan BatchResponse, 1)
			err := bn.SubmitBatch(BatchRequest{
				Sample:     make([]float32, SampleSize),
				SourceID:   "test",
				ResultChan: resultChans[i],
			})
			require.NoError(t, err)
		}

		// All should receive results
		for i, ch := range resultChans {
			select {
			case resp := <-ch:
				assert.False(t, resp.HasError(), "request %d should not have error", i)
				assert.NotEmpty(t, resp.Results, "request %d should have results", i)
			case <-time.After(10 * time.Second):
				t.Fatalf("timeout waiting for result %d", i)
			}
		}
	})
}

func TestBirdNET_SubmitBatch_Disabled(t *testing.T) {
	settings := conf.NewTestSettings().Build()
	settings.BirdNET.Overlap = 1.0 // Batching disabled (overlap < 2.0)

	bn, err := NewBirdNET(settings)
	if err != nil {
		t.Skipf("Skipping test, BirdNET initialization failed: %v", err)
	}
	defer bn.Delete()

	require.False(t, bn.IsBatchingEnabled(), "batching should be disabled with overlap < 2.0")
	require.Equal(t, 1, bn.GetBatchSize(), "batch size should be 1 when disabled")

	t.Run("falls back to single prediction", func(t *testing.T) {
		resultChan := make(chan BatchResponse, 1)

		err := bn.SubmitBatch(BatchRequest{
			Sample:     make([]float32, SampleSize),
			SourceID:   "test",
			ResultChan: resultChan,
		})
		require.NoError(t, err)

		select {
		case resp := <-resultChan:
			assert.False(t, resp.HasError())
			assert.NotEmpty(t, resp.Results)
		case <-time.After(10 * time.Second):
			t.Fatal("timeout waiting for result")
		}
	})
}

func TestBirdNET_UpdateBatchSize(t *testing.T) {
	settings := conf.NewTestSettings().Build()
	settings.BirdNET.Overlap = 1.0 // Start with batching disabled

	bn, err := NewBirdNET(settings)
	if err != nil {
		t.Skipf("Skipping test, BirdNET initialization failed: %v", err)
	}
	defer bn.Delete()

	// Initial state: batching disabled
	require.False(t, bn.IsBatchingEnabled())
	require.Equal(t, 1, bn.GetBatchSize())

	t.Run("enable batching at runtime", func(t *testing.T) {
		bn.UpdateBatchSize(2.0) // Should enable batching with size 4
		assert.True(t, bn.IsBatchingEnabled())
		assert.Equal(t, 4, bn.GetBatchSize())
	})

	t.Run("increase batch size", func(t *testing.T) {
		bn.UpdateBatchSize(2.5) // Should increase to size 8
		assert.True(t, bn.IsBatchingEnabled())
		assert.Equal(t, 8, bn.GetBatchSize())
	})

	t.Run("decrease batch size", func(t *testing.T) {
		bn.UpdateBatchSize(2.2) // Should decrease to size 4
		assert.True(t, bn.IsBatchingEnabled())
		assert.Equal(t, 4, bn.GetBatchSize())
	})

	t.Run("disable batching at runtime", func(t *testing.T) {
		bn.UpdateBatchSize(1.5) // Should disable batching
		assert.False(t, bn.IsBatchingEnabled())
		assert.Equal(t, 1, bn.GetBatchSize())
	})

	t.Run("no change when same tier", func(t *testing.T) {
		bn.UpdateBatchSize(2.0) // Enable
		assert.Equal(t, 4, bn.GetBatchSize())

		bn.UpdateBatchSize(2.3) // Same tier, no change
		assert.Equal(t, 4, bn.GetBatchSize())
	})
}
