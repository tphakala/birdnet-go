// Package notification provides a system for managing and broadcasting notifications
// throughout the BirdNET-Go application. It handles system alerts, errors, and
// important detection events.
package notification

import (
	"fmt"
	"reflect"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/tphakala/birdnet-go/internal/errors"
)

// Type represents the category of a notification
type Type string

const (
	// TypeError indicates a system error notification
	TypeError Type = "error"
	// TypeWarning indicates a warning notification
	TypeWarning Type = "warning"
	// TypeInfo indicates an informational notification
	TypeInfo Type = "info"
	// TypeDetection indicates a bird detection notification
	TypeDetection Type = "detection"
	// TypeSystem indicates a system status notification
	TypeSystem Type = "system"
)

// Sentinel errors for notification operations
var (
	ErrNotificationNotFound = errors.Newf("notification not found").Component("notification").Category(errors.CategoryNotFound).Build()
)

// Priority represents the urgency level of a notification
type Priority string

const (
	// PriorityCritical indicates urgent attention required
	PriorityCritical Priority = "critical"
	// PriorityHigh indicates important but not urgent
	PriorityHigh Priority = "high"
	// PriorityMedium indicates normal priority
	PriorityMedium Priority = "medium"
	// PriorityLow indicates low priority/informational
	PriorityLow Priority = "low"
)

// Status represents the read state of a notification
type Status string

const (
	// StatusUnread indicates the notification hasn't been seen
	StatusUnread Status = "unread"
	// StatusRead indicates the notification has been seen
	StatusRead Status = "read"
	// StatusAcknowledged indicates the user has acted on the notification
	StatusAcknowledged Status = "acknowledged"
)

// Metadata key constants for common notification metadata fields
const (
	// MetadataKeyIsToast identifies toast notifications in metadata
	MetadataKeyIsToast = "isToast"
)

// isToastNotification checks if a notification is a toast notification
// by examining its metadata for the isToast flag
func isToastNotification(notif *Notification) bool {
	if notif == nil || notif.Metadata == nil {
		return false
	}
	isToast, ok := notif.Metadata[MetadataKeyIsToast].(bool)
	return ok && isToast
}

