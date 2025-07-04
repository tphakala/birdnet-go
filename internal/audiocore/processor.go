package audiocore

import (
	"context"
	"log/slog"
	"sync"

	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logging"
)

// processorChainImpl implements the ProcessorChain interface
type processorChainImpl struct {
	processors []AudioProcessor
	mu         sync.RWMutex
	logger     *slog.Logger
}

// NewProcessorChain creates a new processor chain
func NewProcessorChain() ProcessorChain {
	logger := logging.ForService("audiocore")
	if logger == nil {
		logger = slog.Default()
	}
	logger = logger.With("component", "processor_chain")

	return &processorChainImpl{
		processors: make([]AudioProcessor, 0),
		logger:     logger,
	}
}

// AddProcessor adds a processor to the chain
func (pc *processorChainImpl) AddProcessor(processor AudioProcessor) error {
	if processor == nil {
		return errors.Newf("processor cannot be nil").
			Component(ComponentAudioCore).
			Category(errors.CategoryValidation).
			Build()
	}

	pc.mu.Lock()
	defer pc.mu.Unlock()

	// Check if processor with same ID already exists
	for _, p := range pc.processors {
		if p.ID() == processor.ID() {
			pc.logger.Warn("processor already exists in chain",
				"processor_id", processor.ID())
			return errors.Newf("processor already exists in chain").
				Component(ComponentAudioCore).
				Category(errors.CategoryConflict).
				Context("processor_id", processor.ID()).
				Build()
		}
	}

	pc.processors = append(pc.processors, processor)
	pc.logger.Info("processor added to chain",
		"processor_id", processor.ID(),
		"chain_length", len(pc.processors))
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
			pc.logger.Info("processor removed from chain",
				"processor_id", id,
				"remaining_processors", len(pc.processors))
			return nil
		}
	}

	pc.logger.Warn("processor not found for removal",
		"processor_id", id)
	return ErrProcessorNotFound
}

// Process runs audio through the entire chain
func (pc *processorChainImpl) Process(ctx context.Context, input *AudioData) (*AudioData, error) {
	pc.mu.RLock()
	defer pc.mu.RUnlock()

	// If no processors, return input unchanged
	if len(pc.processors) == 0 {
		pc.logger.Debug("no processors in chain, returning input unchanged")
		return input, nil
	}

	// Process through each processor in sequence
	current := input

	if pc.logger.Enabled(context.TODO(), slog.LevelDebug) {
		pc.logger.Debug("starting processor chain execution",
			"processor_count", len(pc.processors),
			"source_id", input.SourceID)
	}

	for _, processor := range pc.processors {
		// Check context cancellation
		select {
		case <-ctx.Done():
			pc.logger.Debug("processor chain cancelled",
				"processor_id", processor.ID())
			return nil, ctx.Err()
		default:
		}

		// Process audio
		processed, err := processor.Process(ctx, current)
		if err != nil {
			pc.logger.Error("processor failed",
				"processor_id", processor.ID(),
				"error", err)
			return nil, errors.New(err).
				Component(ComponentAudioCore).
				Category(errors.CategoryProcessing).
				Context("processor_id", processor.ID()).
				Context("operation", "process_audio").
				Build()
		}

		if pc.logger.Enabled(context.TODO(), slog.LevelDebug) {
			pc.logger.Debug("processor executed successfully",
				"processor_id", processor.ID())
		}

		current = processed
	}

	if pc.logger.Enabled(context.TODO(), slog.LevelDebug) {
		pc.logger.Debug("processor chain completed successfully",
			"processor_count", len(pc.processors))
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
var ErrProcessorNotFound = errors.Newf("processor not found").
	Component(ComponentAudioCore).
	Category(errors.CategoryNotFound).
	Context("resource", "processor").
	Build()
