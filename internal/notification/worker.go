package notification

import (
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"text/template"
	"time"

	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/events"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// NotificationWorker implements EventConsumer to process error events
type NotificationWorker struct {
	service        *Service
	templates      map[string]*template.Template
	templateMu     sync.RWMutex
	config         *WorkerConfig
	circuitBreaker *CircuitBreaker

	// Metrics
	eventsProcessed atomic.Uint64
	eventsDropped   atomic.Uint64
	eventsFailed    atomic.Uint64

	log logger.Logger
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
	FailureThreshold  int
	RecoveryTimeout   time.Duration
	HalfOpenMaxEvents int
}

// DefaultWorkerConfig returns default configuration
func DefaultWorkerConfig() *WorkerConfig {
	return &WorkerConfig{
		BatchingEnabled:   false, // Start with single event processing
		BatchSize:         DefaultBatchSize,
		BatchTimeout:      DefaultBatchTimeoutMs * time.Millisecond,
		FailureThreshold:  DefaultFailureThreshold,
		RecoveryTimeout:   DefaultRecoveryTimeoutSeconds * time.Second,
		HalfOpenMaxEvents: DefaultHalfOpenMaxEvents,
	}
}

// CircuitBreaker implements the circuit breaker pattern
type CircuitBreaker struct {
	mu              sync.Mutex
	state           string // Uses circuitState* constants from constants.go
	failures        int
	lastFailureTime time.Time
	successCount    int
	config          *WorkerConfig
	log             logger.Logger
}

// NewNotificationWorker creates a new notification worker
func NewNotificationWorker(service *Service, config *WorkerConfig) (*NotificationWorker, error) {
	if service == nil {
		return nil, fmt.Errorf("notification service is required")
	}

	if config == nil {
		config = DefaultWorkerConfig()
	}

	log := logger.Global().Module("notifications")
	worker := &NotificationWorker{
		service:   service,
		templates: make(map[string]*template.Template),
		config:    config,
		circuitBreaker: &CircuitBreaker{
			state:  circuitStateClosed,
			config: config,
			log:    log,
		},
		log: log,
	}

	// Pre-compile templates
	if err := worker.initTemplates(); err != nil {
		return nil, errors.New(err).
			Component("notification").
			Category(errors.CategoryConfiguration).
			Context("operation", "init_templates").
			Build()
	}

	// Log worker initialization
	log.Info("notification worker initialized",
		logger.Int("batch_size", config.BatchSize),
		logger.Duration("batch_timeout", config.BatchTimeout),
		logger.Int("failure_threshold", config.FailureThreshold),
		logger.Duration("recovery_timeout", config.RecoveryTimeout),
		logger.Int("half_open_max_events", config.HalfOpenMaxEvents),
		logger.Bool("debug", config.Debug))

	return worker, nil
}

// initTemplates pre-compiles notification templates
func (w *NotificationWorker) initTemplates() error {
	templates := map[string]string{
		"error_critical":     "Critical {{.Category}} error in {{.Component}}: {{.Message}}",
		"error_high":         "{{.Category}} error in {{.Component}}: {{.Message}}",
		"error_medium":       "{{.Component}} reported: {{.Message}}",
		"error_low":          "Minor issue in {{.Component}}",
		"error_with_context": "{{.Component}} error ({{.Category}}): {{.Message}}\nContext: {{.Context}}",
	}

	for name, tmplStr := range templates {
		tmpl, err := template.New(name).Parse(tmplStr)
		if err != nil {
			return fmt.Errorf("failed to parse template %s: %w", name, err)
		}
		w.templates[name] = tmpl
	}

	if w.config.Debug {
		w.log.Debug("notification templates initialized",
			logger.Int("template_count", len(templates)))
	}

	return nil
}

// Name returns the consumer name
func (w *NotificationWorker) Name() string {
	return "notification-worker"
}

// ProcessEvent processes a single error event
func (w *NotificationWorker) ProcessEvent(event events.ErrorEvent) error {
	w.logEventProcessingStart(event)

	if !w.circuitBreaker.Allow() {
		return w.handleCircuitBreakerOpen(event)
	}

	priority := w.determineEventPriority(event)
	if w.shouldSkipLowPriority(event, priority) {
		return nil
	}

	notification, err := w.createEventNotification(event, priority)
	if err != nil {
		return w.handleNotificationCreationError(event, err)
	}

	w.recordNotificationSuccess(notification, event, priority)
	return nil
}

// logEventProcessingStart logs debug info when processing starts.
func (w *NotificationWorker) logEventProcessingStart(event events.ErrorEvent) {
	if w.config.Debug {
		w.log.Debug("processing error event",
			logger.String("component", event.GetComponent()),
			logger.String("category", event.GetCategory()),
			logger.Int("error_message_length", len(event.GetMessage())),
			logger.Any("context", scrubContextMap(event.GetContext())))
	}
}

// handleCircuitBreakerOpen handles the case when circuit breaker is open.
func (w *NotificationWorker) handleCircuitBreakerOpen(event events.ErrorEvent) error {
	w.eventsDropped.Add(1)
	w.log.Debug("circuit breaker open, dropping event",
		logger.String("component", event.GetComponent()),
		logger.String("category", event.GetCategory()))
	return nil
}

// determineEventPriority determines priority from category and explicit priority if available.
func (w *NotificationWorker) determineEventPriority(event events.ErrorEvent) Priority {
	explicitPriority := ""
	if enhancedErr, ok := event.(*errors.EnhancedError); ok {
		explicitPriority = enhancedErr.GetPriority()
	}
	return getNotificationPriority(event.GetCategory(), explicitPriority)
}

// shouldSkipLowPriority returns true if low priority events should be skipped.
func (w *NotificationWorker) shouldSkipLowPriority(event events.ErrorEvent, priority Priority) bool {
	if priority != PriorityLow {
		return false
	}
	w.log.Debug("skipping low priority error notification",
		logger.String("category", event.GetCategory()),
		logger.String("priority", string(priority)),
		logger.String("component", event.GetComponent()))
	return true
}

// createEventNotification creates a notification for the event.
func (w *NotificationWorker) createEventNotification(event events.ErrorEvent, priority Priority) (*Notification, error) {
	title := w.generateTitle(event, priority)
	message := w.generateMessage(event, priority)

	return w.service.CreateWithComponent(
		TypeError,
		priority,
		title,
		message,
		event.GetComponent(),
	)
}

// handleNotificationCreationError handles errors during notification creation.
func (w *NotificationWorker) handleNotificationCreationError(event events.ErrorEvent, err error) error {
	w.eventsFailed.Add(1)
	w.circuitBreaker.RecordFailure()

	// Rate limit errors are expected - just track and return
	var enhErr *errors.EnhancedError
	if errors.As(err, &enhErr) && enhErr.GetMessage() == "rate limit exceeded" {
		w.eventsDropped.Add(1)
		return nil
	}

	w.log.Error("failed to create notification",
		logger.Error(err),
		logger.String("component", event.GetComponent()),
		logger.String("category", event.GetCategory()))
	return err
}

// recordNotificationSuccess records success and enriches notification metadata.
func (w *NotificationWorker) recordNotificationSuccess(notification *Notification, event events.ErrorEvent, priority Priority) {
	w.eventsProcessed.Add(1)
	w.circuitBreaker.RecordSuccess()

	w.enrichNotificationWithContext(notification, event, priority)
	w.logNotificationCreated(notification, event, priority)
}

// enrichNotificationWithContext adds event context to notification metadata.
func (w *NotificationWorker) enrichNotificationWithContext(notification *Notification, event events.ErrorEvent, priority Priority) {
	if notification == nil || event.GetContext() == nil {
		return
	}

	for k, v := range event.GetContext() {
		notification.WithMetadata(k, v)
	}

	if priority != PriorityCritical {
		notification.WithExpiry(DefaultDetectionExpiry)
	}
}

// logNotificationCreated logs debug info after successful notification creation.
func (w *NotificationWorker) logNotificationCreated(notification *Notification, event events.ErrorEvent, priority Priority) {
	if w.config.Debug {
		w.log.Debug("created error notification",
			logger.String("notification_id", notification.ID),
			logger.String("component", event.GetComponent()),
			logger.String("category", event.GetCategory()),
			logger.String("priority", string(priority)),
			logger.Int("metadata_count", len(event.GetContext())),
			logger.Any("scrubbed_context", scrubContextMap(event.GetContext())))
	}
}

// eventKey groups events by component, category, and priority.
type eventKey struct {
	component string
	category  string
	priority  Priority
}

// ProcessBatch processes multiple events at once with aggregation
func (w *NotificationWorker) ProcessBatch(errorEvents []events.ErrorEvent) error {
	if len(errorEvents) == 0 {
		return nil
	}

	if w.config.Debug {
		w.log.Debug("processing event batch",
			logger.Int("batch_size", len(errorEvents)))
	}

	eventGroups := w.groupEventsByKey(errorEvents)
	aggregatedErrors, successCount := w.processEventGroups(eventGroups)

	w.log.Debug("processed event batch with aggregation",
		logger.Int("total", len(errorEvents)),
		logger.Int("groups", len(eventGroups)),
		logger.Int("success", successCount),
		logger.Int("failed", len(errorEvents)-successCount))

	if len(aggregatedErrors) > 0 {
		return errors.Join(aggregatedErrors...)
	}
	return nil
}

// groupEventsByKey groups events by component, category, and priority, skipping low priority.
func (w *NotificationWorker) groupEventsByKey(errorEvents []events.ErrorEvent) map[eventKey][]events.ErrorEvent {
	eventGroups := make(map[eventKey][]events.ErrorEvent)

	for _, event := range errorEvents {
		explicitPriority := ""
		if enhancedErr, ok := event.(*errors.EnhancedError); ok {
			explicitPriority = enhancedErr.GetPriority()
		}
		priority := getNotificationPriority(event.GetCategory(), explicitPriority)

		if priority == PriorityLow {
			continue
		}

		key := eventKey{
			component: event.GetComponent(),
			category:  event.GetCategory(),
			priority:  priority,
		}
		eventGroups[key] = append(eventGroups[key], event)
	}

	return eventGroups
}

// processEventGroups processes each event group and returns aggregated errors and success count.
func (w *NotificationWorker) processEventGroups(eventGroups map[eventKey][]events.ErrorEvent) (aggregatedErrors []error, successCount int) {
	for key, groupEvents := range eventGroups {
		err := w.processEventGroup(key, groupEvents)
		if err != nil {
			aggregatedErrors = append(aggregatedErrors, err)
		} else {
			successCount += len(groupEvents)
		}
	}

	return aggregatedErrors, successCount
}

// processEventGroup processes a single group of events with the same key.
func (w *NotificationWorker) processEventGroup(key eventKey, groupEvents []events.ErrorEvent) error {
	eventCount := len(groupEvents)

	if !w.circuitBreaker.Allow() {
		w.eventsDropped.Add(uint64(eventCount))
		w.log.Debug("circuit breaker open, dropping event group",
			logger.String("component", key.component),
			logger.String("category", key.category),
			logger.Int("count", eventCount))
		return nil
	}

	title := fmt.Sprintf("%s (%d occurrences)", w.generateTitle(groupEvents[0], key.priority), eventCount)
	message := w.buildAggregatedMessage(key, groupEvents)

	notification, err := w.service.CreateWithComponent(
		TypeError,
		key.priority,
		title,
		message,
		key.component,
	)

	if err != nil {
		w.eventsFailed.Add(uint64(eventCount))
		w.circuitBreaker.RecordFailure()

		var enhErr *errors.EnhancedError
		if errors.As(err, &enhErr) && enhErr.GetMessage() == "rate limit exceeded" {
			w.eventsDropped.Add(uint64(eventCount))
		}
		return err
	}

	w.eventsProcessed.Add(uint64(eventCount))
	w.circuitBreaker.RecordSuccess()
	w.addAggregatedMetadata(notification, groupEvents)
	return nil
}

// buildAggregatedMessage builds an aggregated message from multiple events.
func (w *NotificationWorker) buildAggregatedMessage(key eventKey, groupEvents []events.ErrorEvent) string {
	var messageBuilder strings.Builder
	messageBuilder.WriteString(fmt.Sprintf("Multiple %s errors in %s:\n", key.category, key.component))

	uniqueMessages := make(map[string]bool)
	for _, event := range groupEvents {
		msg := event.GetMessage()
		if len(uniqueMessages) >= DefaultMaxSummaryMessages {
			messageBuilder.WriteString(fmt.Sprintf("\n... and %d more errors", len(groupEvents)-len(uniqueMessages)))
			break
		}
		if !uniqueMessages[msg] {
			uniqueMessages[msg] = true
			messageBuilder.WriteString("\n• ")
			messageBuilder.WriteString(w.truncateMessage(msg, DefaultTruncateLength))
		}
	}

	return messageBuilder.String()
}

// addAggregatedMetadata adds aggregation metadata to a notification.
func (w *NotificationWorker) addAggregatedMetadata(notification *Notification, groupEvents []events.ErrorEvent) {
	if notification == nil {
		return
	}
	notification.WithMetadata("error_count", len(groupEvents))
	notification.WithMetadata("first_occurrence", groupEvents[0].GetTimestamp())
	notification.WithMetadata("last_occurrence", groupEvents[len(groupEvents)-1].GetTimestamp())
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
		return w.truncateMessage(event.GetMessage(), DefaultMessageTruncateLength)
	}

	// Prepare template data
	data := map[string]any{
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
		w.log.Error("failed to execute notification template",
			logger.String("template", templateName),
			logger.Error(err))
		// Fallback to simple message
		return w.truncateMessage(event.GetMessage(), DefaultMessageTruncateLength)
	}

	// Truncate if necessary
	result := buf.String()
	const maxLength = DefaultMessageTruncateLength
	if len(result) > maxLength {
		result = result[:maxLength-3] + "..."
	}

	return result
}

