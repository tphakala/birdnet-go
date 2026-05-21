package support

import (
	"context"
	"time"

	"github.com/tphakala/birdnet-go/internal/datastore"
)

// supportDumpEventLimit is the maximum number of events included in a support dump.
const supportDumpEventLimit = 1000

// supportDumpEventDays is the number of days of events to include in a support dump.
const supportDumpEventDays = 30

// DatastoreAppEventsProvider adapts a datastore.Interface to the AppEventsProvider
// interface, converting datastore.AppEvent values to support-local AppEventEntry
// and scrubbing sensitive metadata keys at export time.
type DatastoreAppEventsProvider struct {
	ds            datastore.Interface
	sensitiveKeys []string
}

// NewDatastoreAppEventsProvider creates a provider backed by the given datastore.
func NewDatastoreAppEventsProvider(ds datastore.Interface, sensitiveKeys []string) *DatastoreAppEventsProvider {
	if len(sensitiveKeys) == 0 {
		sensitiveKeys = DefaultSensitiveKeys()
	}
	return &DatastoreAppEventsProvider{
		ds:            ds,
		sensitiveKeys: sensitiveKeys,
	}
}

// GetRecentAppEvents returns events from the last 30 days, capped at 1000 entries,
// with sensitive metadata values scrubbed.
func (p *DatastoreAppEventsProvider) GetRecentAppEvents(ctx context.Context, limit int) ([]AppEventEntry, error) {
	if limit <= 0 {
		limit = supportDumpEventLimit
	}

	since := time.Now().Add(-supportDumpEventDays * 24 * time.Hour)
	events, err := p.ds.GetAppEventsSince(ctx, since, limit)
	if err != nil {
		return nil, err
	}

	entries := make([]AppEventEntry, 0, len(events))
	for _, ev := range events {
		entries = append(entries, AppEventEntry{
			Timestamp: ev.Timestamp,
			Category:  ev.Category,
			EventType: ev.EventType,
			Message:   ev.Message,
			Metadata:  p.scrubMetadata(ev.Metadata),
		})
	}

	return entries, nil
}

// scrubMetadata redacts values for sensitive keys in the metadata map.
func (p *DatastoreAppEventsProvider) scrubMetadata(metadata map[string]any) map[string]any {
	if len(metadata) == 0 {
		return metadata
	}

	scrubbed := make(map[string]any, len(metadata))
	for k, v := range metadata {
		if p.isKeySensitive(k) {
			scrubbed[k] = redactedPlaceholder
		} else {
			scrubbed[k] = p.scrubNestedValue(v)
		}
	}
	return scrubbed
}

// scrubNestedValue recursively scrubs sensitive keys in nested maps and slices.
func (p *DatastoreAppEventsProvider) scrubNestedValue(value any) any {
	switch v := value.(type) {
	case map[string]any:
		return p.scrubMetadata(v)
	case []any:
		scrubbed := make([]any, len(v))
		for i, item := range v {
			scrubbed[i] = p.scrubNestedValue(item)
		}
		return scrubbed
	default:
		return value
	}
}

// isKeySensitive checks if a metadata key matches any sensitive key pattern.
func (p *DatastoreAppEventsProvider) isKeySensitive(key string) bool {
	return MatchesSensitiveKey(key, p.sensitiveKeys)
}
