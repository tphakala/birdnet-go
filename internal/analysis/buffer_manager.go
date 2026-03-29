package analysis

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/audiocore/buffer"
	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// monitorKey identifies a unique monitor (one per source x model).
type monitorKey struct {
	sourceID string
	modelID  string
}

// monitorConfig describes parameters for a single analysis buffer monitor.
type monitorConfig struct {
	sourceID    string
	modelID     string
	spec        classifier.ModelSpec
	readSize    int // bytes = ClipLength * SampleRate * bytesPerSample
	overlapSize int // bytes, scaled from user config, PCM-aligned
}

// BufferManager handles the lifecycle of analysis buffer monitors
type BufferManager struct {
	monitors  sync.Map // keyed by monitorKey → chan struct{}
	bn        *classifier.Orchestrator
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
func NewBufferManager(bn *classifier.Orchestrator, bufMgr *buffer.Manager, quitChan chan struct{}, wg *sync.WaitGroup) (*BufferManager, error) {
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
func MustNewBufferManager(bn *classifier.Orchestrator, bufMgr *buffer.Manager, quitChan chan struct{}, wg *sync.WaitGroup) *BufferManager {
	bm, err := NewBufferManager(bn, bufMgr, quitChan, wg)
	if err != nil {
		panic(fmt.Sprintf("MustNewBufferManager: %v", err))
	}
	return bm
}

// AddMonitor safely adds analysis buffer monitors for a source using all
// loaded models' configurations. This is a convenience wrapper around
// AddMonitors for callers that don't have model details (e.g., watchdog reset).
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

	// Build monitorConfigs for all loaded models.
	allInfos := m.bn.ModelInfos()
	configs := make([]monitorConfig, 0, len(allInfos))
	for i := range allInfos {
		configs = append(configs, buildMonitorConfig(source, &allInfos[i]))
	}

	// Fallback to primary model for backward compatibility when no
	// models are registered via the orchestrator's model map.
	if len(configs) == 0 {
		cfg := buildMonitorConfig(source, &m.bn.ModelInfo)
		configs = []monitorConfig{cfg}
	}

	return m.AddMonitors(source, configs)
}

// AddMonitors creates one analysis buffer monitor goroutine per monitorConfig
// for the given source. Each monitor is stored under a composite
// monitorKey{source, cfg.modelID} so that multiple models can watch the same
// source simultaneously.
func (m *BufferManager) AddMonitors(source string, models []monitorConfig) error {
	// Validate source parameter
	if source == "" {
		return errors.Newf("cannot add monitors for empty source").
			Component("analysis.buffer").
			Category(errors.CategoryValidation).
			Context("operation", "add_monitors").
			Context("retryable", false).
			Build()
	}

	// Check if BirdNET instance is available
	if m.bn == nil {
		return errors.Newf("BirdNET instance not initialized").
			Component("analysis.buffer").
			Category(errors.CategoryBuffer).
			Context("operation", "add_monitors").
			Context("source", source).
			Context("retryable", false).
			Build()
	}

	for _, cfg := range models {
		key := monitorKey{sourceID: source, modelID: cfg.modelID}

		// Create a monitor-specific quit channel
		monitorQuit := make(chan struct{})

		// Use LoadOrStore to atomically check and store, preventing race conditions
		actual, loaded := m.monitors.LoadOrStore(key, monitorQuit)
		if loaded {
			// Monitor already exists for this (source, model) - not an error
			continue
		}

		// Use the channel we just stored (actual is our monitorQuit channel)
		monitorQuit = actual.(chan struct{})

		// Capture cfg for the goroutine closure
		monitorCfg := cfg

		// Start the monitor with error handling
		m.wg.Go(func() {
			defer func() {
				// Panic recovery for the monitor goroutine
				if r := recover(); r != nil {
					m.logger.Error("Monitor goroutine panicked",
						logger.String("source", source),
						logger.String("model_id", monitorCfg.modelID),
						logger.Any("panic", r),
						logger.String("component", "analysis.buffer"))
				}

				// Clean up monitor from map if it exits unexpectedly
				if quitChanIface, exists := m.monitors.Load(key); exists {
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
					m.monitors.Delete(key)
				}
			}()

			// Run the monitor using audiocore buffer manager
			m.analysisBufferMonitor(monitorQuit, monitorCfg)
		})
	}

	return nil
}

// RemoveMonitor safely stops and removes all monitors for a source.
// It iterates the monitors map and deletes every entry whose
// monitorKey.sourceID matches the given source.
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

	m.monitors.Range(func(key, value any) bool {
		mk, ok := key.(monitorKey)
		if !ok || mk.sourceID != source {
			return true // continue iteration
		}

		// Signal the monitor to stop with safe type assertion
		if quitChanTyped, okCh := value.(chan struct{}); okCh {
			m.safeCloseChannel(quitChanTyped, source)
		} else {
			m.logger.Warn("Invalid quit channel type during monitor removal",
				logger.String("source", source),
				logger.String("model_id", mk.modelID),
				logger.String("type", fmt.Sprintf("%T", value)),
				logger.String("component", "analysis.buffer"))
		}
		// Remove from the map
		m.monitors.Delete(key)

		return true
	})

	return nil
}

