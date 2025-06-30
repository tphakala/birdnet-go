package notification

import (
	"fmt"
	"sync"
	"sync/atomic"
	"text/template"
	"time"

	"log/slog"

	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/events"
	"github.com/tphakala/birdnet-go/internal/logging"
)

// getLoggerSafe returns a logger for the service, falling back to default if logging not initialized
func getLoggerSafe(service string) *slog.Logger {
	logger := logging.ForService(service)
	if logger == nil {
		logger = slog.Default().With("service", service)
	}
	return logger
}

// NotificationWorker implements EventConsumer to process error events
type NotificationWorker struct {
	service       *Service
	templates     map[string]*template.Template
	templateMu    sync.RWMutex
	config        *WorkerConfig
	circuitBreaker *CircuitBreaker
	
	// Metrics
	eventsProcessed atomic.Uint64
	eventsDropped   atomic.Uint64
	eventsFailed    atomic.Uint64
	
	logger *slog.Logger
}

// WorkerConfig holds configuration for the notification worker
type WorkerConfig struct {
	// BatchingEnabled enables batch processing of notifications
	BatchingEnabled bool
	// BatchSize is the maximum number of events to process in a batch
	BatchSize int
	// BatchTimeout is how long to wait for a full batch
	BatchTimeout time.Duration
	// CircuitBreaker settings
	FailureThreshold   int
	RecoveryTimeout    time.Duration
	HalfOpenMaxEvents  int
}

// DefaultWorkerConfig returns default configuration
func DefaultWorkerConfig() *WorkerConfig {
	return &WorkerConfig{
		BatchingEnabled:    false, // Start with single event processing
		BatchSize:          10,
		BatchTimeout:       100 * time.Millisecond,
		FailureThreshold:   5,
		RecoveryTimeout:    30 * time.Second,
		HalfOpenMaxEvents:  3,
	}
}

// CircuitBreaker implements the circuit breaker pattern
type CircuitBreaker struct {
	mu              sync.Mutex
	state           string // "closed", "open", "half-open"
	failures        int
	lastFailureTime time.Time
	successCount    int
	config          *WorkerConfig
}

// NewNotificationWorker creates a new notification worker
func NewNotificationWorker(service *Service, config *WorkerConfig) (*NotificationWorker, error) {
	if service == nil {
		return nil, fmt.Errorf("notification service is required")
	}
	
	if config == nil {
		config = DefaultWorkerConfig()
	}
	
	worker := &NotificationWorker{
		service:   service,
		templates: make(map[string]*template.Template),
		config:    config,
		circuitBreaker: &CircuitBreaker{
			state:  "closed",
			config: config,
		},
		logger: getLoggerSafe("notification-worker"),
	}
	
	// Pre-compile templates
	if err := worker.initTemplates(); err != nil {
		return nil, errors.New(err).
			Component("notification").
			Category(errors.CategoryConfiguration).
			Context("operation", "init_templates").
			Build()
	}
	
	return worker, nil
}

// initTemplates pre-compiles notification templates
func (w *NotificationWorker) initTemplates() error {
	templates := map[string]string{
		"error_critical": "Critical {{.Category}} error in {{.Component}}: {{.Message}}",
		"error_high":     "{{.Category}} error in {{.Component}}: {{.Message}}",
		"error_medium":   "{{.Component}} reported: {{.Message}}",
		"error_low":      "Minor issue in {{.Component}}",
		"error_with_context": "{{.Component}} error ({{.Category}}): {{.Message}}\nContext: {{.Context}}",
	}
	
	for name, tmplStr := range templates {
		tmpl, err := template.New(name).Parse(tmplStr)
		if err != nil {
			return fmt.Errorf("failed to parse template %s: %w", name, err)
		}
		w.templates[name] = tmpl
	}
	
	return nil
}

// Name returns the consumer name
func (w *NotificationWorker) Name() string {
	return "notification-worker"
}

