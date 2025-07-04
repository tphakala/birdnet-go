package audiocore

import (
	"context"
	"sync"

	"github.com/tphakala/birdnet-go/internal/errors"
)

// processorChainImpl implements the ProcessorChain interface
type processorChainImpl struct {
	processors []AudioProcessor
	mu         sync.RWMutex
}

// NewProcessorChain creates a new processor chain
func NewProcessorChain() ProcessorChain {
	return &processorChainImpl{
		processors: make([]AudioProcessor, 0),
	}
}

// AddProcessor adds a processor to the chain
func (pc *processorChainImpl) AddProcessor(processor AudioProcessor) error {
	if processor == nil {
		return errors.New(nil).
			Component(ComponentAudioCore).
			Category(errors.CategoryValidation).
			Context("error", "processor cannot be nil").
			Build()
	}

	pc.mu.Lock()
	defer pc.mu.Unlock()

	// Check if processor with same ID already exists
	for _, p := range pc.processors {
		if p.ID() == processor.ID() {
			return errors.New(nil).
				Component(ComponentAudioCore).
				Category(errors.CategoryConflict).
				Context("processor_id", processor.ID()).
				Context("error", "processor already exists in chain").
				Build()
		}
	}

	pc.processors = append(pc.processors, processor)
	return nil
}

// RemoveProcessor removes a processor from the chain
func (pc *processorChainImpl) RemoveProcessor(id string) error {
	pc.mu.Lock()
	defer pc.mu.Unlock()

	for i, p := range pc.processors {
		if p.ID() == id {
			// Remove processor while maintaining order
			pc.processors = append(pc.processors[:i], pc.processors[i+1:]...)
			return nil
		}
	}

	return errors.New(ErrProcessorNotFound).
		Component(ComponentAudioCore).
		Context("processor_id", id).
		Build()
}

// Process runs audio through the entire chain
func (pc *processorChainImpl) Process(ctx context.Context, input *AudioData) (*AudioData, error) {
	pc.mu.RLock()
	defer pc.mu.RUnlock()

	// If no processors, return input unchanged
	if len(pc.processors) == 0 {
		return input, nil
	}

	// Process through each processor in sequence
	current := input
	for _, processor := range pc.processors {
		// Check context cancellation
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		// Process audio
		processed, err := processor.Process(ctx, current)
		if err != nil {
			return nil, errors.New(err).
				Component(ComponentAudioCore).
				Category(errors.CategoryProcessing).
				Context("processor_id", processor.ID()).
				Context("operation", "process_audio").
				Build()
		}

		current = processed
	}

	return current, nil
}

// GetProcessors returns all processors in order
func (pc *processorChainImpl) GetProcessors() []AudioProcessor {
	pc.mu.RLock()
	defer pc.mu.RUnlock()

	// Return a copy to prevent external modification
	processors := make([]AudioProcessor, len(pc.processors))
	copy(processors, pc.processors)
	return processors
}

// ErrProcessorNotFound is returned when a processor is not found in the chain
var ErrProcessorNotFound = errors.New(nil).
	Component(ComponentAudioCore).
	Category(errors.CategoryNotFound).
	Context("resource", "processor").
	Build()