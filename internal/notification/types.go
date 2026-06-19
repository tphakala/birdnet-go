// Package notification provides a system for managing and broadcasting notifications
// throughout the BirdNET-Go application. It handles system alerts, errors, and
// important detection events.
package notification

import (
	"cmp"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
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

// Delivery target constants control where a notification is routed.
const (
	DeliveryTargetAll  = ""     // default: deliver to all channels
	DeliveryTargetBell = "bell" // in-app notification store only
	DeliveryTargetPush = "push" // push providers only
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

	// Translation key fields for frontend i18n support.
	// The frontend uses these to translate notifications via t(key, params).
	// Title/Message fields always contain English fallback text for push providers.

	// TitleKey is the i18n translation key for the title (e.g., "notifications.content.startup.title")
	TitleKey string `json:"title_key,omitempty"`
	// TitleParams contains interpolation parameters for the title translation.
	// Values must be scalar types (string, int, float64, bool) for safe frontend interpolation.
	TitleParams map[string]any `json:"title_params,omitempty"`
	// MessageKey is the i18n translation key for the message
	MessageKey string `json:"message_key,omitempty"`
	// MessageParams contains interpolation parameters for the message translation.
	// Values must be scalar types (string, int, float64, bool) for safe frontend interpolation.
	MessageParams map[string]any `json:"message_params,omitempty"`

	// DeliveryTarget controls where this notification is delivered.
	// Empty string means "all channels" (backwards-compatible default).
	// Set to "bell" for in-app only, "push" for push providers only.
	// Transient routing field; never serialized or persisted.
	DeliveryTarget string `json:"-"`

	// seq is a process-local monotonic creation sequence assigned by
	// NewNotification. It exists solely to give List a deterministic,
	// creation-ordered tiebreaker when two notifications share a Timestamp
	// (routine on coarse-resolution clocks and under load). Unexported and
	// never serialized or persisted; copied by Clone so the store's copy
	// keeps the original creation order.
	seq uint64
}

// notificationSeq assigns a process-wide, strictly increasing sequence number
// to each notification created via NewNotification. It is the tiebreaker that
// makes InMemoryStore.List ordering deterministic for notifications that share
// a Timestamp: results are gathered by ranging a Go map (randomized order), so
// without a real tiebreaker an unstable timestamp-only sort produced arbitrary
// ordering for equal timestamps.
var notificationSeq atomic.Uint64

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
		seq:       notificationSeq.Add(1),
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

// WithTitleKey sets the translation key and parameters for the title.
// Params values must be scalar types (string, int, float64, bool) for safe frontend interpolation.
// Non-scalar values are coerced to strings via fmt.Sprintf as a safety net.
func (n *Notification) WithTitleKey(key string, params map[string]any) *Notification {
	n.TitleKey = key
	n.TitleParams = sanitizeParams(params)
	return n
}

// WithMessageKey sets the translation key and parameters for the message.
// Params values must be scalar types (string, int, float64, bool) for safe frontend interpolation.
// Non-scalar values are coerced to strings via fmt.Sprintf as a safety net.
func (n *Notification) WithMessageKey(key string, params map[string]any) *Notification {
	n.MessageKey = key
	n.MessageParams = sanitizeParams(params)
	return n
}

// WithDeliveryTarget sets the delivery target and returns the notification for chaining.
func (n *Notification) WithDeliveryTarget(target string) *Notification {
	n.DeliveryTarget = target
	return n
}

// sanitizeParams ensures all parameter values are scalar types suitable for
// frontend i18n interpolation. Non-scalar values (structs, slices, maps, time.Time)
// are coerced to their string representation.
func sanitizeParams(params map[string]any) map[string]any {
	if params == nil {
		return nil
	}
	sanitized := make(map[string]any, len(params))
	for k, v := range params {
		switch v.(type) {
		case string, int, int64, float64, bool:
			sanitized[k] = v
		default:
			sanitized[k] = fmt.Sprintf("%v", v)
		}
	}
	return sanitized
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
// without risk of concurrent map access if the original is modified, and it
// backs the InMemoryStore read path (Get/List) and write path (Save/Update).
// NOTE: every field of Notification must be copied here; a newly added field
// will be silently dropped from stored and returned copies if omitted.
func (n *Notification) Clone() *Notification {
	if n == nil {
		return nil
	}

	clone := &Notification{
		ID:             n.ID,
		Type:           n.Type,
		Priority:       n.Priority,
		Status:         n.Status,
		Title:          n.Title,
		Message:        n.Message,
		Component:      n.Component,
		Timestamp:      n.Timestamp,
		TitleKey:       n.TitleKey,
		MessageKey:     n.MessageKey,
		DeliveryTarget: n.DeliveryTarget,
		seq:            n.seq,
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

	// Deep copy translation parameter maps
	if n.TitleParams != nil {
		clone.TitleParams = deepCopyMetadata(n.TitleParams)
	}
	if n.MessageParams != nil {
		clone.MessageParams = deepCopyMetadata(n.MessageParams)
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
	// Get retrieves a notification by ID. The returned notification is an
	// independent deep copy; callers may read or mutate it (including its
	// Metadata/params maps) without affecting stored state or racing other
	// readers. Implementations MUST honor this so REST/SSE callers can marshal
	// results concurrently with store mutations.
	Get(id string) (*Notification, error)
	// List returns notifications with optional filtering. Each returned
	// notification is an independent deep copy (see Get).
	List(filter *FilterOptions) ([]*Notification, error)
	// Count returns the number of notifications matching filter. filter.Limit
	// and filter.Offset are ignored — pagination does not apply to a count.
	Count(filter *FilterOptions) (int, error)
	// Update modifies an existing notification
	Update(notification *Notification) error
	// MarkAllRead sets every unread, non-toast notification to StatusRead and
	// returns the number of notifications that were changed.
	MarkAllRead() (int, error)
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
	// IncludeToasts, when true, allows toast-flagged notifications to appear
	// in results. Toasts are ephemeral by design (short expiry, SSE-only
	// delivery); the default (false) keeps them out of List/Count results so
	// callers that inspect "the notifications view" cannot accidentally leak
	// them — a concrete concern after the guest-visible endpoints widened in
	// PR #2775. Set to true only when the caller truly needs toast metadata.
	IncludeToasts bool
}

// InMemoryStore provides a thread-safe in-memory notification store
type InMemoryStore struct {
	mu            sync.RWMutex
	notifications map[string]*Notification
	maxSize       int
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

// Save stores a notification in memory. A deep copy is made so later
// mutations of the caller's notification (adding metadata, changing status)
// do not leak into the store or into concurrent subscribers reading via
// List/Count/Get. Broadcasting already clones before sending to subscribers,
// so storing a clone keeps the store's copy independent from both.
func (s *InMemoryStore) Save(notification *Notification) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Enforce max size by removing oldest notifications
	if len(s.notifications) >= s.maxSize {
		s.removeOldest()
	}

	s.notifications[notification.ID] = notification.Clone()
	return nil
}

// Get retrieves a notification by ID
func (s *InMemoryStore) Get(id string) (*Notification, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if notif, exists := s.notifications[id]; exists {
		// Return a deep copy so external mutations (including in-place writes to
		// the Metadata/params maps by callers or concurrent JSON marshaling in
		// REST handlers) cannot affect the stored notification or race other
		// readers. A shallow copy (*notif) would alias those maps; Clone copies
		// them. The clone runs under RLock so the source is stable.
		return notif.Clone(), nil
	}
	return nil, ErrNotificationNotFound
}

// List returns filtered notifications
func (s *InMemoryStore) List(filter *FilterOptions) ([]*Notification, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// len(s.notifications) is an upper bound on matches; preallocate to avoid
	// repeated slice growth during the filter loop.
	results := make([]*Notification, 0, len(s.notifications))
	for _, notif := range s.notifications {
		if s.matchesFilter(notif, filter) {
			// Return deep copies so callers (and concurrent JSON marshaling in
			// REST handlers) never touch the stored Metadata/params maps. A
			// shallow copy would alias them; see Get for rationale.
			results = append(results, notif.Clone())
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

// Count returns the number of notifications matching filter. Reuses the same
// matchesFilter predicate as List but avoids allocating a result slice, which
// matters for callers that only need a badge count (e.g. NotificationBell
// polling /notifications/unread/count). filter.Limit and filter.Offset are
// ignored — a count is not paginated.
func (s *InMemoryStore) Count(filter *FilterOptions) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	count := 0
	for _, notif := range s.notifications {
		if s.matchesFilter(notif, filter) {
			count++
		}
	}
	return count, nil
}

// Update modifies an existing notification. Stores a deep copy for the same
// reason as Save: the caller's pointer may continue to mutate after the call
// returns (e.g. MarkAsRead builds a new status on the returned copy and
// writes back; the store must not share map references with that copy).
func (s *InMemoryStore) Update(notification *Notification) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.notifications[notification.ID]; !exists {
		return ErrNotificationNotFound
	}

	s.notifications[notification.ID] = notification.Clone()
	return nil
}

// MarkAllRead sets every unread, non-toast notification to StatusRead.
// Returns the number of notifications that were changed.
func (s *InMemoryStore) MarkAllRead() (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	changed := 0
	for _, notif := range s.notifications {
		if notif.Status == StatusUnread && !isToastNotification(notif) {
			notif.Status = StatusRead
			changed++
		}
	}
	return changed, nil
}

// Delete removes a notification
func (s *InMemoryStore) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.notifications, id)
	return nil
}

// DeleteExpired removes all expired notifications
func (s *InMemoryStore) DeleteExpired() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for id, notif := range s.notifications {
		if notif.IsExpired() {
			delete(s.notifications, id)
		}
	}
	return nil
}

// removeOldest removes the oldest notification to make room. "Oldest" is the
// entry List ranks last: earliest Timestamp, and among equal timestamps the
// lowest creation sequence (the earliest created). Using seq as the tiebreaker
// keeps eviction deterministic and consistent with List's ordering on
// coarse-resolution clocks where timestamps routinely collide; without it the
// victim among equal-timestamp entries was whichever the randomized map range
// happened to visit first.
func (s *InMemoryStore) removeOldest() {
	var oldestID string
	var oldestTime time.Time
	var oldestSeq uint64

	for id, notif := range s.notifications {
		switch {
		case oldestID == "": // first candidate
		case notif.Timestamp.Before(oldestTime): // strictly older
		case notif.Timestamp.Equal(oldestTime) && notif.seq < oldestSeq: // same time, created earlier
		default:
			continue
		}
		oldestID, oldestTime, oldestSeq = id, notif.Timestamp, notif.seq
	}

	if oldestID != "" {
		delete(s.notifications, oldestID)
	}
}

// matchesFilter checks if a notification matches the filter criteria.
// Toast-flagged notifications are excluded by default (they are ephemeral
// SSE-only payloads); callers that need toast metadata must opt in via
// FilterOptions.IncludeToasts.
func (s *InMemoryStore) matchesFilter(notif *Notification, filter *FilterOptions) bool {
	if isToastNotification(notif) && (filter == nil || !filter.IncludeToasts) {
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

// GetUnreadCount returns the count of unread, non-toast notifications.
// Toasts are ephemeral and are excluded so the count matches what callers
// see via List. Iterates the store because a single cached counter cannot
// correctly distinguish toast from non-toast saves at write time.
func (s *InMemoryStore) GetUnreadCount() (int, error) {
	return s.Count(&FilterOptions{Status: []Status{StatusUnread}})
}

// sortNotificationsByTime sorts notifications newest-first by a deterministic
// total order. The primary key is Timestamp (descending). Ties are broken by
// the creation sequence (descending, so the more recently created wins) and,
// as a final fallback for notifications built without NewNotification (seq 0),
// by ID. A total order is required because the input is gathered by ranging a
// Go map, whose iteration order is randomized; a timestamp-only comparison
// would leave equal-timestamp notifications in arbitrary, run-varying order.
func sortNotificationsByTime(notifications []*Notification) {
	slices.SortFunc(notifications, func(a, b *Notification) int {
		return cmp.Or(
			b.Timestamp.Compare(a.Timestamp), // newest first
			cmp.Compare(b.seq, a.seq),        // later creation first on equal timestamps
			strings.Compare(a.ID, b.ID),      // stable fallback for seq-less notifications
		)
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
