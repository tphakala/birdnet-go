package repository

import (
	"context"

	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
)

// PrivacyFilterDiscardRepository stores the detections the privacy filter
// discarded, with the detectors whose hits caused each one.
type PrivacyFilterDiscardRepository interface {
	// Save inserts discard and discard.Hits in one transaction. IDs and DiscardID
	// on the input are ignored; on success discard.ID is set. The Source
	// association is never written.
	// Returns ErrPrivacyDiscardNil when discard is nil.
	Save(ctx context.Context, discard *entities.PrivacyFilterDiscard) error

	// DeleteBefore deletes the discards whose DiscardedAt is before cutoffMillis
	// (Unix milliseconds), their hit rows with them (foreign key cascade), and
	// returns how many discards were deleted.
	DeleteBefore(ctx context.Context, cutoffMillis int64) (int64, error)

	// ListPage returns one page of the discards with DiscardedAt in
	// [start, end) (Unix milliseconds), newest first (DiscardedAt descending, then
	// ID descending), each with its Hits ordered by ID, and the total number of
	// discards in the range. limit must be positive and offset non-negative. The
	// Source association is not loaded.
	ListPage(ctx context.Context, start, end int64, limit, offset int) ([]entities.PrivacyFilterDiscard, int64, error)
}