// RemoveAllMonitors stops all running monitors.
func (m *BufferManager) RemoveAllMonitors() []error {
	var removalErrors []error

	// Collect unique source IDs first, then remove each via RemoveMonitor.
	// This avoids modifying the map inside Range which could interact poorly
	// with the Range callback from RemoveMonitor.
	sources := make(map[string]struct{})
	m.monitors.Range(func(key, _ any) bool {
		if mk, ok := key.(monitorKey); ok {
			sources[mk.sourceID] = struct{}{}
		}
		return true
	})

	for source := range sources {
		if err := m.RemoveMonitor(source); err != nil {
			wrappedErr := errors.New(err).
				Component("analysis.buffer").
				Category(errors.CategoryBuffer).
				Context("operation", "remove_all_monitors").
				Context("failed_source", source).
				Build()
			removalErrors = append(removalErrors, wrappedErr)
		}
	}

	return removalErrors
}

// UpdateMonitors synchronizes the running monitors with the desired state from sourceModels.
// It starts new monitors for models that are not already running for a given source,
// stops monitors for sources/models that are no longer desired, and leaves
// unchanged monitors running.
func (m *BufferManager) UpdateMonitors(sourceModels map[string][]monitorConfig) error {
	// Performance metrics logging pattern
	startTime := time.Now()
	defer func() {
		m.logger.Debug("Buffer monitors updated",
			logger.Int64("duration_ms", time.Since(startTime).Milliseconds()),
			logger.String("component", "analysis.buffer"),
			logger.String("operation", "update_monitors"))
	}()

	desiredKeys := make(map[monitorKey]struct{})
	for sourceID, configs := range sourceModels {
		for _, config := range configs {
			desiredKeys[monitorKey{sourceID: sourceID, modelID: config.modelID}] = struct{}{}
		}
	}

	// Stop monitors that are no longer in the desired state
	var removedCount int
	m.monitors.Range(func(key, value any) bool {
		mk, ok := key.(monitorKey)
		if !ok {
			return true // Should not happen
		}
		if _, exists := desiredKeys[mk]; !exists {
			if quitChan, ok := value.(chan struct{}); ok {
				m.safeCloseChannel(quitChan, mk.sourceID)
			}
			m.monitors.Delete(key)
			removedCount++
		}
		return true
	})

	// Add new monitors. AddMonitors has internal checks to prevent duplicates.
	var addErrors []error
	for sourceID, configs := range sourceModels {
		if err := m.AddMonitors(sourceID, configs); err != nil {
			wrappedErr := errors.New(err).
				Component("analysis.buffer").
				Category(errors.CategoryBuffer).
				Context("operation", "update_monitors").
				Context("failed_operation", "add_monitors").
				Context("source", sourceID).
				Build()
			addErrors = append(addErrors, wrappedErr)
		}
	}

	// Final logging and error reporting
	if len(addErrors) > 0 {
		combinedErr := errors.Join(addErrors...)
		return errors.New(combinedErr).
			Component("analysis.buffer").
			Category(errors.CategoryBuffer).
			Context("operation", "update_monitors_final").
			Context("total_errors", len(addErrors)).
			Build()
	}

	return nil
}

// analysisBufferMonitor reads from the audiocore analysis buffer and feeds
// audio chunks to the BirdNET analysis pipeline.
func (m *BufferManager) analysisBufferMonitor(quitChan chan struct{}, cfg monitorConfig) {
	const detectionOffset = 10 * time.Second
	const pollInterval = 100 * time.Millisecond

	// Use the model-specific read size from the config instead of the
	// hardcoded constant that assumed BirdNET v2.4 parameters.
	analysisWindowBytes := cfg.readSize

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-quitChan:
			return
		case <-ticker.C:
			ab, err := m.bufferMgr.AnalysisBuffer(cfg.sourceID, cfg.modelID)
			if err != nil {
				// Buffer removed, exit gracefully
				m.logger.Info("analysis buffer removed, stopping monitor",
					logger.String("source_id", cfg.sourceID),
					logger.String("model_id", cfg.modelID))
				return
			}
			data, readErr := ab.Read()
			if readErr != nil {
				m.logger.Error("buffer read error",
					logger.String("source_id", cfg.sourceID),
					logger.String("model_id", cfg.modelID),
					logger.Error(readErr))
				time.Sleep(1 * time.Second)
				continue
			}

			// Exact equality is required: AnalysisBuffer.Read() returns
			// overlapSize + readSize bytes or nil. A partial read means
			// the buffer hasn't accumulated enough data yet.
			if len(data) == analysisWindowBytes {
				audioCapturedAt := time.Now()

				// Calculate the offset dynamically to pick up runtime configuration changes
				beginTimeOffset := time.Duration(conf.Setting().Realtime.Audio.Export.PreCapture)*time.Second + detectionOffset
				startTime := time.Now().Add(-beginTimeOffset)

				if processErr := ProcessData(context.Background(), m.bn, data, startTime, audioCapturedAt, cfg.sourceID, cfg.modelID); processErr != nil {
					m.logger.Error("error processing data",
						logger.String("source_id", cfg.sourceID),
						logger.String("model_id", cfg.modelID),
						logger.Error(processErr))
				}
			}
		}
	}
}

// buildMonitorConfig builds a monitorConfig from a ModelInfo.
// This is used by AddMonitor and UpdateMonitors to create a config for any model.
// Note: overlapSize is left at zero because it is only needed at buffer
// allocation time (in audio_pipeline_service.go), not by the monitor goroutine.
func buildMonitorConfig(sourceID string, info *classifier.ModelInfo) monitorConfig {
	spec := info.Spec
	clipLenSec := int(spec.ClipLength.Seconds())
	readSize := spec.SampleRate * clipLenSec * conf.NumChannels * (conf.BitDepth / 8)

	return monitorConfig{
		sourceID: sourceID,
		modelID:  info.ID,
		spec:     spec,
		readSize: readSize,
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
