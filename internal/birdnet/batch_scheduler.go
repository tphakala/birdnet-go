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

// NewBatchScheduler creates a new BatchScheduler with the given batch size
func NewBatchScheduler(predictor BatchPredictor, batchSize int) *BatchScheduler {
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

// Stop gracefully shuts down the scheduler, processing any pending requests
func (s *BatchScheduler) Stop() {
	s.mu.Lock()
	s.stopped = true
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

		// Check if stopped
		if s.stopped {
			if len(s.pending) == 0 {
				return nil // Exit signal
			}
			// Process remaining items as final batch
			batch := s.pending
			s.pending = nil
			return batch
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

	// Collect samples
	samples := make([][]float32, len(batch))
	for i, req := range batch {
		samples[i] = req.Sample
	}

	// Run batch inference
	results, err := s.predictor.PredictBatch(samples)

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
