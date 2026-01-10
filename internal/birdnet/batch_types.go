package birdnet

import (
	"fmt"

	"github.com/tphakala/birdnet-go/internal/datastore"
)

// SampleSize is the expected number of float32 samples per audio chunk (3 seconds at 48kHz)
const SampleSize = 144000

// DefaultTopKResults is the number of top prediction results to return from inference
const DefaultTopKResults = 10

// BatchRequest represents a single audio chunk submitted for batch inference
type BatchRequest struct {
	Sample     []float32            // Audio samples (must be SampleSize length)
	SourceID   string               // Identifier for the audio source
	ResultChan chan<- BatchResponse // Channel to receive results
}

// Validate checks that the BatchRequest has valid data
func (r *BatchRequest) Validate() error {
	if r.Sample == nil {
		return fmt.Errorf("sample cannot be nil")
	}
	if len(r.Sample) != SampleSize {
		return fmt.Errorf("sample size must be %d, got %d", SampleSize, len(r.Sample))
	}
	if r.ResultChan == nil {
		return fmt.Errorf("result channel cannot be nil")
	}
	return nil
}

// BatchResponse contains the inference results for a single chunk
type BatchResponse struct {
	Results []datastore.Results // Top-K prediction results
	Err     error               // Error if inference failed
}

// HasError returns true if the response contains an error
func (r *BatchResponse) HasError() bool {
	return r.Err != nil
}
