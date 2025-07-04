package audiocore

import (
	"context"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/errors"
)

// managerImpl is the concrete implementation of AudioManager
type managerImpl struct {
	config          ManagerConfig
	sources         map[string]AudioSource
	processorChains map[string]ProcessorChain
	bufferPool      BufferPool
	audioOutput     chan AudioData
	ctx             context.Context
	cancel          context.CancelFunc
	wg              sync.WaitGroup
	mu              sync.RWMutex
	started         bool
	metrics         ManagerMetrics
	metricsMu       sync.RWMutex
}

// NewAudioManager creates a new audio manager
func NewAudioManager(config *ManagerConfig) AudioManager {
	// Set defaults if not specified
	if config.DefaultBufferSize == 0 {
		config.DefaultBufferSize = 4096
	}
	if config.MetricsInterval == 0 {
		config.MetricsInterval = 10 * time.Second
	}
	if config.ProcessingTimeout == 0 {
		config.ProcessingTimeout = 5 * time.Second
	}

	// Create buffer pool with default config if not specified
	if config.BufferPoolConfig.SmallBufferSize == 0 {
		config.BufferPoolConfig.SmallBufferSize = 4 * 1024     // 4KB
		config.BufferPoolConfig.MediumBufferSize = 64 * 1024   // 64KB
		config.BufferPoolConfig.LargeBufferSize = 1024 * 1024  // 1MB
		config.BufferPoolConfig.MaxBuffersPerSize = 100
	}

	return &managerImpl{
		config:          *config,
		sources:         make(map[string]AudioSource),
		processorChains: make(map[string]ProcessorChain),
		bufferPool:      NewBufferPool(config.BufferPoolConfig),
		audioOutput:     make(chan AudioData, 100),
	}
}

// AddSource adds a new audio source
func (m *managerImpl) AddSource(source AudioSource) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Check if we've reached the maximum number of sources
	if m.config.MaxSources > 0 && len(m.sources) >= m.config.MaxSources {
		return errors.New(ErrMaxSourcesReached).
			Component(ComponentAudioCore).
			Context("max_sources", m.config.MaxSources).
			Context("current_sources", len(m.sources)).
			Build()
	}

	// Check if source already exists
	if _, exists := m.sources[source.ID()]; exists {
		return errors.New(ErrSourceAlreadyExists).
			Component(ComponentAudioCore).
			Context("source_id", source.ID()).
			Build()
	}

	m.sources[source.ID()] = source

	// If manager is already started, start this source too
	if m.started {
		go m.processSource(source)
	}

	return nil
}

// RemoveSource removes an audio source
func (m *managerImpl) RemoveSource(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	source, exists := m.sources[id]
	if !exists {
		return errors.New(ErrSourceNotFound).
			Component(ComponentAudioCore).
			Context("source_id", id).
			Build()
	}

	// Stop the source
	if err := source.Stop(); err != nil {
		return errors.New(err).
			Component(ComponentAudioCore).
			Category(errors.CategoryAudio).
			Context("operation", "stop_source").
			Context("source_id", id).
			Build()
	}

	delete(m.sources, id)
	delete(m.processorChains, id)

	return nil
}

// GetSource retrieves a source by ID
func (m *managerImpl) GetSource(id string) (AudioSource, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	source, exists := m.sources[id]
	return source, exists
}

// ListSources returns all registered sources
func (m *managerImpl) ListSources() []AudioSource {
	m.mu.RLock()
	defer m.mu.RUnlock()

	sources := make([]AudioSource, 0, len(m.sources))
	for _, source := range m.sources {
		sources = append(sources, source)
	}
	return sources
}

// SetProcessorChain sets the processor chain for a source
func (m *managerImpl) SetProcessorChain(sourceID string, chain ProcessorChain) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.sources[sourceID]; !exists {
		return errors.New(ErrSourceNotFound).
			Component(ComponentAudioCore).
			Context("source_id", sourceID).
			Build()
	}

	m.processorChains[sourceID] = chain
	return nil
}

