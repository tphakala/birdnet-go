package events

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
	
	"github.com/tphakala/birdnet-go/internal/logging"
	"log/slog"
)

// EventBus provides asynchronous event processing with non-blocking guarantees
type EventBus struct {
	// Channel for events
	eventChan chan ErrorEvent
	
	// Configuration
	bufferSize int
	workers    int
	
	// State management
	ctx         context.Context
	cancel      context.CancelFunc
	wg          sync.WaitGroup
	initialized atomic.Bool
	running     atomic.Bool
	mu          sync.Mutex
	
	// Consumers
	consumers []EventConsumer
	
	// Metrics
	stats EventBusStats
	
	// Logging
	logger *slog.Logger
}

// Global event bus instance (lazily initialized)
var (
	globalEventBus *EventBus
	globalMutex    sync.Mutex
)

// DefaultConfig returns the default event bus configuration
func DefaultConfig() *Config {
	return &Config{
		BufferSize: 10000,
		Workers:    4,
		Enabled:    true,
	}
}

// Config holds event bus configuration
type Config struct {
	BufferSize int
	Workers    int
	Enabled    bool
}

// Initialize creates or returns the global event bus instance
func Initialize(config *Config) (*EventBus, error) {
	globalMutex.Lock()
	defer globalMutex.Unlock()
	
	// Return existing instance if already initialized
	if globalEventBus != nil {
		return globalEventBus, nil
	}
	
	// Use default config if none provided
	if config == nil {
		config = DefaultConfig()
	}
	
	// Skip initialization if disabled
	if !config.Enabled {
		return nil, nil
	}
	
	// Create new event bus
	ctx, cancel := context.WithCancel(context.Background())
	
	eb := &EventBus{
		eventChan:  make(chan ErrorEvent, config.BufferSize),
		bufferSize: config.BufferSize,
		workers:    config.Workers,
		ctx:        ctx,
		cancel:     cancel,
		consumers:  make([]EventConsumer, 0),
		logger:     logging.ForService("events"),
	}
	
	// Mark as initialized
	eb.initialized.Store(true)
	
	// Store global instance
	globalEventBus = eb
	
	eb.logger.Info("event bus initialized",
		"buffer_size", config.BufferSize,
		"workers", config.Workers,
	)
	
	return eb, nil
}

// GetEventBus returns the global event bus instance
func GetEventBus() *EventBus {
	globalMutex.Lock()
	defer globalMutex.Unlock()
	return globalEventBus
}

// IsInitialized returns true if the event bus has been initialized
func IsInitialized() bool {
	globalMutex.Lock()
	defer globalMutex.Unlock()
	return globalEventBus != nil && globalEventBus.initialized.Load()
}

// RegisterConsumer adds a new event consumer
func (eb *EventBus) RegisterConsumer(consumer EventConsumer) error {
	if eb == nil {
		return fmt.Errorf("event bus not initialized")
	}
	
	eb.mu.Lock()
	defer eb.mu.Unlock()
	
	// Check for duplicate
	for _, existing := range eb.consumers {
		if existing.Name() == consumer.Name() {
			return fmt.Errorf("consumer %s already registered", consumer.Name())
		}
	}
	
	eb.consumers = append(eb.consumers, consumer)
	
	eb.logger.Info("registered event consumer",
		"consumer", consumer.Name(),
		"supports_batching", consumer.SupportsBatching(),
	)
	
	// Start workers if this is the first consumer and not already running
	if len(eb.consumers) == 1 && !eb.running.Load() {
		eb.start()
	}
	
	return nil
}

