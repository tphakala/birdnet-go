package analysis

import (
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/birdnet"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/myaudio"
)

// BufferManager handles the lifecycle of analysis buffer monitors
type BufferManager struct {
	monitors sync.Map
	bn       *birdnet.BirdNET
	quitChan chan struct{}
	wg       *sync.WaitGroup
}

// NewBufferManager creates a new buffer manager
func NewBufferManager(bn *birdnet.BirdNET, quitChan chan struct{}, wg *sync.WaitGroup) *BufferManager {
	return &BufferManager{
		bn:       bn,
		quitChan: quitChan,
		wg:       wg,
	}
}

// AddMonitor safely adds a new analysis buffer monitor for a source
func (m *BufferManager) AddMonitor(source string) error {
	// Validate source parameter
	if source == "" {
		return errors.Newf("cannot add monitor for empty source").
			Component("analysis.buffer").
			Category(errors.CategoryValidation).
			Context("operation", "add_monitor").
			Context("retryable", false).
			Build()
	}

	// Check if BirdNET instance is available
	if m.bn == nil {
		return errors.Newf("BirdNET instance not initialized").
			Component("analysis.buffer").
			Category(errors.CategoryBuffer).
			Context("operation", "add_monitor").
			Context("source", source).
			Context("retryable", false).
			Build()
	}

	// Check if monitor already exists
	if _, exists := m.monitors.Load(source); exists {
		// Not an error - monitor already running for this source
		return nil
	}

	// Create a monitor-specific quit channel
	monitorQuit := make(chan struct{})
	m.monitors.Store(source, monitorQuit)

	// Start the monitor with error handling
	m.wg.Add(1)
	go func() {
		defer func() {
			m.wg.Done()
			// Clean up monitor from map if it exits unexpectedly
			if quitChan, exists := m.monitors.Load(source); exists {
				select {
				case <-quitChan.(chan struct{}):
					// Normal shutdown - quit channel was closed
				default:
					// Unexpected exit - clean up
					close(quitChan.(chan struct{}))
				}
				m.monitors.Delete(source)
			}
		}()
		
		// Run the monitor - any panics will be recovered by the defer
		myaudio.AnalysisBufferMonitor(m.wg, m.bn, monitorQuit, source)
	}()

	return nil
}

// RemoveMonitor safely stops and removes a monitor for a source
func (m *BufferManager) RemoveMonitor(source string) error {
	// Validate source parameter
	if source == "" {
		return errors.Newf("cannot remove monitor for empty source").
			Component("analysis.buffer").
			Category(errors.CategoryValidation).
			Context("operation", "remove_monitor").
			Context("retryable", false).
			Build()
	}

	// Get the monitor's quit channel
	quitChan, exists := m.monitors.Load(source)
	if !exists {
		// Not an error - monitor doesn't exist
		return nil
	}

	// Safely close the quit channel with panic recovery
	defer func() {
		if r := recover(); r != nil {
			// Channel was already closed - this is expected during shutdown
			logger := GetLogger()
			logger.Debug("Channel close panic recovered during monitor removal",
				"source", source,
				"panic", r,
				"component", "analysis.buffer")
		}
	}()

	// Signal the monitor to stop
	close(quitChan.(chan struct{}))
	// Remove from the map
	m.monitors.Delete(source)

	return nil
}

// RemoveAllMonitors stops all running monitors
func (m *BufferManager) RemoveAllMonitors() []error {
	var removalErrors []error
	
	m.monitors.Range(func(key, value interface{}) bool {
		source := key.(string)
		if err := m.RemoveMonitor(source); err != nil {
			// Wrap the error with additional context
			wrappedErr := errors.New(err).
				Component("analysis.buffer").
				Category(errors.CategoryBuffer).
				Context("operation", "remove_all_monitors").
				Context("failed_source", source).
				Build()
			removalErrors = append(removalErrors, wrappedErr)
		}
		return true
	})
	
	return removalErrors
}

// UpdateMonitors ensures monitors are running for all given sources
func (m *BufferManager) UpdateMonitors(sources []string) error {
	// Performance metrics logging pattern
	logger := GetLogger()
	startTime := time.Now()
	defer func() {
		logger.Debug("Buffer monitors updated",
			"duration_ms", time.Since(startTime).Milliseconds(),
			"source_count", len(sources),
			"component", "analysis.buffer",
			"operation", "update_monitors")
	}()

	// Validate input
	if sources == nil {
		return errors.Newf("sources list cannot be nil").
			Component("analysis.buffer").
			Category(errors.CategoryValidation).
			Context("operation", "update_monitors").
			Context("retryable", false).
			Build()
	}

	// Track existing monitors that should be removed
	toRemove := make(map[string]bool)
	currentCount := 0
	m.monitors.Range(func(key, _ interface{}) bool {
		toRemove[key.(string)] = true
		currentCount++
		return true
	})

	// State transition logging pattern
	logger.Info("Updating buffer monitors",
		"current_monitors", currentCount,
		"requested_sources", len(sources),
		"component", "analysis.buffer")

	var addErrors []error
	var removeErrors []error
	addedCount := 0

	// Add new monitors and mark existing ones as still needed
	for _, source := range sources {
		if source != "" {
			wasExisting := toRemove[source]
			delete(toRemove, source)
			
			if !wasExisting {
				if err := m.AddMonitor(source); err != nil {
					wrappedErr := errors.New(err).
						Component("analysis.buffer").
						Category(errors.CategoryBuffer).
						Context("operation", "update_monitors").
						Context("failed_operation", "add_monitor").
						Context("source", source).
						Build()
					addErrors = append(addErrors, wrappedErr)
				} else {
					addedCount++
				}
			}
		}
	}

	// Remove monitors that are no longer needed
	removedCount := 0
	for source := range toRemove {
		if err := m.RemoveMonitor(source); err != nil {
			wrappedErr := errors.New(err).
				Component("analysis.buffer").
				Category(errors.CategoryBuffer).
				Context("operation", "update_monitors").
				Context("failed_operation", "remove_monitor").
				Context("source", source).
				Build()
			removeErrors = append(removeErrors, wrappedErr)
		} else {
			removedCount++
		}
	}

	// State transition logging - final state
	newCount := currentCount - removedCount + addedCount
	logger.Info("Buffer monitor update completed",
		"monitors_added", addedCount,
		"monitors_removed", removedCount, 
		"final_monitor_count", newCount,
		"add_errors", len(addErrors),
		"remove_errors", len(removeErrors),
		"component", "analysis.buffer")

	// Return combined error if any operations failed
	if len(addErrors) > 0 || len(removeErrors) > 0 {
		addErrors = append(addErrors, removeErrors...)
		return errors.Newf("monitor update completed with %d errors", len(addErrors)).
			Component("analysis.buffer").
			Category(errors.CategoryBuffer).
			Context("operation", "update_monitors").
			Context("total_errors", len(addErrors)).
			Context("successful_adds", addedCount).
			Context("successful_removes", removedCount).
			Build()
	}

	return nil
}
