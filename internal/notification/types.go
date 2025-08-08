// Package notification provides a system for managing and broadcasting notifications
// throughout the BirdNET-Go application. It handles system alerts, errors, and
// important detection events.
package notification

import (
	"maps"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cespare/xxhash/v2"
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

	// DefaultDeduplicationWindow is the default time window for deduplication
	DefaultDeduplicationWindow = 5 * time.Minute
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

// Notification represents a single notification event.
//
// IMPORTANT: Do not directly modify Component, Type, Title, or Message fields after creation
// as this will cause ContentHash to become stale and break deduplication. Use the provided
// builder methods like WithComponent() instead, which properly regenerate the hash.
type Notification struct {
	// ID is the unique identifier for the notification
	ID string `json:"id"`
	// Type categorizes the notification
	// WARNING: Direct mutation breaks deduplication - use builder methods
	Type Type `json:"type"`
	// Priority indicates the urgency level
	Priority Priority `json:"priority"`
	// Status tracks whether the notification has been read
	Status Status `json:"status"`
	// Title is a short summary of the notification
	// WARNING: Direct mutation breaks deduplication - use builder methods
	Title string `json:"title"`
	// Message provides detailed information
	// WARNING: Direct mutation breaks deduplication - use builder methods
	Message string `json:"message"`
	// Component identifies the source component (e.g., "database", "audio", "birdweather")
	// WARNING: Direct mutation breaks deduplication - use WithComponent() instead
	Component string `json:"component,omitempty"`
	// Timestamp indicates when the notification was created
	Timestamp time.Time `json:"timestamp"`
	// Metadata contains additional context-specific data
	Metadata map[string]any `json:"metadata,omitempty"`
	// ExpiresAt indicates when the notification should be auto-removed (optional)
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	// ContentHash is a hash of the notification content for deduplication
	ContentHash string `json:"-"` // Not exposed in JSON to maintain API compatibility
	// OccurrenceCount tracks how many times this notification has occurred
	OccurrenceCount int `json:"occurrence_count,omitempty"`
	// FirstOccurrence tracks when this notification first occurred (for deduplicated notifications)
	FirstOccurrence *time.Time `json:"first_occurrence,omitempty"`
}

// NewNotification creates a new notification with a unique ID and timestamp
func NewNotification(notifType Type, priority Priority, title, message string) *Notification {
	n := &Notification{
		ID:              uuid.New().String(),
		Type:            notifType,
		Priority:        priority,
		Status:          StatusUnread,
		Title:           title,
		Message:         message,
		Timestamp:       time.Now(),
		Metadata:        make(map[string]any),
		OccurrenceCount: 1,
	}
	// Set first occurrence to point to the same timestamp (no extra allocation)
	n.FirstOccurrence = &n.Timestamp
	// Generate content hash for deduplication
	n.ContentHash = n.GenerateContentHash()
	return n
}

// WithComponent sets the component field and returns the notification for chaining
func (n *Notification) WithComponent(component string) *Notification {
	n.Component = component
	// Regenerate content hash since component is part of the hash
	n.ContentHash = n.GenerateContentHash()
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

// GenerateContentHash generates a hash of the notification's content for deduplication.
// The hash is stable and depends on the specific fields: component, type, title,
// and message in that exact order. Changing these fields or their order will break
// deduplication for existing notifications.
// Uses xxHash for better performance compared to cryptographic hashes.
func (n *Notification) GenerateContentHash() string {
	// Normalize the content to ensure consistent hashing
	content := strings.Join([]string{
		strings.ToLower(n.Component),
		string(n.Type),
		strings.TrimSpace(n.Title),
		strings.TrimSpace(n.Message),
	}, "|")
	
	h := xxhash.Sum64([]byte(content))
	return strconv.FormatUint(h, 36) // Use base36 for shorter string representation
}

// MarkAsRead updates the notification status to read
func (n *Notification) MarkAsRead() {
	n.Status = StatusRead
}

// MarkAsAcknowledged updates the notification status to acknowledged
func (n *Notification) MarkAsAcknowledged() {
	n.Status = StatusAcknowledged
}

// NotificationStore interface defines methods for persisting notifications
type NotificationStore interface {
	// Save persists a notification or merges with existing if duplicate
	// Returns the ID of the saved/updated notification
	Save(notification *Notification) (string, error)
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

// getPriorityWeight returns a numeric weight for priority comparison
func getPriorityWeight(p Priority) int {
	switch p {
	case PriorityCritical:
		return 4
	case PriorityHigh:
		return 3
	case PriorityMedium:
		return 2
	case PriorityLow:
		return 1
	default:
		return 0
	}
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
	mu                  sync.RWMutex
	notifications       map[string]*Notification      // Index by ID
	hashIndex          map[string]*Notification      // Index by content hash for deduplication
	maxSize            int
	unreadCount        int                          // Track unread count for optimization
	deduplicationWindow time.Duration                // Time window for deduplication
	lastCleanup        time.Time                    // Track last hash index cleanup
}

// NewInMemoryStore creates a new in-memory notification store
func NewInMemoryStore(maxSize int) *InMemoryStore {
	// Validate maxSize
	if maxSize <= 0 {
		maxSize = 1000 // Default to 1000 notifications
	}

	return &InMemoryStore{
		notifications:       make(map[string]*Notification),
		hashIndex:          make(map[string]*Notification),
		maxSize:            maxSize,
		deduplicationWindow: DefaultDeduplicationWindow,
		lastCleanup:        time.Now(),
	}
}

// SetDeduplicationWindow sets the time window for deduplication
// If window is <= 0, uses DefaultDeduplicationWindow instead
func (s *InMemoryStore) SetDeduplicationWindow(window time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	
	if window <= 0 {
		s.deduplicationWindow = DefaultDeduplicationWindow
	} else {
		s.deduplicationWindow = window
	}
}

// FindByContentHash finds an existing notification by content hash within the deduplication window
func (s *InMemoryStore) FindByContentHash(hash string) (*Notification, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	
	if existing, ok := s.hashIndex[hash]; ok {
		// Check if the existing notification is within the deduplication window
		cutoff := time.Now().Add(-s.deduplicationWindow)
		if existing.Timestamp.After(cutoff) {
			// Return a copy to prevent external modifications
			notifCopy := *existing
			return &notifCopy, true
		}
	}
	return nil, false
}

// Save stores a notification in memory, handling deduplication
// Returns the ID of the saved or deduplicated notification
func (s *InMemoryStore) Save(notification *Notification) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Periodically cleanup old hash entries (every hour)
	if time.Since(s.lastCleanup) > time.Hour {
		s.cleanupHashIndex()
		s.lastCleanup = time.Now()
	}

	// Check for duplicate within deduplication window
	if notification.ContentHash != "" {
		if existing, ok := s.hashIndex[notification.ContentHash]; ok {
			cutoff := time.Now().Add(-s.deduplicationWindow)
			if existing.Timestamp.After(cutoff) {
				// Found duplicate within window - update existing notification
				existing.OccurrenceCount++
				existing.Timestamp = notification.Timestamp // Update to latest timestamp
				
				// Update priority if new one is higher
				if getPriorityWeight(notification.Priority) > getPriorityWeight(existing.Priority) {
					existing.Priority = notification.Priority
				}
				
				// Merge metadata from new notification (preserves any new metadata fields)
				if notification.Metadata != nil {
					if existing.Metadata == nil {
						existing.Metadata = make(map[string]any)
					}
					// Merge new metadata into existing
					maps.Copy(existing.Metadata, notification.Metadata)
				}
				
				// Mark as unread if it was previously read
				wasRead := existing.Status != StatusUnread
				if wasRead {
					existing.Status = StatusUnread
					s.unreadCount++
				}
				
				// Return the ID of the deduplicated notification
				return existing.ID, nil
			}
		}
	}

	// No duplicate found or outside window - save as new notification
	// Enforce max size by removing oldest notifications
	if len(s.notifications) >= s.maxSize {
		s.removeOldest()
	}

	s.notifications[notification.ID] = notification
	
	// Update hash index
	if notification.ContentHash != "" {
		s.hashIndex[notification.ContentHash] = notification
	}
	
	// Update unread count if this is a new unread notification
	if notification.Status == StatusUnread {
		s.unreadCount++
	}
	
	// Return the ID of the newly saved notification
	return notification.ID, nil
}

// Get retrieves a notification by ID
func (s *InMemoryStore) Get(id string) (*Notification, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if notif, exists := s.notifications[id]; exists {
		// Return a deep copy to prevent external modifications from affecting the stored notification
		notifCopy := *notif
		// Deep copy metadata to prevent shared references
		if notif.Metadata != nil {
			notifCopy.Metadata = make(map[string]any, len(notif.Metadata))
			maps.Copy(notifCopy.Metadata, notif.Metadata)
		}
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
			// Return deep copies to prevent external modifications
			notifCopy := *notif
			// Deep copy metadata to prevent shared references
			if notif.Metadata != nil {
				notifCopy.Metadata = make(map[string]any, len(notif.Metadata))
				maps.Copy(notifCopy.Metadata, notif.Metadata)
			}
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
		return errors.Newf("notification not found: %s", notification.ID).
			Component("notification").
			Category(errors.CategoryNotFound).
			Build()
	}
	
	// Update unread count if status changed
	if oldNotif.Status == StatusUnread && notification.Status != StatusUnread {
		s.unreadCount--
	} else if oldNotif.Status != StatusUnread && notification.Status == StatusUnread {
		s.unreadCount++
	}
	
	// Update fields using a helper method to reduce complexity
	s.updateNotificationFields(oldNotif, notification)
	
	return nil
}

// updateNotificationFields copies fields from source to target notification
func (s *InMemoryStore) updateNotificationFields(target, source *Notification) {
	// Update the actual stored notification fields instead of replacing the pointer
	// This ensures hashIndex continues to point to the same object
	
	// Check if hash-affecting fields are being changed
	hashAffectingFieldsChanged := target.Component != source.Component ||
		target.Type != source.Type ||
		target.Title != source.Title ||
		target.Message != source.Message

	target.Status = source.Status
	target.Priority = source.Priority
	target.Title = source.Title
	target.Message = source.Message
	target.Component = source.Component
	target.Timestamp = source.Timestamp
	
	// Deep copy Metadata to prevent shared references
	if source.Metadata != nil {
		target.Metadata = make(map[string]any, len(source.Metadata))
		maps.Copy(target.Metadata, source.Metadata)
	} else {
		target.Metadata = nil
	}
	
	target.ExpiresAt = source.ExpiresAt
	target.OccurrenceCount = source.OccurrenceCount
	target.FirstOccurrence = source.FirstOccurrence
	
	// If hash-affecting fields changed, regenerate ContentHash and update hashIndex
	if hashAffectingFieldsChanged {
		oldHash := target.ContentHash
		target.ContentHash = target.GenerateContentHash()
		
		// Update hash index if hash changed
		if oldHash != target.ContentHash {
			// Remove old hash entry if it exists and points to this notification
			if oldEntry, exists := s.hashIndex[oldHash]; exists && oldEntry.ID == target.ID {
				delete(s.hashIndex, oldHash)
			}
			// Add new hash entry
			s.hashIndex[target.ContentHash] = target
		}
	}
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
		// Remove from hash index if it's the current entry
		if notif.ContentHash != "" {
			if hashEntry, ok := s.hashIndex[notif.ContentHash]; ok && hashEntry.ID == notif.ID {
				delete(s.hashIndex, notif.ContentHash)
			}
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
			// Remove from hash index if it's the current entry
			if notif.ContentHash != "" {
				if hashEntry, ok := s.hashIndex[notif.ContentHash]; ok && hashEntry.ID == notif.ID {
					delete(s.hashIndex, notif.ContentHash)
				}
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
		if notif, exists := s.notifications[oldestID]; exists {
			if notif.Status == StatusUnread {
				s.unreadCount--
			}
			// Remove from hash index if it's the current entry
			if notif.ContentHash != "" {
				if hashEntry, ok := s.hashIndex[notif.ContentHash]; ok && hashEntry.ID == notif.ID {
					delete(s.hashIndex, notif.ContentHash)
				}
			}
		}
		delete(s.notifications, oldestID)
	}
}

// matchesFilter checks if a notification matches the filter criteria
func (s *InMemoryStore) matchesFilter(notif *Notification, filter *FilterOptions) bool {
	if filter == nil {
		return true
	}

	// Check type filter
	if len(filter.Types) > 0 && !slices.Contains(filter.Types, notif.Type) {
		return false
	}

	// Check priority filter
	if len(filter.Priorities) > 0 && !slices.Contains(filter.Priorities, notif.Priority) {
		return false
	}

	// Check status filter
	if len(filter.Status) > 0 && !slices.Contains(filter.Status, notif.Status) {
		return false
	}

	// Check component filter
	if filter.Component != "" && notif.Component != filter.Component {
		return false
	}

	// Check time filters
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

// cleanupHashIndex removes expired entries from the hash index
// Note: This method must be called with the lock already held
func (s *InMemoryStore) cleanupHashIndex() {
	cutoff := time.Now().Add(-s.deduplicationWindow)
	
	// Collect hashes to delete to avoid modifying map during iteration
	toDelete := make([]string, 0)
	
	for hash, notif := range s.hashIndex {
		// Check if the notification is expired based on deduplication window
		if notif.Timestamp.Before(cutoff) {
			// Also check if it's been removed from main store (orphaned entry)
			if _, exists := s.notifications[notif.ID]; !exists {
				toDelete = append(toDelete, hash)
			}
		}
	}
	
	// Delete collected hashes
	for _, hash := range toDelete {
		delete(s.hashIndex, hash)
	}
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
