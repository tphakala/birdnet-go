package birdnet

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/datastore"
)

// mockBatchPredictor implements the BatchPredictor interface for testing
type mockBatchPredictor struct {
	predictBatchCalls int
	mu                sync.Mutex
}

func (m *mockBatchPredictor) PredictBatch(samples [][]float32) ([][]datastore.Results, error) {
	m.mu.Lock()
	m.predictBatchCalls++
	m.mu.Unlock()

	// Return mock results
	results := make([][]datastore.Results, len(samples))
	for i := range results {
		results[i] = []datastore.Results{{Species: "MockBird", Confidence: 0.9}}
	}
	return results, nil
}

func TestBatchScheduler_Submit(t *testing.T) {
	t.Run("batch triggers at size", func(t *testing.T) {
		predictor := &mockBatchPredictor{}
		scheduler := NewBatchScheduler(predictor, 4)
		t.Cleanup(func() { scheduler.Stop() })

		// Create channels for all 4 requests
		resultChans := make([]chan BatchResponse, 4)
		for i := range resultChans {
			resultChans[i] = make(chan BatchResponse, 1)
		}

		// Submit 4 requests
		for i := range 4 {
			err := scheduler.Submit(BatchRequest{
				Sample:     make([]float32, SampleSize),
				SourceID:   "test",
				ResultChan: resultChans[i],
			})
			require.NoError(t, err)
		}

		// Collect results - all assertions in main goroutine
		results := make([]BatchResponse, 4)
		for i := range 4 {
			select {
			case resp := <-resultChans[i]:
				results[i] = resp
			case <-time.After(5 * time.Second):
				t.Fatalf("timeout waiting for result %d", i)
			}
		}

		// Verify all results received
		for i, resp := range results {
			assert.False(t, resp.HasError(), "result %d should not have error", i)
			assert.NotEmpty(t, resp.Results, "result %d should have predictions", i)
		}

		// Verify batch was called once
		predictor.mu.Lock()
		calls := predictor.predictBatchCalls
		predictor.mu.Unlock()
		assert.Equal(t, 1, calls)
	})

	t.Run("multiple batches", func(t *testing.T) {
		predictor := &mockBatchPredictor{}
		scheduler := NewBatchScheduler(predictor, 2)
		t.Cleanup(func() { scheduler.Stop() })

		// Create channels for all 4 requests
		resultChans := make([]chan BatchResponse, 4)
		for i := range resultChans {
			resultChans[i] = make(chan BatchResponse, 1)
		}

		// Submit 4 requests (should trigger 2 batches)
		for i := range 4 {
			err := scheduler.Submit(BatchRequest{
				Sample:     make([]float32, SampleSize),
				SourceID:   "test",
				ResultChan: resultChans[i],
			})
			require.NoError(t, err)
		}

		// Collect all results
		for i := range 4 {
			select {
			case <-resultChans[i]:
				// Result received
			case <-time.After(5 * time.Second):
				t.Fatalf("timeout waiting for result %d", i)
			}
		}

		predictor.mu.Lock()
		calls := predictor.predictBatchCalls
		predictor.mu.Unlock()
		assert.Equal(t, 2, calls)
	})
}

func TestNewBatchScheduler_NilPredictor(t *testing.T) {
	assert.Panics(t, func() {
		NewBatchScheduler(nil, 4)
	}, "should panic when predictor is nil")
}

func TestBatchScheduler_Stop(t *testing.T) {
	predictor := &mockBatchPredictor{}
	scheduler := NewBatchScheduler(predictor, 4)

	// Submit 2 requests (less than batch size)
	resultChans := make([]chan BatchResponse, 2)
	for i := range 2 {
		resultChans[i] = make(chan BatchResponse, 1)
		err := scheduler.Submit(BatchRequest{
			Sample:     make([]float32, SampleSize),
			SourceID:   "test",
			ResultChan: resultChans[i],
		})
		require.NoError(t, err)
	}

	// Stop should notify pending requesters with error (realtime audio - stale data)
	scheduler.Stop()

	// Verify all pending requesters received error notification
	for i, ch := range resultChans {
		select {
		case resp := <-ch:
			require.Error(t, resp.Err, "request %d should receive error on stop", i)
			assert.Contains(t, resp.Err.Error(), "scheduler stopped")
		default:
			t.Fatalf("request %d should have received error notification", i)
		}
	}

	// Verify predictor was never called for partial batch
	predictor.mu.Lock()
	calls := predictor.predictBatchCalls
	predictor.mu.Unlock()
	assert.Equal(t, 0, calls, "partial batch should not be processed")
}

// mockMismatchPredictor returns fewer results than samples to test error handling
type mockMismatchPredictor struct{}

func (m *mockMismatchPredictor) PredictBatch(samples [][]float32) ([][]datastore.Results, error) {
	// Return only 1 result regardless of input size
	return [][]datastore.Results{
		{{Species: "MockBird", Confidence: 0.9}},
	}, nil
}

func TestBatchScheduler_ResultCountMismatch(t *testing.T) {
	predictor := &mockMismatchPredictor{}
	scheduler := NewBatchScheduler(predictor, 2)
	t.Cleanup(func() { scheduler.Stop() })

	// Submit 2 requests to trigger a batch
	resultChans := make([]chan BatchResponse, 2)
	for i := range 2 {
		resultChans[i] = make(chan BatchResponse, 1)
		err := scheduler.Submit(BatchRequest{
			Sample:     make([]float32, SampleSize),
			SourceID:   "test",
			ResultChan: resultChans[i],
		})
		require.NoError(t, err)
	}

	// All requesters should receive error due to result count mismatch
	for i, ch := range resultChans {
		select {
		case resp := <-ch:
			require.Error(t, resp.Err, "request %d should have error due to mismatch", i)
			assert.Contains(t, resp.Err.Error(), "returned 1 results for 2 samples")
		case <-time.After(5 * time.Second):
			t.Fatalf("timeout waiting for result %d", i)
		}
	}
}
