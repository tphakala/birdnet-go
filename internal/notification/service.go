package notification

import (
	"context"
	"fmt"
	"sync"
	"time"

	"log/slog"

	"github.com/tphakala/birdnet-go/internal/errors"
)

// Subscriber represents a notification subscriber
type Subscriber struct {
	ch     chan *Notification
	ctx    context.Context
	cancel context.CancelFunc
}

// Service manages notifications and provides rate limiting
type Service struct {
	store         NotificationStore
	subscribers   []*Subscriber
	subscribersMu sync.RWMutex
	rateLimiter   *RateLimiter
	cleanupTicker *time.Ticker
	ctx           context.Context
	cancel        context.CancelFunc
	wg            sync.WaitGroup
	logger        *slog.Logger
	config        *ServiceConfig
	telemetry     *NotificationTelemetry
}

// ServiceConfig holds the complete configuration for the notification service.
// This is the primary configuration struct used throughout the notification system.
// It includes all settings needed for:
// - Debug logging control
// - Notification storage limits
// - Automatic cleanup of expired notifications
// - Rate limiting to prevent notification spam
//
// Use this struct when initializing the notification service via NewService().
type ServiceConfig struct {
	// Debug enables debug logging for the service
	Debug bool
	// MaxNotifications is the maximum number of notifications to keep in memory
	MaxNotifications int
	// CleanupInterval is how often to clean up expired notifications
	CleanupInterval time.Duration
	// RateLimitWindow is the time window for rate limiting
	RateLimitWindow time.Duration
	// RateLimitMaxEvents is the maximum number of events per window
	RateLimitMaxEvents int
}

// DefaultServiceConfig returns a default configuration
func DefaultServiceConfig() *ServiceConfig {
	return &ServiceConfig{
		MaxNotifications:   1000,
		CleanupInterval:    5 * time.Minute,
		RateLimitWindow:    1 * time.Minute,
		RateLimitMaxEvents: 100,
	}
}

// NewService creates a new notification service
func NewService(config *ServiceConfig) *Service {
	if config == nil {
		config = DefaultServiceConfig()
	}

	ctx, cancel := context.WithCancel(context.Background())

	service := &Service{
		store:         NewInMemoryStore(config.MaxNotifications),
		subscribers:   make([]*Subscriber, 0),
		rateLimiter:   NewRateLimiter(config.RateLimitWindow, config.RateLimitMaxEvents),
		cleanupTicker: time.NewTicker(config.CleanupInterval),
		ctx:           ctx,
		cancel:        cancel,
		logger:        getFileLogger(config.Debug),
		config:        config,
	}

	// Log service initialization
	service.logger.Info("notification service initialized",
		"max_notifications", config.MaxNotifications,
		"cleanup_interval", config.CleanupInterval,
		"rate_limit_window", config.RateLimitWindow,
		"rate_limit_max_events", config.RateLimitMaxEvents,
		"debug", config.Debug)

	// Start background cleanup
	service.wg.Add(1)
	go service.cleanupLoop()

	service.logger.Info("notification cleanup worker started",
		"interval", config.CleanupInterval)

	return service
}

// SetTelemetry sets the telemetry integration for the service.
// This must be called after service creation to enable telemetry reporting.
func (s *Service) SetTelemetry(telemetry *NotificationTelemetry) {
	s.telemetry = telemetry
	s.logger.Info("telemetry integration enabled for notification service",
		"enabled", telemetry != nil && telemetry.IsEnabled())
}

// GetTelemetry returns the telemetry integration, or nil if not set.
func (s *Service) GetTelemetry() *NotificationTelemetry {
	return s.telemetry
}

// Create adds a new notification to the system
func (s *Service) Create(notifType Type, priority Priority, title, message string) (*Notification, error) {
	// Check rate limit
	if !s.rateLimiter.Allow() {
		if s.config.Debug {
			s.logger.Debug("notification rate limit exceeded",
				"type", notifType,
				"priority", priority,
				"title_length", len(title))
		}
		return nil, errors.Newf("rate limit exceeded").
			Component("notification").
			Category(errors.CategorySystem).
			Build()
	}

	notification := NewNotification(notifType, priority, title, message)

	if s.config.Debug {
		s.logger.Debug("creating notification",
			"id", notification.ID,
			"type", notifType,
			"priority", priority,
			"title_length", len(title),
			"message_length", len(message))
	}

	// Save to store
	if err := s.store.Save(notification); err != nil {
		return nil, errors.New(err).
			Component("notification").
			Category(errors.CategorySystem).
			Context("operation", "save_notification").
			Build()
	}

	// Broadcast to subscribers
	s.broadcast(notification)

	if s.config.Debug {
		s.logger.Debug("notification created and broadcast",
			"id", notification.ID,
			"subscriber_count", len(s.subscribers))
	}

	return notification, nil
}