// Start begins processing audio from all sources
func (m *managerImpl) Start(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.started {
		return errors.New(nil).
			Component(ComponentAudioCore).
			Category(errors.CategoryState).
			Context("error", "manager already started").
			Build()
	}

	m.ctx, m.cancel = context.WithCancel(ctx)
	m.started = true

	// Start metrics collection if enabled
	if m.config.EnableMetrics {
		m.wg.Add(1)
		go m.collectMetrics()
	}

	// Start processing each source
	for _, source := range m.sources {
		m.wg.Add(1)
		go m.processSource(source)
	}

	return nil
}

// Stop halts all audio processing
func (m *managerImpl) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.started {
		return errors.New(ErrManagerNotStarted).
			Component(ComponentAudioCore).
			Build()
	}

	// Cancel context to signal all goroutines to stop
	m.cancel()

	// Stop all sources
	var errs []error
	for _, source := range m.sources {
		if err := source.Stop(); err != nil {
			errs = append(errs, err)
		}
	}

	// Wait for all goroutines to finish
	m.wg.Wait()

	// Close output channel
	close(m.audioOutput)

	m.started = false

	// Return any errors that occurred
	if len(errs) > 0 {
		return errors.Join(errs...)
	}

	return nil
}

// AudioOutput returns a channel that emits processed audio from all sources
func (m *managerImpl) AudioOutput() <-chan AudioData {
	return m.audioOutput
}

// Metrics returns current metrics for the manager
func (m *managerImpl) Metrics() ManagerMetrics {
	m.metricsMu.RLock()
	defer m.metricsMu.RUnlock()

	// Update buffer pool stats
	m.metrics.BufferPoolStats = m.bufferPool.Stats()
	m.metrics.ActiveSources = len(m.sources)
	m.metrics.LastUpdate = time.Now()

	return m.metrics
}

// processSource handles audio processing for a single source
func (m *managerImpl) processSource(source AudioSource) {
	defer m.wg.Done()

	// Start the source
	if err := source.Start(m.ctx); err != nil {
		// Log error but continue with other sources
		return
	}

	// Get processor chain for this source
	m.mu.RLock()
	chain := m.processorChains[source.ID()]
	m.mu.RUnlock()

	// Process audio from this source
	for {
		select {
		case <-m.ctx.Done():
			return

		case audioData, ok := <-source.AudioOutput():
			if !ok {
				return
			}

			// Process through chain if available
			if chain != nil {
				processedData, err := chain.Process(m.ctx, &audioData)
				if err == nil {
					audioData = *processedData
				}
			}

			// Send to output channel
			select {
			case m.audioOutput <- audioData:
				m.updateMetrics(true)
			case <-m.ctx.Done():
				return
			default:
				// Channel full, drop frame
				m.updateMetrics(false)
			}

		case err := <-source.Errors():
			// Handle source errors
			if err != nil {
				m.incrementErrorCount()
			}
		}
	}
}

// collectMetrics periodically collects metrics
func (m *managerImpl) collectMetrics() {
	defer m.wg.Done()

	ticker := time.NewTicker(m.config.MetricsInterval)
	defer ticker.Stop()

	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			// Metrics are updated continuously, just trigger an update
			m.Metrics()
		}
	}
}

// updateMetrics updates processing metrics
func (m *managerImpl) updateMetrics(success bool) {
	m.metricsMu.Lock()
	defer m.metricsMu.Unlock()

	if success {
		m.metrics.ProcessedFrames++
	} else {
		m.metrics.ProcessingErrors++
	}
}

// incrementErrorCount increments the error counter
func (m *managerImpl) incrementErrorCount() {
	m.metricsMu.Lock()
	defer m.metricsMu.Unlock()
	m.metrics.ProcessingErrors++
}