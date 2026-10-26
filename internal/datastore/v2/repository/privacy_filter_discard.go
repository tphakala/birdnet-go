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
}