// CreateWithComponent creates a notification with a specific component
func (s *Service) CreateWithComponent(notifType Type, priority Priority, title, message, component string) (*Notification, error) {
	// Check rate limit
	if !s.rateLimiter.Allow() {
		return nil, errors.Newf("rate limit exceeded").
			Component("notification").
			Category(errors.CategorySystem).
			Build()
	}

	notification := NewNotification(notifType, priority, title, message).
		WithComponent(component)

	// Save to store
	if err := s.store.Save(notification); err != nil {
		return nil, errors.New(err).
			Component("notification").
			Category(errors.CategorySystem).
			Context("operation", "save_notification").
			Build()
	}

	// Broadcast to subscribers
	s.broadcast(notification)

	return notification, nil
}

// Get retrieves a notification by ID
func (s *Service) Get(id string) (*Notification, error) {
	return s.store.Get(id)
}

// List returns notifications based on filter options
func (s *Service) List(filter *FilterOptions) ([]*Notification, error) {
	return s.store.List(filter)
}

// MarkAsRead updates a notification's status to read
func (s *Service) MarkAsRead(id string) error {
	if id == "" {
		return errors.Newf("notification ID cannot be empty").
			Component("notification").
			Category(errors.CategoryValidation).
			Build()
	}

	notification, err := s.store.Get(id)
	if err != nil {
		return err
	}

	notification.MarkAsRead()
	return s.store.Update(notification)
}

// MarkAsAcknowledged updates a notification's status to acknowledged
func (s *Service) MarkAsAcknowledged(id string) error {
	if id == "" {
		return errors.Newf("notification ID cannot be empty").
			Component("notification").
			Category(errors.CategoryValidation).
			Build()
	}

	notification, err := s.store.Get(id)
	if err != nil {
		return err
	}

	notification.MarkAsAcknowledged()
	return s.store.Update(notification)
}

// Delete removes a notification
func (s *Service) Delete(id string) error {
	if id == "" {
		return errors.Newf("notification ID cannot be empty").
			Component("notification").
			Category(errors.CategoryValidation).
			Build()
	}

	return s.store.Delete(id)
}

// Subscribe creates a channel to receive real-time notifications.
//
// Returns:
//   - A read-only channel that will receive notifications
//   - A context that is cancelled when the subscription is terminated
//
// The subscriber is responsible for:
//  1. Monitoring the returned context's Done() channel to detect cancellation
//  2. Stopping consumption of notifications when the context is cancelled
//  3. NOT closing the returned channel (it's managed by the service)
//
// Example usage:
//
//	ch, ctx := service.Subscribe()
//	go func() {
//		for {
//			select {
//			case notif := <-ch:
//				if notif == nil {
//					return // Channel was closed by service shutdown
//				}
//				// Process notification
//			case <-ctx.Done():
//				return // Subscription was cancelled
//			}
//		}
//	}()
//
// To unsubscribe, call service.Unsubscribe(ch)
//
// Note: The service automatically cleans up cancelled subscribers during
// broadcast operations to prevent memory leaks.
func (s *Service) Subscribe() (<-chan *Notification, context.Context) {
	s.subscribersMu.Lock()
	defer s.subscribersMu.Unlock()

	ctx, cancel := context.WithCancel(s.ctx)
	sub := &Subscriber{
		ch:     make(chan *Notification, 10),
		ctx:    ctx,
		cancel: cancel,
	}
	s.subscribers = append(s.subscribers, sub)
	
	if s.config.Debug {
		s.logger.Debug("new subscriber added",
			"total_subscribers", len(s.subscribers))
	}
	
	return sub.ch, ctx
}

// Unsubscribe removes a notification channel
// It cancels the subscriber's context but does not close the channel
// The subscriber should close the channel when done reading
func (s *Service) Unsubscribe(ch <-chan *Notification) {
	s.subscribersMu.Lock()
	defer s.subscribersMu.Unlock()

	for i, subscriber := range s.subscribers {
		if subscriber.ch == ch {
			subscriber.cancel()
			s.subscribers = append(s.subscribers[:i], s.subscribers[i+1:]...)
			
			if s.config.Debug {
				s.logger.Debug("subscriber removed",
					"remaining_subscribers", len(s.subscribers))
			}
			
			break
		}
	}
}

// GetUnreadCount returns the number of unread notifications
func (s *Service) GetUnreadCount() (int, error) {
	return s.store.GetUnreadCount()
}

// CreateErrorNotification creates a notification from an error
func (s *Service) CreateErrorNotification(err error) (*Notification, error) {
	// Extract error details
	var title, message, component string
	var priority Priority

	// Check if it's an enhanced error
	var enhancedErr *errors.EnhancedError
	if errors.As(err, &enhancedErr) {
		component = enhancedErr.GetComponent()
		category := enhancedErr.GetCategory()
		message = enhancedErr.Error()

		// Determine priority based on category
		switch category {
		case string(errors.CategorySystem), string(errors.CategoryDatabase):
			priority = PriorityCritical
			title = "Critical System Error"
		case string(errors.CategoryNetwork), string(errors.CategoryHTTP):
			priority = PriorityHigh
			title = fmt.Sprintf("%s Error", category)
		case string(errors.CategoryImageProvider), string(errors.CategoryImageFetch):
			priority = PriorityLow
			title = "Image Provider Notice"
		default:
			priority = PriorityMedium
			title = "Application Error"
		}
	} else {
		// Fallback for standard errors
		priority = PriorityMedium
		title = "Application Error"
		message = err.Error()
		component = "unknown"
	}

	return s.CreateWithComponent(TypeError, priority, title, message, component)
}

