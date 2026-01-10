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
	settings.BirdNET.BatchSize = 2 // Enable batching

	bn, err := NewBirdNET(settings)
	if err != nil {
		t.Skipf("Skipping test, BirdNET initialization failed: %v", err)
	}
	defer bn.Delete()

	t.Run("batched submission", func(t *testing.T) {
		resultChan := make(chan BatchResponse, 1)

		err := bn.SubmitBatch(BatchRequest{
			Sample:     make([]float32, SampleSize),
			SourceID:   "test",
			ResultChan: resultChan,
		})
		require.NoError(t, err)

		// Submit second request to trigger batch
		resultChan2 := make(chan BatchResponse, 1)
		err = bn.SubmitBatch(BatchRequest{
			Sample:     make([]float32, SampleSize),
			SourceID:   "test",
			ResultChan: resultChan2,
		})
		require.NoError(t, err)

		// Both should receive results
		select {
		case resp := <-resultChan:
			assert.False(t, resp.HasError())
			assert.NotEmpty(t, resp.Results)
		case <-time.After(10 * time.Second):
			t.Fatal("timeout waiting for first result")
		}

		select {
		case resp := <-resultChan2:
			assert.False(t, resp.HasError())
			assert.NotEmpty(t, resp.Results)
		case <-time.After(10 * time.Second):
			t.Fatal("timeout waiting for second result")
		}
	})
}

func TestBirdNET_SubmitBatch_Disabled(t *testing.T) {
	settings := conf.NewTestSettings().Build()
	settings.BirdNET.BatchSize = 1 // Batching disabled

	bn, err := NewBirdNET(settings)
	if err != nil {
		t.Skipf("Skipping test, BirdNET initialization failed: %v", err)
	}
	defer bn.Delete()

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
