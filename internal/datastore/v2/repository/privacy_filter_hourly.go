package repository

import (
	"context"

	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
)

// PrivacyFilterHourlyRepository stores the hourly rollup of privacy filter
// activity.
type PrivacyFilterHourlyRepository interface {
	// AddDelta adds the counters of row to the row with the same key (hour start,
	// source, model, trigger type and threshold), creating it when missing.
	// MaxScore keeps the larger of the stored and the given value. The ID of row
	// is ignored and left untouched.
	// Returns ErrPrivacyRollupNoSource when row.SourceID is 0.
	AddDelta(ctx context.Context, row *entities.PrivacyFilterHourly) error

	// DeleteBefore deletes the rows whose hour start is before cutoffHourStart
	// (Unix seconds) and returns how many were deleted.
	DeleteBefore(ctx context.Context, cutoffHourStart int64) (int64, error)
}