// formatContext formats the error context for display
func formatContext(ctx map[string]any) string {
	if len(ctx) == 0 {
		return ""
	}

	parts := make([]string, 0, len(ctx))
	for k, v := range ctx {
		parts = append(parts, fmt.Sprintf("%s=%v", k, v))
	}

	// Sort for consistent output
	slices.Sort(parts)
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
	case circuitStateOpen:
		// Check if we should transition to half-open
		if time.Since(cb.lastFailureTime) > cb.config.RecoveryTimeout {
			oldState := cb.state
			cb.state = circuitStateHalfOpen
			cb.successCount = 0
			if cb.config.Debug && cb.log != nil {
				cb.log.Debug("circuit breaker state transition",
					logger.String("from", oldState),
					logger.String("to", cb.state),
					logger.Duration("recovery_timeout", cb.config.RecoveryTimeout))
			}
			return true
		}
		return false

	case circuitStateHalfOpen:
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

	if cb.state == circuitStateHalfOpen {
		cb.successCount++
		if cb.successCount >= cb.config.HalfOpenMaxEvents {
			oldState := cb.state
			cb.state = circuitStateClosed
			if cb.config.Debug && cb.log != nil {
				cb.log.Debug("circuit breaker state transition",
					logger.String("from", oldState),
					logger.String("to", cb.state),
					logger.String("reason", "successful operations threshold reached"))
			}
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
		oldState := cb.state
		cb.state = circuitStateOpen
		if cb.config.Debug && cb.log != nil && oldState != circuitStateOpen {
			cb.log.Debug("circuit breaker state transition",
				logger.String("from", oldState),
				logger.String("to", cb.state),
				logger.Int("failures", cb.failures),
				logger.Int("threshold", cb.config.FailureThreshold))
		}
	}

	if cb.state == circuitStateHalfOpen {
		oldState := cb.state
		cb.state = circuitStateOpen
		if cb.config.Debug && cb.log != nil {
			cb.log.Debug("circuit breaker state transition",
				logger.String("from", oldState),
				logger.String("to", cb.state),
				logger.String("reason", "failure in half-open state"))
		}
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

	cb.state = circuitStateClosed
	cb.failures = 0
	cb.successCount = 0
}
