package detection

import (
	"context"
	"log/slog"
	"sync"

	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logging"
)

// handlerChainImpl implements HandlerChain
type handlerChainImpl struct {
	handlers []Handler
	mu       sync.RWMutex
	logger   *slog.Logger
}

// NewHandlerChain creates a new handler chain
func NewHandlerChain() HandlerChain {
	// Get logger from logging package
	logger := logging.ForService("audiocore").With("component", "detection_handler_chain")
	if logger == nil {
		// Fallback to default slog if logging not initialized
		logger = slog.Default().With("component", "detection_handler_chain")
	}

	return &handlerChainImpl{
		handlers: make([]Handler, 0),
		logger:   logger,
	}
}

// AddHandler adds a handler to the chain
func (c *handlerChainImpl) AddHandler(handler Handler) error {
	if handler == nil {
		return errors.Newf("handler is nil").
			Component("audiocore").
			Category(errors.CategoryValidation).
			Build()
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	// Check for duplicate ID
	for _, h := range c.handlers {
		if h.ID() == handler.ID() {
			return errors.Newf("handler with ID '%s' already exists", handler.ID()).
				Component("audiocore").
				Category(errors.CategoryState).
				Context("handler_id", handler.ID()).
				Build()
		}
	}

	c.handlers = append(c.handlers, handler)
	c.logger.Info("handler added to chain", "handler_id", handler.ID())
	return nil
}

// RemoveHandler removes a handler by ID
func (c *handlerChainImpl) RemoveHandler(id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	for i, h := range c.handlers {
		if h.ID() == id {
			// Close the handler before removing
			if err := h.Close(); err != nil {
				c.logger.Error("failed to close handler",
					"handler_id", id,
					"error", err)
			}

			// Remove from slice
			c.handlers = append(c.handlers[:i], c.handlers[i+1:]...)
			c.logger.Info("handler removed from chain", "handler_id", id)
			return nil
		}
	}

	return errors.Newf("handler not found: %s", id).
		Component("audiocore").
		Category(errors.CategoryNotFound).
		Context("handler_id", id).
		Build()
}

// HandleDetection sends detection to all handlers
func (c *handlerChainImpl) HandleDetection(ctx context.Context, detection *Detection) error {
	c.mu.RLock()
	handlers := make([]Handler, len(c.handlers))
	copy(handlers, c.handlers)
	c.mu.RUnlock()

	var errs []error
	for _, handler := range handlers {
		if err := handler.HandleDetection(ctx, detection); err != nil {
			c.logger.Error("handler failed to process detection",
				"handler_id", handler.ID(),
				"species", detection.Species,
				"confidence", detection.Confidence,
				"error", err)
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

// HandleAnalysisResult sends result to all handlers
func (c *handlerChainImpl) HandleAnalysisResult(ctx context.Context, result *AnalysisResult) error {
	c.mu.RLock()
	handlers := make([]Handler, len(c.handlers))
	copy(handlers, c.handlers)
	c.mu.RUnlock()

	c.logger.Debug("processing analysis result",
		"source_id", result.SourceID,
		"detections", len(result.Detections),
		"analyzer_id", result.AnalyzerID)

	var errs []error
	for _, handler := range handlers {
		if err := handler.HandleAnalysisResult(ctx, result); err != nil {
			c.logger.Error("handler failed to process analysis result",
				"handler_id", handler.ID(),
				"source_id", result.SourceID,
				"error", err)
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

// GetHandlers returns all handlers in order
func (c *handlerChainImpl) GetHandlers() []Handler {
	c.mu.RLock()
	defer c.mu.RUnlock()

	handlers := make([]Handler, len(c.handlers))
	copy(handlers, c.handlers)
	return handlers
}

// Close closes all handlers
func (c *handlerChainImpl) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	var errs []error
	for _, handler := range c.handlers {
		if err := handler.Close(); err != nil {
			c.logger.Error("failed to close handler",
				"handler_id", handler.ID(),
				"error", err)
			errs = append(errs, err)
		}
	}

	c.handlers = nil

	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}
