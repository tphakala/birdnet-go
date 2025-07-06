package audiocore

import (
	"log/slog"
	"sync"

	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logging"
)

// analyzerManagerImpl implements AnalyzerManager
// Usage: manager := NewAnalyzerManager(factory)
//        analyzer, err := manager.CreateAnalyzer("birdnet", config)
//        manager.RegisterAnalyzer(analyzer)
type analyzerManagerImpl struct {
	analyzers map[string]Analyzer
	factory   AnalyzerFactory
	mu        sync.RWMutex
	logger    *slog.Logger
	metrics   *MetricsCollector
}

// NewAnalyzerManager creates a new analyzer manager
func NewAnalyzerManager(factory AnalyzerFactory) AnalyzerManager {
	logger := logging.ForService("audiocore")
	if logger == nil {
		logger = slog.Default()
	}
	logger = logger.With("component", "analyzer_manager")

	return &analyzerManagerImpl{
		analyzers: make(map[string]Analyzer),
		factory:   factory,
		logger:    logger,
		metrics:   GetMetrics(),
	}
}

// RegisterAnalyzer adds an analyzer to the pool
func (m *analyzerManagerImpl) RegisterAnalyzer(analyzer Analyzer) error {
	if analyzer == nil {
		return errors.New(nil).
			Component(ComponentAudioCore).
			Category(errors.CategoryValidation).
			Context("error", "analyzer is nil").
			Build()
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	id := analyzer.ID()
	if _, exists := m.analyzers[id]; exists {
		return errors.New(nil).
			Component(ComponentAudioCore).
			Category(errors.CategoryState).
			Context("analyzer_id", id).
			Context("error", "analyzer already registered").
			Build()
	}

	m.analyzers[id] = analyzer
	m.logger.Info("analyzer registered",
		"analyzer_id", id,
		"type", analyzer.GetConfiguration().Type)

	// Metrics tracking could be added here if needed

	return nil
}

// GetAnalyzer retrieves analyzer by ID
func (m *analyzerManagerImpl) GetAnalyzer(id string) (Analyzer, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	analyzer, exists := m.analyzers[id]
	if !exists {
		return nil, errors.New(nil).
			Component(ComponentAudioCore).
			Category(errors.CategoryNotFound).
			Context("analyzer_id", id).
			Context("error", "analyzer not found").
			Build()
	}

	return analyzer, nil
}

// ListAnalyzers returns all registered analyzers
func (m *analyzerManagerImpl) ListAnalyzers() []Analyzer {
	m.mu.RLock()
	defer m.mu.RUnlock()

	analyzers := make([]Analyzer, 0, len(m.analyzers))
	for _, analyzer := range m.analyzers {
		analyzers = append(analyzers, analyzer)
	}

	return analyzers
}

// CreateAnalyzer creates analyzer from config
func (m *analyzerManagerImpl) CreateAnalyzer(config AnalyzerConfig) (Analyzer, error) {
	if m.factory == nil {
		return nil, errors.New(nil).
			Component(ComponentAudioCore).
			Category(errors.CategoryConfiguration).
			Context("error", "no analyzer factory configured").
			Build()
	}

	// Generate ID if not provided
	id := config.Type
	if config.ExtraConfig != nil {
		if configID, ok := config.ExtraConfig["id"].(string); ok && configID != "" {
			id = configID
		}
	}

	// Create the analyzer
	analyzer, err := m.factory.CreateAnalyzer(id, config)
	if err != nil {
		return nil, errors.New(err).
			Component(ComponentAudioCore).
			Category(errors.CategoryConfiguration).
			Context("analyzer_type", config.Type).
			Context("operation", "create_analyzer").
			Build()
	}

	// Register it
	if err := m.RegisterAnalyzer(analyzer); err != nil {
		// Clean up the created analyzer
		_ = analyzer.Close()
		return nil, err
	}

	return analyzer, nil
}

// RemoveAnalyzer removes and closes analyzer
func (m *analyzerManagerImpl) RemoveAnalyzer(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	analyzer, exists := m.analyzers[id]
	if !exists {
		return errors.New(nil).
			Component(ComponentAudioCore).
			Category(errors.CategoryNotFound).
			Context("analyzer_id", id).
			Context("error", "analyzer not found").
			Build()
	}

	// Close the analyzer
	if err := analyzer.Close(); err != nil {
		m.logger.Warn("error closing analyzer",
			"analyzer_id", id,
			"error", err)
	}

	delete(m.analyzers, id)
	m.logger.Info("analyzer removed",
		"analyzer_id", id)

	// Metrics tracking could be added here if needed

	return nil
}

// Close closes all analyzers
func (m *analyzerManagerImpl) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	var firstErr error
	for id, analyzer := range m.analyzers {
		if err := analyzer.Close(); err != nil {
			m.logger.Warn("error closing analyzer",
				"analyzer_id", id,
				"error", err)
			if firstErr == nil {
				firstErr = err
			}
		}
		delete(m.analyzers, id)
	}

	return firstErr
}

// GetMetrics returns analyzer metrics
func (m *analyzerManagerImpl) GetMetrics() map[string]any {
	m.mu.RLock()
	defer m.mu.RUnlock()

	metrics := map[string]any{
		"total_analyzers": len(m.analyzers),
		"analyzers":       make(map[string]any),
	}

	for id, analyzer := range m.analyzers {
		config := analyzer.GetConfiguration()
		metrics["analyzers"].(map[string]any)[id] = map[string]any{
			"type":      config.Type,
			"threshold": config.Threshold,
		}
	}

	return metrics
}

// CompositeAnalyzerFactory combines multiple analyzer factories
type CompositeAnalyzerFactory struct {
	factories map[string]AnalyzerFactory
	mu        sync.RWMutex
}

// NewCompositeAnalyzerFactory creates a new composite factory
func NewCompositeAnalyzerFactory() *CompositeAnalyzerFactory {
	return &CompositeAnalyzerFactory{
		factories: make(map[string]AnalyzerFactory),
	}
}

// RegisterFactory registers a factory for a specific analyzer type
func (f *CompositeAnalyzerFactory) RegisterFactory(analyzerType string, factory AnalyzerFactory) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.factories[analyzerType] = factory
}

// CreateAnalyzer creates an analyzer from configuration
func (f *CompositeAnalyzerFactory) CreateAnalyzer(id string, config AnalyzerConfig) (Analyzer, error) {
	f.mu.RLock()
	factory, exists := f.factories[config.Type]
	f.mu.RUnlock()

	if !exists {
		return nil, errors.New(nil).
			Component(ComponentAudioCore).
			Category(errors.CategoryConfiguration).
			Context("analyzer_type", config.Type).
			Context("error", "unsupported analyzer type").
			Build()
	}

	return factory.CreateAnalyzer(id, config)
}

// SupportedTypes returns list of supported analyzer types
func (f *CompositeAnalyzerFactory) SupportedTypes() []string {
	f.mu.RLock()
	defer f.mu.RUnlock()

	types := make([]string, 0, len(f.factories))
	for typ := range f.factories {
		types = append(types, typ)
	}
	return types
}

// Ensure interfaces are implemented
var (
	_ AnalyzerManager = (*analyzerManagerImpl)(nil)
	_ AnalyzerFactory = (*CompositeAnalyzerFactory)(nil)
)