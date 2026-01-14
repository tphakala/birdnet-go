package birdnet

import (
	"fmt"
	"sync"

	"github.com/tphakala/birdnet-go/internal/datastore"
)

// BatchPredictor is the interface for batch prediction
type BatchPredictor interface {
	PredictBatch(samples [][]float32) ([][]datastore.Results, error)
}

// BatchScheduler collects audio chunks and triggers batch inference when full
type BatchScheduler struct {
	predictor BatchPredictor
	batchSize int
	pending   []BatchRequest
	mu        sync.Mutex
	cond      *sync.Cond
	stopped   bool
	wg        sync.WaitGroup
}

// NewBatchScheduler creates a new BatchScheduler with the given batch size.
// Panics if predictor is nil.
func NewBatchScheduler(predictor BatchPredictor, batchSize int) *BatchScheduler {
	if predictor == nil {
		panic("birdnet: NewBatchScheduler called with nil predictor")
	}
	if batchSize < 1 {
		batchSize = 1
	}

	s := &BatchScheduler{
		predictor: predictor,
		batchSize: batchSize,
		pending:   make([]BatchRequest, 0, batchSize),
	}
	s.cond = sync.NewCond(&s.mu)

	s.wg.Add(1)
	go s.processLoop()

	return s
}

// Submit adds a chunk to the batch queue
func (s *BatchScheduler) Submit(req BatchRequest) error {
	if err := req.Validate(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.stopped {
		return fmt.Errorf("scheduler is stopped")
	}

	s.pending = append(s.pending, req)

	// Signal if batch is full
	if len(s.pending) >= s.batchSize {
		s.cond.Signal()
	}

	return nil
}

// Stop gracefully shuts down the scheduler, notifying pending requesters.
// In realtime audio processing, incomplete batches on shutdown are stale data.
func (s *BatchScheduler) Stop() {
	s.mu.Lock()
	s.stopped = true
	// Notify pending requesters that scheduler is stopping
	for _, req := range s.pending {
		req.ResultChan <- BatchResponse{Err: fmt.Errorf("scheduler stopped")}
	}
	s.pending = nil
	s.cond.Signal()
	s.mu.Unlock()

	s.wg.Wait()
}

// processLoop waits for batches to fill and processes them
func (s *BatchScheduler) processLoop() {
	defer s.wg.Done()

	for {
		batch := s.waitForBatch()
		if batch == nil {
			return // Scheduler stopped with no pending work
		}
		s.processBatch(batch)
	}
}

// waitForBatch waits until a full batch is available or scheduler is stopped.
// Returns nil if scheduler is stopped with no pending work.
func (s *BatchScheduler) waitForBatch() []BatchRequest {
	s.mu.Lock()
	defer s.mu.Unlock()

	for {
		// Check if we have a full batch ready
		if len(s.pending) >= s.batchSize {
			batch := make([]BatchRequest, s.batchSize)
			copy(batch, s.pending[:s.batchSize])
			s.pending = s.pending[s.batchSize:]
			return batch
		}

		// Check if stopped - exit without processing remaining
		if s.stopped {
			return nil
		}

		// Wait for more items or stop signal
		s.cond.Wait()
	}
}

// processBatch runs inference on a batch and sends results
func (s *BatchScheduler) processBatch(batch []BatchRequest) {
	if len(batch) == 0 {
		return
	}

	// Recover from panics to prevent callers from hanging forever on ResultChan
	defer func() {
		if r := recover(); r != nil {
			err := fmt.Errorf("panic during batch inference: %v", r)
			for _, req := range batch {
				req.ResultChan <- BatchResponse{Err: err}
			}
		}
	}()

	// Collect samples
	samples := make([][]float32, len(batch))
	for i, req := range batch {
		samples[i] = req.Sample
	}

	// Run batch inference
	results, err := s.predictor.PredictBatch(samples)

	// Validate result count matches batch size to prevent index out of bounds
	if err == nil && len(results) != len(batch) {
		err = fmt.Errorf("batch inference returned %d results for %d samples", len(results), len(batch))
	}

	// Send results back to each requester
	for i, req := range batch {
		var resp BatchResponse
		if err != nil {
			resp = BatchResponse{Err: err}
		} else {
			resp = BatchResponse{Results: results[i]}
		}
		req.ResultChan <- resp
	}
}