// Notification represents a single notification event
type Notification struct {
	// ID is the unique identifier for the notification
	ID string `json:"id"`
	// Type categorizes the notification
	Type Type `json:"type"`
	// Priority indicates the urgency level
	Priority Priority `json:"priority"`
	// Status tracks whether the notification has been read
	Status Status `json:"status"`
	// Title is a short summary of the notification
	Title string `json:"title"`
	// Message provides detailed information
	Message string `json:"message"`
	// Component identifies the source component (e.g., "database", "audio", "birdweather")
	Component string `json:"component,omitempty"`
	// Timestamp indicates when the notification was created
	Timestamp time.Time `json:"timestamp"`
	// Metadata contains additional context-specific data
	Metadata map[string]any `json:"metadata,omitempty"`
	// ExpiresAt indicates when the notification should be auto-removed (optional)
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

// NewNotification creates a new notification with a unique ID and timestamp
func NewNotification(notifType Type, priority Priority, title, message string) *Notification {
	return &Notification{
		ID:        uuid.New().String(),
		Type:      notifType,
		Priority:  priority,
		Status:    StatusUnread,
		Title:     title,
		Message:   message,
		Timestamp: time.Now(),
		Metadata:  make(map[string]any),
	}
}

// WithComponent sets the component field and returns the notification for chaining
func (n *Notification) WithComponent(component string) *Notification {
	n.Component = component
	return n
}

// WithMetadata adds metadata and returns the notification for chaining
func (n *Notification) WithMetadata(key string, value any) *Notification {
	if n.Metadata == nil {
		n.Metadata = make(map[string]any)
	}
	n.Metadata[key] = value
	return n
}

// WithExpiry sets the expiration time and returns the notification for chaining
func (n *Notification) WithExpiry(duration time.Duration) *Notification {
	expiresAt := time.Now().Add(duration)
	n.ExpiresAt = &expiresAt
	return n
}

// IsExpired checks if the notification has expired
func (n *Notification) IsExpired() bool {
	if n.ExpiresAt == nil {
		return false
	}
	return time.Now().After(*n.ExpiresAt)
}

// MarkAsRead updates the notification status to read
func (n *Notification) MarkAsRead() {
	n.Status = StatusRead
}

// MarkAsAcknowledged updates the notification status to acknowledged
func (n *Notification) MarkAsAcknowledged() {
	n.Status = StatusAcknowledged
}

// Clone creates a deep copy of the notification, including the Metadata map.
// This is used to safely broadcast notifications to multiple subscribers
// without risk of concurrent map access if the original is modified.
func (n *Notification) Clone() *Notification {
	if n == nil {
		return nil
	}

	clone := &Notification{
		ID:        n.ID,
		Type:      n.Type,
		Priority:  n.Priority,
		Status:    n.Status,
		Title:     n.Title,
		Message:   n.Message,
		Component: n.Component,
		Timestamp: n.Timestamp,
	}

	// Deep copy ExpiresAt
	if n.ExpiresAt != nil {
		expiresAt := *n.ExpiresAt
		clone.ExpiresAt = &expiresAt
	}

	// Deep copy Metadata map to handle nested structures safely
	if n.Metadata != nil {
		clone.Metadata = deepCopyMetadata(n.Metadata)
	}

	return clone
}

// deepCopyMetadata creates a deep copy of the metadata map that preserves Go types.
// This ensures nested maps/slices are fully copied, preventing concurrent access issues
// when the original metadata is modified while being serialized.
func deepCopyMetadata(src map[string]any) map[string]any {
	if src == nil {
		return nil
	}
	return deepCopyValue(src).(map[string]any)
}

// deepCopyValue recursively deep copies a value using reflection to handle any
// map or slice type generically. This ensures all nested collections are properly
// deep-copied, preventing concurrent access issues regardless of the specific type.
// Pointer types and custom structs are copied by reference (not dereferenced).
func deepCopyValue(v any) any {
	if v == nil {
		return nil
	}

	original := reflect.ValueOf(v)

	// We only need to handle maps and slices, as they are reference types
	// that can cause concurrent access issues. Primitives are copied by value,
	// and pointers/structs typically don't need deep copying for our SSE use case.
	switch original.Kind() {
	case reflect.Map:
		// Create a new map of the same type
		newMap := reflect.MakeMap(original.Type())
		iter := original.MapRange()
		for iter.Next() {
			// Recursively copy the value
			copiedValue := deepCopyValue(iter.Value().Interface())

			// If copiedValue is nil, we need a zero value of the correct type
			if copiedValue == nil {
				newMap.SetMapIndex(iter.Key(), reflect.Zero(iter.Value().Type()))
			} else {
				newMap.SetMapIndex(iter.Key(), reflect.ValueOf(copiedValue))
			}
		}
		return newMap.Interface()

	case reflect.Slice:
		// Create a new slice of the same type, length, and capacity
		newSlice := reflect.MakeSlice(original.Type(), original.Len(), original.Cap())
		for i := range original.Len() {
			elem := original.Index(i)
			// Recursively copy the element
			copiedElem := deepCopyValue(elem.Interface())

			if copiedElem == nil {
				newSlice.Index(i).Set(reflect.Zero(elem.Type()))
			} else {
				newSlice.Index(i).Set(reflect.ValueOf(copiedElem))
			}
		}
		return newSlice.Interface()

	default:
		// For primitive types, pointers, structs, etc., return the value as-is.
		// Primitives are value types, and pointers/structs typically don't need
		// deep copying for our SSE serialization use case.
		return v
	}
}

// NotificationStore interface defines methods for persisting notifications
type NotificationStore interface {
	// Save persists a notification
	Save(notification *Notification) error
	// Get retrieves a notification by ID
	Get(id string) (*Notification, error)
	// List returns notifications with optional filtering
	List(filter *FilterOptions) ([]*Notification, error)
	// Update modifies an existing notification
	Update(notification *Notification) error
	// Delete removes a notification
	Delete(id string) error
	// DeleteExpired removes all expired notifications
	DeleteExpired() error
	// GetUnreadCount returns the count of unread notifications
	GetUnreadCount() (int, error)
}

// FilterOptions provides filtering capabilities for listing notifications
type FilterOptions struct {
	// Types filters by notification types
	Types []Type
	// Priorities filters by priority levels
	Priorities []Priority
	// Status filters by read status
	Status []Status
	// Component filters by source component
	Component string
	// Since returns notifications after this time
	Since *time.Time
	// Until returns notifications before this time
	Until *time.Time
	// Limit restricts the number of results
	Limit int
	// Offset for pagination
	Offset int
}

// InMemoryStore provides a thread-safe in-memory notification store
type InMemoryStore struct {
	mu            sync.RWMutex
	notifications map[string]*Notification
	maxSize       int
	unreadCount   int // Track unread count for optimization
}

// NewInMemoryStore creates a new in-memory notification store
func NewInMemoryStore(maxSize int) *InMemoryStore {
	// Validate maxSize
	if maxSize <= 0 {
		maxSize = 1000 // Default to 1000 notifications
	}

	return &InMemoryStore{
		notifications: make(map[string]*Notification),
		maxSize:       maxSize,
	}
}

// Save stores a notification in memory
func (s *InMemoryStore) Save(notification *Notification) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Enforce max size by removing oldest notifications
	if len(s.notifications) >= s.maxSize {
		s.removeOldest()
	}

	s.notifications[notification.ID] = notification

	// Update unread count if this is a new unread notification
	if notification.Status == StatusUnread {
		s.unreadCount++
	}

	return nil
}

// Get retrieves a notification by ID
func (s *InMemoryStore) Get(id string) (*Notification, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if notif, exists := s.notifications[id]; exists {
		// Return a copy to prevent external modifications from affecting the stored notification
		notifCopy := *notif
		return &notifCopy, nil
	}
	return nil, ErrNotificationNotFound
}

