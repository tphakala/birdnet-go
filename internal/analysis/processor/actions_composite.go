// processor/actions_composite.go
// This file contains CompositeAction implementation and helper functions.

package processor

import (
	"context"
	"fmt"
	"time"

	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/imageprovider"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// actionResult is a simple struct to pass action execution results through channels.
type actionResult struct {
	err error
}

// sendPanicError creates and sends a panic error result through the channel.
// This consolidates duplicate panic handling logic from executeActionWithRecovery.
func sendPanicError(resultChan chan<- actionResult, ctx context.Context, r any, action Action, step, total int) {
	panicErr := errors.Newf("action panicked: %v", r).
		Component("analysis.processor").
		Category(errors.CategoryProcessing).
		Context("action_type", fmt.Sprintf("%T", action)).
		Context("action_description", action.GetDescription()).
		Context("panic_value", fmt.Sprintf("%v", r)).
		Context("step", step).
		Context("total_steps", total).
		Build()
	select {
	case resultChan <- actionResult{err: panicErr}:
	case <-ctx.Done():
		// Context cancelled, exit gracefully
	}
}

// executeContextAction runs a ContextAction with panic recovery.
// Returns error via the provided channel.
func executeContextAction(ctx context.Context, contextAction ContextAction, data any, resultChan chan<- actionResult, step, total int) {
	defer func() {
		if r := recover(); r != nil {
			sendPanicError(resultChan, ctx, r, contextAction, step, total)
		}
	}()

	err := contextAction.ExecuteContext(ctx, data)
	select {
	case resultChan <- actionResult{err: err}:
	case <-ctx.Done():
	}
}

// executeLegacyAction runs a non-context Action with panic recovery.
// Returns error via the provided channel.
//
// IMPORTANT: Legacy Goroutine Leak Limitation
// If the context times out before the action completes, this function returns
// but the spawned goroutine continues running until action.Execute() returns.
// This is a known limitation for actions that don't implement ContextAction.
//
// Mitigation: Actions should implement ContextAction interface for proper
// cancellation support. Legacy actions without context awareness may leak
// goroutines on timeout, though they will eventually complete.
//
// The ContextAction interface was introduced to address this limitation.
// New actions should always implement ContextAction.
func executeLegacyAction(ctx context.Context, action Action, data any, resultChan chan<- actionResult, step, total int) {
	defer func() {
		if r := recover(); r != nil {
			sendPanicError(resultChan, ctx, r, action, step, total)
		}
	}()

	done := make(chan struct{})
	var execErr error

	go func() {
		defer close(done)
		defer func() {
			if r := recover(); r != nil {
				execErr = errors.Newf("action panicked: %v", r).
					Component("analysis.processor").
					Category(errors.CategoryProcessing).
					Context("action_type", fmt.Sprintf("%T", action)).
					Context("action_description", action.GetDescription()).
					Context("panic_value", fmt.Sprintf("%v", r)).
					Context("step", step).
					Context("total_steps", total).
					Build()
			}
		}()
		execErr = action.Execute(ctx, data)
	}()

	select {
	case <-done:
		select {
		case resultChan <- actionResult{err: execErr}:
		case <-ctx.Done():
		}
	case <-ctx.Done():
		select {
		case resultChan <- actionResult{err: ctx.Err()}:
		default:
		}
	}
}

// buildTimeoutError creates a structured timeout error for composite action execution.
func buildTimeoutError(action Action, timeout time.Duration, step, total int) error {
	return errors.Newf("action timed out after %v", timeout).
		Component("analysis.processor").
		Category(errors.CategoryTimeout).
		Context("action_type", fmt.Sprintf("%T", action)).
		Context("action_description", action.GetDescription()).
		Context("timeout_seconds", timeout.Seconds()).
		Context("step", step).
		Context("total_steps", total).
		Build()
}

// getBirdImageFromCache retrieves a bird image from cache with proper error handling and logging.
// This helper consolidates duplicate image retrieval logic used by MqttAction and SSEAction.
// Returns an empty BirdImage if the cache is nil or if retrieval fails.
func getBirdImageFromCache(cache *imageprovider.BirdImageCache, scientificName, commonName, correlationID string) imageprovider.BirdImage {
	if cache == nil {
		GetLogger().Warn("BirdImageCache is nil, cannot fetch image",
			logger.String("detection_id", correlationID),
			logger.String("species", commonName),
			logger.String("scientific_name", scientificName),
			logger.String("operation", "check_bird_image_cache"))
		return imageprovider.BirdImage{}
	}

	birdImage, err := cache.Get(scientificName)
	if err != nil {
		GetLogger().Warn("Error getting bird image from cache",
			logger.String("detection_id", correlationID),
			logger.Error(err),
			logger.String("species", commonName),
			logger.String("scientific_name", scientificName),
			logger.String("operation", "get_bird_image"))
		return imageprovider.BirdImage{}
	}

	return birdImage
}

// Execute runs all actions sequentially, stopping on first error
// This method is designed to prevent deadlocks and handle timeouts properly
func (a *CompositeAction) Execute(ctx context.Context, data any) error {
	// Handle nil or empty actions gracefully
	if a == nil || a.Actions == nil || len(a.Actions) == 0 {
		return nil // Nothing to execute
	}

	// Only lock while accessing the Actions slice, not during execution
	a.mu.Lock()
	actions := make([]Action, len(a.Actions))
	copy(actions, a.Actions)
	a.mu.Unlock()

	// Count non-nil actions for accurate progress reporting
	nonNilCount := 0
	for _, action := range actions {
		if action != nil {
			nonNilCount++
		}
	}

	if nonNilCount == 0 {
		return nil // All actions are nil
	}

	// Execute each action in order without holding the mutex
	currentStep := 0
	for _, action := range actions {
		if action == nil {
			continue
		}
		currentStep++

		// Add panic recovery for each action to prevent crashes
		err := a.executeActionWithRecovery(ctx, action, data, currentStep, nonNilCount)
		if err != nil {
			return err
		}
	}

	return nil
}

// executeActionWithRecovery executes a single action with panic recovery and proper context handling.
// This method orchestrates action execution through helper functions to reduce complexity.
// The parentCtx is used as the base for the per-action timeout context to preserve cancellation chain.
func (a *CompositeAction) executeActionWithRecovery(parentCtx context.Context, action Action, data any, step, total int) error {
	timeout := CompositeActionTimeout
	if a.Timeout != nil {
		timeout = *a.Timeout
	}

	ctx, cancel := context.WithTimeout(parentCtx, timeout)
	defer cancel()

	resultChan := make(chan actionResult, 1)

	if contextAction, ok := action.(ContextAction); ok {
		go executeContextAction(ctx, contextAction, data, resultChan, step, total)
	} else {
		go executeLegacyAction(ctx, action, data, resultChan, step, total)
	}

	return a.waitForResult(ctx, resultChan, action, timeout, step, total)
}

// waitForResult waits for action completion or timeout and handles the result.
func (a *CompositeAction) waitForResult(ctx context.Context, resultChan <-chan actionResult, action Action, timeout time.Duration, step, total int) error {
	select {
	case res := <-resultChan:
		if res.err != nil {
			return a.handleExecutionError(res.err, action, timeout, step, total)
		}
		return nil
	case <-ctx.Done():
		return a.logAndReturnTimeoutError(action, timeout, step, total)
	}
}

// handleExecutionError processes action execution errors, distinguishing timeouts from other errors.
func (a *CompositeAction) handleExecutionError(err error, action Action, timeout time.Duration, step, total int) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return a.logAndReturnTimeoutError(action, timeout, step, total)
	}

	GetLogger().Error("Composite action failed",
		logger.String("component", "analysis.processor.actions"),
		logger.String("detection_id", a.CorrelationID),
		logger.Int("step", step),
		logger.Int("total_steps", total),
		logger.String("action_description", action.GetDescription()),
		logger.Error(err),
		logger.String("operation", "composite_action_execute"))
	return err
}

// logAndReturnTimeoutError logs a timeout error and returns a structured timeout error.
func (a *CompositeAction) logAndReturnTimeoutError(action Action, timeout time.Duration, step, total int) error {
	GetLogger().Error("Composite action timed out",
		logger.String("component", "analysis.processor.actions"),
		logger.String("detection_id", a.CorrelationID),
		logger.Int("step", step),
		logger.Int("total_steps", total),
		logger.String("action_description", action.GetDescription()),
		logger.Float64("timeout_seconds", timeout.Seconds()),
		logger.String("operation", "composite_action_timeout"))
	return buildTimeoutError(action, timeout, step, total)
}