// broadcast sends a notification to all subscribers
func (s *Service) broadcast(notification *Notification) {
	s.subscribersMu.Lock()
	defer s.subscribersMu.Unlock()

	if s.config.Debug && len(s.subscribers) > 0 {
		s.logger.Debug("broadcasting notification",
			"notification_id", notification.ID,
			"type", notification.Type,
			"subscriber_count", len(s.subscribers))
	}

	// Track which subscribers are still active
	activeSubscribers := make([]*Subscriber, 0, len(s.subscribers))
	var successCount, failedCount, cancelledCount int

	for _, sub := range s.subscribers {
		select {
		case <-sub.ctx.Done():
			// Subscriber cancelled, don't add to active list
			cancelledCount++
			continue
		default:
			// Subscriber is still active
			activeSubscribers = append(activeSubscribers, sub)

			// Try to send notification
			select {
			case sub.ch <- notification:
				// Successfully sent
				successCount++
			default:
				// Channel is full, skip this subscriber
				failedCount++
				if s.logger != nil {
					s.logger.Debug("notification channel full, skipping subscriber")
				}
			}
		}
	}

	// Update the subscribers list to only include active ones
	s.subscribers = activeSubscribers

	if s.config.Debug && (successCount > 0 || failedCount > 0 || cancelledCount > 0) {
		s.logger.Debug("broadcast completed",
			"notification_id", notification.ID,
			"success_count", successCount,
			"failed_count", failedCount,
			"cancelled_count", cancelledCount,
			"active_subscribers", len(activeSubscribers))
	}
}

// cleanupLoop periodically removes expired notifications
func (s *Service) cleanupLoop() {
	defer s.wg.Done()

	for {
		select {
		case <-s.cleanupTicker.C:
			if s.config.Debug {
				// Count expired notifications before cleanup
				filter := &FilterOptions{}
				notifications, _ := s.store.List(filter)
				var expiredCount int
				for _, n := range notifications {
					if n.IsExpired() {
						expiredCount++
					}
				}
				if expiredCount > 0 {
					s.logger.Debug("starting notification cleanup",
						"expired_count", expiredCount,
						"total_count", len(notifications))
				}
			}
			
			if err := s.store.DeleteExpired(); err != nil {
				// Log error but don't stop the cleanup loop
				if s.logger != nil {
					s.logger.Error("error cleaning up expired notifications", "error", err)
				}
			} else if s.config.Debug {
				s.logger.Debug("notification cleanup completed")
			}
		case <-s.ctx.Done():
			if s.config.Debug {
				s.logger.Debug("notification cleanup loop shutting down")
			}
			return
		}
	}
}

// Stop gracefully shuts down the notification service
func (s *Service) Stop() {
	s.logger.Info("notification service shutting down")
	
	s.cancel()
	s.cleanupTicker.Stop()
	s.wg.Wait()

	// Cancel all subscriber contexts
	s.subscribersMu.Lock()
	subscriberCount := len(s.subscribers)
	for _, sub := range s.subscribers {
		sub.cancel()
	}
	s.subscribers = nil
	s.subscribersMu.Unlock()
	
	s.logger.Info("notification service stopped",
		"subscribers_cancelled", subscriberCount)
	
	// Close the logger to clean up resources
	if err := CloseLogger(); err != nil {
		// Use fallback logging since our logger might be closed
		slog.Default().Error("failed to close notification logger", "error", err)
	}
}

// RateLimiter provides rate limiting for notifications
type RateLimiter struct {
	window    time.Duration
	maxEvents int
	events    []time.Time
	mu        sync.Mutex
}

// NewRateLimiter creates a new rate limiter
func NewRateLimiter(window time.Duration, maxEvents int) *RateLimiter {
	return &RateLimiter{
		window:    window,
		maxEvents: maxEvents,
		events:    make([]time.Time, 0, maxEvents),
	}
}

// Allow checks if an event is allowed based on rate limits
func (r *RateLimiter) Allow() bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-r.window)

	// Remove old events outside the window by reusing the slice
	validCount := 0
	for _, event := range r.events {
		if event.After(cutoff) {
			r.events[validCount] = event
			validCount++
		}
	}
	r.events = r.events[:validCount]

	// Check if we're at the limit
	if len(r.events) >= r.maxEvents {
		return false
	}

	// Add this event
	r.events = append(r.events, now)
	return true
}

// Reset clears the rate limiter
func (r *RateLimiter) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = nil
}