// List returns filtered notifications
func (s *InMemoryStore) List(filter *FilterOptions) ([]*Notification, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var results []*Notification
	for _, notif := range s.notifications {
		if s.matchesFilter(notif, filter) {
			// Return copies to prevent external modifications
			notifCopy := *notif
			results = append(results, &notifCopy)
		}
	}

	// Sort by timestamp (newest first)
	sortNotificationsByTime(results)

	// Apply pagination
	if filter != nil {
		if filter.Offset < len(results) {
			results = results[filter.Offset:]
		} else {
			results = []*Notification{}
		}

		if filter.Limit > 0 && len(results) > filter.Limit {
			results = results[:filter.Limit]
		}
	}

	return results, nil
}

// Update modifies an existing notification
func (s *InMemoryStore) Update(notification *Notification) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	oldNotif, exists := s.notifications[notification.ID]
	if !exists {
		return fmt.Errorf("notification not found: %s", notification.ID)
	}

	// Update unread count if status changed
	if oldNotif.Status == StatusUnread && notification.Status != StatusUnread {
		s.unreadCount--
	} else if oldNotif.Status != StatusUnread && notification.Status == StatusUnread {
		s.unreadCount++
	}

	s.notifications[notification.ID] = notification
	return nil
}

// Delete removes a notification
func (s *InMemoryStore) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Check if notification exists and is unread
	if notif, exists := s.notifications[id]; exists {
		if notif.Status == StatusUnread {
			s.unreadCount--
		}
	}

	delete(s.notifications, id)
	return nil
}

// DeleteExpired removes all expired notifications
func (s *InMemoryStore) DeleteExpired() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for id, notif := range s.notifications {
		if notif.IsExpired() {
			if notif.Status == StatusUnread {
				s.unreadCount--
			}
			delete(s.notifications, id)
		}
	}
	return nil
}

// removeOldest removes the oldest notification to make room
func (s *InMemoryStore) removeOldest() {
	var oldestID string
	var oldestTime time.Time

	for id, notif := range s.notifications {
		if oldestID == "" || notif.Timestamp.Before(oldestTime) {
			oldestID = id
			oldestTime = notif.Timestamp
		}
	}

	if oldestID != "" {
		// Update unread count if removing an unread notification
		if notif, exists := s.notifications[oldestID]; exists && notif.Status == StatusUnread {
			s.unreadCount--
		}
		delete(s.notifications, oldestID)
	}
}

// matchesFilter checks if a notification matches the filter criteria
func (s *InMemoryStore) matchesFilter(notif *Notification, filter *FilterOptions) bool {
	// Always exclude toast notifications from lists
	// Toast notifications should only be sent via SSE as ephemeral messages
	if isToastNotification(notif) {
		return false
	}

	if filter == nil {
		return true
	}

	return s.matchesAttributeFilters(notif, filter) && s.matchesTimeFilters(notif, filter)
}

// matchesAttributeFilters checks type, priority, status, and component filters.
func (s *InMemoryStore) matchesAttributeFilters(notif *Notification, filter *FilterOptions) bool {
	if len(filter.Types) > 0 && !slices.Contains(filter.Types, notif.Type) {
		return false
	}
	if len(filter.Priorities) > 0 && !slices.Contains(filter.Priorities, notif.Priority) {
		return false
	}
	if len(filter.Status) > 0 && !slices.Contains(filter.Status, notif.Status) {
		return false
	}
	if filter.Component != "" && notif.Component != filter.Component {
		return false
	}
	return true
}

// matchesTimeFilters checks Since and Until time filters.
func (s *InMemoryStore) matchesTimeFilters(notif *Notification, filter *FilterOptions) bool {
	if filter.Since != nil && notif.Timestamp.Before(*filter.Since) {
		return false
	}
	if filter.Until != nil && notif.Timestamp.After(*filter.Until) {
		return false
	}
	return true
}

// GetUnreadCount returns the count of unread notifications
func (s *InMemoryStore) GetUnreadCount() (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.unreadCount, nil
}

// sortNotificationsByTime sorts notifications by timestamp (newest first)
func sortNotificationsByTime(notifications []*Notification) {
	sort.Slice(notifications, func(i, j int) bool {
		return notifications[i].Timestamp.After(notifications[j].Timestamp)
	})
}

// Config is deprecated and will be removed in a future version.
// Use ServiceConfig in service.go for configuring the notification service.
// ServiceConfig provides all necessary configuration options including:
// - Debug logging control
// - Maximum notifications limit
// - Cleanup intervals for expired notifications
// - Rate limiting settings
//
// Deprecated: This type is kept for backward compatibility only.
// New code should use ServiceConfig instead.
type Config struct {
	// Debug enables debug logging for the notification system
	Debug bool `json:"debug" yaml:"debug"`
	// MaxNotifications sets the maximum number of notifications to keep in memory
	MaxNotifications int `json:"max_notifications" yaml:"max_notifications"`
}