// ProcessEvent processes a single error event
func (w *NotificationWorker) ProcessEvent(event events.ErrorEvent) error {
	// Check circuit breaker
	if !w.circuitBreaker.Allow() {
		w.eventsDropped.Add(1)
		w.logger.Debug("circuit breaker open, dropping event",
			"component", event.GetComponent(),
			"category", event.GetCategory(),
		)
		return nil // Don't propagate error when circuit is open
	}
	
	// Determine priority based on category
	priority := getNotificationPriority(event.GetCategory())
	
	// Only create notifications for high and critical priority errors
	if priority != PriorityHigh && priority != PriorityCritical {
		w.logger.Debug("skipping low priority error notification",
			"category", event.GetCategory(),
			"priority", priority,
			"component", event.GetComponent(),
		)
		return nil
	}
	
	// Create notification
	title := w.generateTitle(event, priority)
	message := w.generateMessage(event, priority)
	
	notification, err := w.service.CreateWithComponent(
		TypeError,
		priority,
		title,
		message,
		event.GetComponent(),
	)
	
	if err != nil {
		w.eventsFailed.Add(1)
		w.circuitBreaker.RecordFailure()
		
		// Check if it's a rate limit error
		var enhErr *errors.EnhancedError
		if errors.As(err, &enhErr) && enhErr.GetMessage() == "rate limit exceeded" {
			// Don't log rate limit errors as they're expected
			w.eventsDropped.Add(1)
			return nil
		}
		
		w.logger.Error("failed to create notification",
			"error", err,
			"component", event.GetComponent(),
			"category", event.GetCategory(),
		)
		return err
	}
	
	// Success
	w.eventsProcessed.Add(1)
	w.circuitBreaker.RecordSuccess()
	
	// Add context metadata
	if notification != nil && event.GetContext() != nil {
		for k, v := range event.GetContext() {
			notification.WithMetadata(k, v)
		}
		
		// Set expiry for non-critical notifications
		if priority != PriorityCritical {
			notification.WithExpiry(24 * time.Hour)
		}
	}
	
	w.logger.Debug("created error notification",
		"notification_id", notification.ID,
		"component", event.GetComponent(),
		"category", event.GetCategory(),
		"priority", priority,
	)
	
	return nil
}

// ProcessBatch processes multiple events at once
func (w *NotificationWorker) ProcessBatch(errorEvents []events.ErrorEvent) error {
	// For now, process individually
	// TODO: Implement true batch processing with aggregation
	var lastErr error
	successCount := 0
	
	for _, event := range errorEvents {
		if err := w.ProcessEvent(event); err != nil {
			lastErr = err
		} else {
			successCount++
		}
	}
	
	w.logger.Debug("processed event batch",
		"total", len(errorEvents),
		"success", successCount,
		"failed", len(errorEvents)-successCount,
	)
	
	return lastErr
}

// SupportsBatching returns true if this consumer supports batch processing
func (w *NotificationWorker) SupportsBatching() bool {
	return w.config.BatchingEnabled
}

// generateTitle generates a notification title based on the event
func (w *NotificationWorker) generateTitle(event events.ErrorEvent, priority Priority) string {
	category := event.GetCategory()
	component := event.GetComponent()
	
	switch priority {
	case PriorityCritical:
		return fmt.Sprintf("Critical %s Error in %s", category, component)
	case PriorityHigh:
		return fmt.Sprintf("%s Error in %s", category, component)
	default:
		return fmt.Sprintf("%s Issue", component)
	}
}

// generateMessage generates a notification message based on the event
func (w *NotificationWorker) generateMessage(event events.ErrorEvent, priority Priority) string {
	// For now, use simple message formatting
	// TODO: Use pre-compiled templates for better performance
	message := event.GetMessage()
	
	// Truncate very long messages
	const maxLength = 500
	if len(message) > maxLength {
		message = message[:maxLength-3] + "..."
	}
	
	return message
}

// GetStats returns worker statistics
func (w *NotificationWorker) GetStats() WorkerStats {
	return WorkerStats{
		EventsProcessed: w.eventsProcessed.Load(),
		EventsDropped:   w.eventsDropped.Load(),
		EventsFailed:    w.eventsFailed.Load(),
		CircuitState:    w.circuitBreaker.State(),
	}
}

// WorkerStats contains runtime statistics
type WorkerStats struct {
	EventsProcessed uint64
	EventsDropped   uint64
	EventsFailed    uint64
	CircuitState    string
}

// CircuitBreaker methods

// Allow checks if the circuit allows the operation
func (cb *CircuitBreaker) Allow() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	
	switch cb.state {
	case "open":
		// Check if we should transition to half-open
		if time.Since(cb.lastFailureTime) > cb.config.RecoveryTimeout {
			cb.state = "half-open"
			cb.successCount = 0
			return true
		}
		return false
		
	case "half-open":
		// Allow limited events in half-open state
		return cb.successCount < cb.config.HalfOpenMaxEvents
		
	default: // closed
		return true
	}
}

// RecordSuccess records a successful operation
func (cb *CircuitBreaker) RecordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	
	cb.failures = 0
	
	if cb.state == "half-open" {
		cb.successCount++
		if cb.successCount >= cb.config.HalfOpenMaxEvents {
			cb.state = "closed"
		}
	}
}

// RecordFailure records a failed operation
func (cb *CircuitBreaker) RecordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	
	cb.failures++
	cb.lastFailureTime = time.Now()
	
	if cb.failures >= cb.config.FailureThreshold {
		cb.state = "open"
	}
	
	if cb.state == "half-open" {
		cb.state = "open"
	}
}

// State returns the current circuit breaker state
func (cb *CircuitBreaker) State() string {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return cb.state
}

// Reset resets the circuit breaker
func (cb *CircuitBreaker) Reset() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	
	cb.state = "closed"
	cb.failures = 0
	cb.successCount = 0
}