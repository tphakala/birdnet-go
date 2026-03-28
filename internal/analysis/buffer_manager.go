package analysis

import (
	"fmt"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/audiocore/buffer"
	"github.com/tphakala/birdnet-go/internal/birdnet"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// BufferManager handles the lifecycle of analysis buffer monitors
type BufferManager struct {
	monitors  sync.Map
	bn        *birdnet.BirdNET
	bufferMgr *buffer.Manager
	quitChan  chan struct{}
	wg        *sync.WaitGroup
	logger    logger.Logger
}

// NewBufferManager creates a new buffer manager with validation.
//
// This constructor performs parameter validation and returns an error
// if any required parameter is nil, following project guidelines.
//
// Parameters:
//   - bn: BirdNET instance for audio analysis
//   - bufMgr: AudioCore buffer manager for analysis buffer access
//   - quitChan: Channel for coordinated shutdown signaling
//   - wg: WaitGroup for goroutine lifecycle management
//
// Returns:
//   - *BufferManager: New buffer manager instance
//   - error: Validation error if any parameter is nil
func NewBufferManager(bn *birdnet.BirdNET, bufMgr *buffer.Manager, quitChan chan struct{}, wg *sync.WaitGroup) (*BufferManager, error) {
	// Validate required parameters
	if bn == nil {
		return nil, errors.Newf("BirdNET instance cannot be nil").
			Component("analysis.buffer").
			Category(errors.CategoryValidation).
			Context("operation", "new_buffer_manager").
			Build()
	}
	if bufMgr == nil {
		return nil, errors.Newf("buffer manager cannot be nil").
			Component("analysis.buffer").
			Category(errors.CategoryValidation).
			Context("operation", "new_buffer_manager").
			Build()
	}
	if quitChan == nil {
		return nil, errors.Newf("quit channel cannot be nil").
			Component("analysis.buffer").
			Category(errors.CategoryValidation).
			Context("operation", "new_buffer_manager").
			Build()
	}
	if wg == nil {
		return nil, errors.Newf("wait group cannot be nil").
			Component("analysis.buffer").
			Category(errors.CategoryValidation).
			Context("operation", "new_buffer_manager").
			Build()
	}

	return &BufferManager{
		bn:        bn,
		bufferMgr: bufMgr,
		quitChan:  quitChan,
		wg:        wg,
		logger:    GetLogger(),
	}, nil
}

// MustNewBufferManager creates a new buffer manager and panics on error.
//
// This function preserves the original panic behavior for early initialization
// contexts where error handling is not practical. It calls NewBufferManager
// and panics if validation fails.
//
// Parameters:
//   - bn: BirdNET instance for audio analysis
//   - bufMgr: AudioCore buffer manager for analysis buffer access
//   - quitChan: Channel for coordinated shutdown signaling
//   - wg: WaitGroup for goroutine lifecycle management
//
// Returns:
//   - *BufferManager: New buffer manager instance
//
// Panics:
//   - If any parameter validation fails
func MustNewBufferManager(bn *birdnet.BirdNET, bufMgr *buffer.Manager, quitChan chan struct{}, wg *sync.WaitGroup) *BufferManager {
	bm, err := NewBufferManager(bn, bufMgr, quitChan, wg)
	if err != nil {
		panic(fmt.Sprintf("MustNewBufferManager: %v", err))
	}
	return bm
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

	// Create a monitor-specific quit channel
	monitorQuit := make(chan struct{})

	// Use LoadOrStore to atomically check and store, preventing race conditions
	actual, loaded := m.monitors.LoadOrStore(source, monitorQuit)
	if loaded {
		// Monitor already exists for this source - not an error
		return nil
	}

	// Use the channel we just stored (actual is our monitorQuit channel)
	monitorQuit = actual.(chan struct{})

	// Start the monitor with error handling
	m.wg.Go(func() {
		defer func() {
			// Panic recovery for the monitor goroutine
			if r := recover(); r != nil {
				m.logger.Error("Monitor goroutine panicked",
					logger.String("source", source),
					logger.Any("panic", r),
					logger.String("component", "analysis.buffer"))
			}

			// Clean up monitor from map if it exits unexpectedly
			if quitChanIface, exists := m.monitors.Load(source); exists {
				// Safe type assertion
				if quitChan, ok := quitChanIface.(chan struct{}); ok {
					select {
					case <-quitChan:
						// Normal shutdown - quit channel was closed
					default:
						// Unexpected exit - safely close channel
						m.safeCloseChannel(quitChan, source)
					}
				}
				m.monitors.Delete(source)
			}
		}()

		// Run the monitor using audiocore buffer manager
		m.analysisBufferMonitor(monitorQuit, source)
	})

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

	// Signal the monitor to stop with safe type assertion
	if quitChanTyped, ok := quitChan.(chan struct{}); ok {
		m.safeCloseChannel(quitChanTyped, source)
	} else {
		m.logger.Warn("Invalid quit channel type during monitor removal",
			logger.String("source", source),
			logger.String("type", fmt.Sprintf("%T", quitChan)),
			logger.String("component", "analysis.buffer"))
	}
	// Remove from the map
	m.monitors.Delete(source)

	return nil
}

// RemoveAllMonitors stops all running monitors
func (m *BufferManager) RemoveAllMonitors() []error {
	var removalErrors []error

	m.monitors.Range(func(key, value any) bool {
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
	startTime := time.Now()
	defer func() {
		m.logger.Debug("Buffer monitors updated",
			logger.Int64("duration_ms", time.Since(startTime).Milliseconds()),
			logger.Int("source_count", len(sources)),
			logger.String("component", "analysis.buffer"),
			logger.String("operation", "update_monitors"))
	}()

	// Treat nil sources as empty slice to allow removing all monitors
	if sources == nil {
		sources = []string{}
	}

	// Track existing monitors that should be removed
	toRemove := make(map[string]bool)
	currentCount := 0
	m.monitors.Range(func(key, _ any) bool {
		toRemove[key.(string)] = true
		currentCount++
		return true
	})

	// State transition logging pattern
	m.logger.Info("Updating buffer monitors",
		logger.Int("current_monitors", currentCount),
		logger.Int("requested_sources", len(sources)),
		logger.String("component", "analysis.buffer"))

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
	m.logger.Info("Buffer monitor update completed",
		logger.Int("monitors_added", addedCount),
		logger.Int("monitors_removed", removedCount),
		logger.Int("final_monitor_count", newCount),
		logger.Int("add_errors", len(addErrors)),
		logger.Int("remove_errors", len(removeErrors)),
		logger.String("component", "analysis.buffer"))

	// Return combined error if any operations failed
	if len(addErrors) > 0 || len(removeErrors) > 0 {
		// Create dedicated allErrors slice and use errors.Join to preserve individual errors
		allErrors := make([]error, 0, len(addErrors)+len(removeErrors))
		allErrors = append(allErrors, addErrors...)
		allErrors = append(allErrors, removeErrors...)

		// Join all errors to preserve individual error details
		combinedErr := errors.Join(allErrors...)

		// Wrap with structured metadata
		return errors.New(combinedErr).
			Component("analysis.buffer").
			Category(errors.CategoryBuffer).
			Context("operation", "update_monitors").
			Context("total_errors", len(allErrors)).
			Context("add_errors", len(addErrors)).
			Context("remove_errors", len(removeErrors)).
			Context("successful_adds", addedCount).
			Context("successful_removes", removedCount).
			Build()
	}

	return nil
}

// analysisBufferMonitor reads from the audiocore analysis buffer and feeds
// audio chunks to the BirdNET analysis pipeline.
func (m *BufferManager) analysisBufferMonitor(quitChan chan struct{}, sourceID string) {
	const detectionOffset = 10 * time.Second
	const pollInterval = 100 * time.Millisecond
	// analysisWindowBytes is the expected size of a full analysis window
	// returned by AnalysisBuffer.Read(): CaptureLength seconds of audio at
	// SampleRate Hz, BitDepth bits, NumChannels channels.
	const analysisWindowBytes = conf.SampleRate * conf.CaptureLength * conf.NumChannels * (conf.BitDepth / 8)

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-quitChan:
			return
		case <-ticker.C:
			ab, err := m.bufferMgr.AnalysisBuffer(sourceID)
			if err != nil {
				// Buffer removed, exit gracefully
				m.logger.Info("analysis buffer removed, stopping monitor",
					logger.String("source_id", sourceID))
				return
			}
			data, readErr := ab.Read()
			if readErr != nil {
				m.logger.Error("buffer read error",
					logger.String("source_id", sourceID),
					logger.Error(readErr))
				time.Sleep(1 * time.Second)
				continue
			}

			if len(data) == analysisWindowBytes {
				audioCapturedAt := time.Now()

				// Calculate the offset dynamically to pick up runtime configuration changes
				beginTimeOffset := time.Duration(conf.Setting().Realtime.Audio.Export.PreCapture)*time.Second + detectionOffset
				startTime := time.Now().Add(-beginTimeOffset)

				if processErr := ProcessData(m.bn, data, startTime, audioCapturedAt, sourceID); processErr != nil {
					m.logger.Error("error processing data",
						logger.String("source_id", sourceID),
						logger.Error(processErr))
					_ = errors.New(processErr).
						Component("analysis").
						Category(errors.CategoryAudioAnalysis).
						Context("operation", "analysis_pipeline_processing").
						Context("source_id", sourceID).
						Build()
				}
			}
		}
	}
}

// safeCloseChannel safely closes a channel with panic recovery
// This function relies on panic recovery to handle double-close scenarios
// rather than trying to check channel state (which would be racy)
func (m *BufferManager) safeCloseChannel(ch chan struct{}, source string) {
	defer func() {
		if r := recover(); r != nil {
			// Double-close is expected in concurrent scenarios, log at debug level
			m.logger.Debug("Channel already closed",
				logger.String("source", source),
				logger.String("component", "analysis.buffer"))
		}
	}()

	// Simply close the channel - panic recovery handles double-close
	close(ch)
}