// TryPublish attempts to publish an event without blocking
// Returns true if the event was accepted, false if dropped
func (eb *EventBus) TryPublish(event ErrorEvent) bool {
	if eb == nil || !eb.initialized.Load() || !eb.running.Load() {
		return false
	}
	
	// Fast path - check if we have consumers
	eb.mu.Lock()
	hasConsumers := len(eb.consumers) > 0
	eb.mu.Unlock()
	
	if !hasConsumers {
		return false
	}
	
	// Non-blocking send
	select {
	case eb.eventChan <- event:
		atomic.AddUint64(&eb.stats.EventsReceived, 1)
		return true
	default:
		// Channel full, drop the event
		atomic.AddUint64(&eb.stats.EventsDropped, 1)
		
		// Log at debug level to avoid spam
		if eb.logger != nil {
			eb.logger.Debug("event dropped due to full buffer",
				"component", event.GetComponent(),
				"category", event.GetCategory(),
			)
		}
		return false
	}
}

// start begins the worker goroutines
func (eb *EventBus) start() {
	if eb.running.Swap(true) {
		return // Already running
	}
	
	eb.logger.Info("starting event bus workers", "count", eb.workers)
	
	// Start worker goroutines
	for i := 0; i < eb.workers; i++ {
		eb.wg.Add(1)
		go eb.worker(i)
	}
}

// worker processes events from the channel
func (eb *EventBus) worker(id int) {
	defer eb.wg.Done()
	
	logger := eb.logger.With("worker_id", id)
	logger.Debug("worker started")
	
	for {
		select {
		case <-eb.ctx.Done():
			logger.Debug("worker stopping due to context cancellation")
			return
			
		case event, ok := <-eb.eventChan:
			if !ok {
				logger.Debug("worker stopping due to channel closure")
				return
			}
			
			eb.processEvent(event, logger)
		}
	}
}

// processEvent sends the event to all registered consumers
func (eb *EventBus) processEvent(event ErrorEvent, logger *slog.Logger) {
	eb.mu.Lock()
	consumers := make([]EventConsumer, len(eb.consumers))
	copy(consumers, eb.consumers)
	eb.mu.Unlock()
	
	for _, consumer := range consumers {
		// Process in a recovery wrapper to prevent panics
		func() {
			defer func() {
				if r := recover(); r != nil {
					atomic.AddUint64(&eb.stats.ConsumerErrors, 1)
					logger.Error("consumer panicked",
						"consumer", consumer.Name(),
						"panic", r,
						"component", event.GetComponent(),
						"category", event.GetCategory(),
					)
				}
			}()
			
			err := consumer.ProcessEvent(event)
			if err != nil {
				atomic.AddUint64(&eb.stats.ConsumerErrors, 1)
				logger.Error("consumer error",
					"consumer", consumer.Name(),
					"error", err,
					"component", event.GetComponent(),
					"category", event.GetCategory(),
				)
			} else {
				atomic.AddUint64(&eb.stats.EventsProcessed, 1)
			}
		}()
	}
}

// Shutdown gracefully shuts down the event bus
func (eb *EventBus) Shutdown(timeout time.Duration) error {
	if eb == nil || !eb.initialized.Load() {
		return nil
	}
	
	eb.logger.Info("shutting down event bus", "timeout", timeout)
	
	// Stop accepting new events
	eb.running.Store(false)
	
	// Cancel context to signal workers
	eb.cancel()
	
	// Wait for workers with timeout
	done := make(chan struct{})
	go func() {
		eb.wg.Wait()
		close(done)
	}()
	
	select {
	case <-done:
		eb.logger.Info("event bus shutdown complete")
		return nil
	case <-time.After(timeout):
		eb.logger.Warn("event bus shutdown timeout exceeded")
		return fmt.Errorf("shutdown timeout exceeded")
	}
}

// GetStats returns current event bus statistics
func (eb *EventBus) GetStats() EventBusStats {
	if eb == nil {
		return EventBusStats{}
	}
	
	return EventBusStats{
		EventsReceived:   atomic.LoadUint64(&eb.stats.EventsReceived),
		EventsSuppressed: atomic.LoadUint64(&eb.stats.EventsSuppressed),
		EventsProcessed:  atomic.LoadUint64(&eb.stats.EventsProcessed),
		EventsDropped:    atomic.LoadUint64(&eb.stats.EventsDropped),
		ConsumerErrors:   atomic.LoadUint64(&eb.stats.ConsumerErrors),
	}
}