package notification

import (
	"fmt"
	"sort"
	"strings"
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
	// Debug enables debug logging for the worker
	Debug bool
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
		logger: getFileLogger(config.Debug),
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

// ProcessBatch processes multiple events at once with aggregation
func (w *NotificationWorker) ProcessBatch(errorEvents []events.ErrorEvent) error {
	if len(errorEvents) == 0 {
		return nil
	}
	
	// Group events by component and category for aggregation
	type eventKey struct {
		component string
		category  string
		priority  Priority
	}
	
	eventGroups := make(map[eventKey][]events.ErrorEvent)
	
	// Group events by key
	for _, event := range errorEvents {
		priority := getNotificationPriority(event.GetCategory())
		
		// Skip low priority events
		if priority != PriorityHigh && priority != PriorityCritical {
			continue
		}
		
		key := eventKey{
			component: event.GetComponent(),
			category:  event.GetCategory(),
			priority:  priority,
		}
		eventGroups[key] = append(eventGroups[key], event)
	}
	
	// Process each group
	var aggregatedErrors []error
	successCount := 0
	
	for key, events := range eventGroups {
		// Check circuit breaker once per group
		if !w.circuitBreaker.Allow() {
			w.eventsDropped.Add(uint64(len(events)))
			w.logger.Debug("circuit breaker open, dropping event group",
				"component", key.component,
				"category", key.category,
				"count", len(events),
			)
			continue
		}
		
		// Create aggregated notification
		title := fmt.Sprintf("%s (%d occurrences)", 
			w.generateTitle(events[0], key.priority), len(events))
		
		// Aggregate messages
		var messageBuilder strings.Builder
		messageBuilder.WriteString(fmt.Sprintf("Multiple %s errors in %s:\n", 
			key.category, key.component))
		
		// Include up to 5 unique messages
		uniqueMessages := make(map[string]bool)
		for _, event := range events {
			msg := event.GetMessage()
			if len(uniqueMessages) >= 5 {
				messageBuilder.WriteString(fmt.Sprintf("\n... and %d more errors", 
					len(events)-len(uniqueMessages)))
				break
			}
			if !uniqueMessages[msg] {
				uniqueMessages[msg] = true
				messageBuilder.WriteString("\n• ")
				messageBuilder.WriteString(w.truncateMessage(msg, 100))
			}
		}
		
		// Create single notification for the group
		notification, err := w.service.CreateWithComponent(
			TypeError,
			key.priority,
			title,
			messageBuilder.String(),
			key.component,
		)
		
		if err != nil {
			w.eventsFailed.Add(uint64(len(events)))
			w.circuitBreaker.RecordFailure()
			aggregatedErrors = append(aggregatedErrors, err)
			
			// Check for rate limit
			var enhErr *errors.EnhancedError
			if errors.As(err, &enhErr) && enhErr.GetMessage() == "rate limit exceeded" {
				w.eventsDropped.Add(uint64(len(events)))
			}
		} else {
			w.eventsProcessed.Add(uint64(len(events)))
			w.circuitBreaker.RecordSuccess()
			successCount += len(events)
			
			// Add aggregated context
			if notification != nil {
				notification.WithMetadata("error_count", len(events))
				notification.WithMetadata("first_occurrence", events[0].GetTimestamp())
				notification.WithMetadata("last_occurrence", events[len(events)-1].GetTimestamp())
			}
		}
	}
	
	w.logger.Debug("processed event batch with aggregation",
		"total", len(errorEvents),
		"groups", len(eventGroups),
		"success", successCount,
		"failed", len(errorEvents)-successCount,
	)
	
	// Return aggregated errors if any
	if len(aggregatedErrors) > 0 {
		return errors.Join(aggregatedErrors...)
	}
	
	return nil
}

// truncateMessage truncates a message to the specified length
func (w *NotificationWorker) truncateMessage(message string, maxLength int) string {
	if len(message) <= maxLength {
		return message
	}
	return message[:maxLength-3] + "..."
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

// generateMessage generates a notification message based on the event using pre-compiled templates
func (w *NotificationWorker) generateMessage(event events.ErrorEvent, priority Priority) string {
	// Select appropriate template based on priority
	var templateName string
	switch priority {
	case PriorityCritical:
		templateName = "error_critical"
	case PriorityHigh:
		templateName = "error_high"
	case PriorityMedium:
		templateName = "error_medium"
	case PriorityLow:
		templateName = "error_low"
	default:
		templateName = "error_medium"
	}
	
	// Get template
	w.templateMu.RLock()
	tmpl, exists := w.templates[templateName]
	w.templateMu.RUnlock()
	
	if !exists {
		// Fallback to simple message if template not found
		return w.truncateMessage(event.GetMessage(), 500)
	}
	
	// Prepare template data
	data := map[string]interface{}{
		"Component": event.GetComponent(),
		"Category":  event.GetCategory(),
		"Message":   event.GetMessage(),
	}
	
	// Add context if available
	if ctx := event.GetContext(); len(ctx) > 0 {
		data["Context"] = formatContext(ctx)
	}
	
	// Execute template
	var buf strings.Builder
	if err := tmpl.Execute(&buf, data); err != nil {
		w.logger.Error("failed to execute notification template",
			"template", templateName,
			"error", err,
		)
		// Fallback to simple message
		return w.truncateMessage(event.GetMessage(), 500)
	}
	
	// Truncate if necessary
	result := buf.String()
	const maxLength = 500
	if len(result) > maxLength {
		result = result[:maxLength-3] + "..."
	}
	
	return result
}

// formatContext formats the error context for display
func formatContext(ctx map[string]interface{}) string {
	if len(ctx) == 0 {
		return ""
	}
	
	parts := make([]string, 0, len(ctx))
	for k, v := range ctx {
		parts = append(parts, fmt.Sprintf("%s=%v", k, v))
	}
	
	// Sort for consistent output
	sort.Strings(parts)
	return strings.Join(parts, ", ")
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