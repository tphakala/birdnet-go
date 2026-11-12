package repository

import (
	"context"

	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
)

// NoiseExampleRepository stores the noise examples a user taught the noise
// example gate. The rows outlive the detection they were learned from, so no
// method here removes examples except by an explicit verdict change for one
// detection.
type NoiseExampleRepository interface {
	// ReplaceForDetection deletes the rows attached to detectionID and inserts rows
	// attached to it, in one transaction. Detached rows are never touched. The
	// DetectionID and ID of the input rows are ignored.
	ReplaceForDetection(ctx context.Context, detectionID uint, rows []entities.NoiseExample) error

	// InsertDetached inserts rows that belong to no detection, in one transaction.
	// The DetectionID and ID of the input rows are ignored.
	InsertDetached(ctx context.Context, rows []entities.NoiseExample) error

	// ListAll returns every row, attached or detached, oldest first.
	ListAll(ctx context.Context) ([]entities.NoiseExample, error)

	// DetachOrphans clears the link of every row whose detection no longer exists,
	// which a delete made while foreign keys were not enforced leaves behind. It
	// removes no row, changes nothing a second time, and returns how many links it
	// cleared.
	DetachOrphans(ctx context.Context) (int64, error)

	// CountForDetection returns the number of rows attached to detectionID, by
	// embedding space.
	CountForDetection(ctx context.Context, detectionID uint) (map[string]int64, error)

	// CountBySpace returns the number of rows of every embedding space.
	CountBySpace(ctx context.Context) (map[string]int64, error)

	// DeleteAll deletes every row of every embedding space, attached or detached,
	// in one statement, and returns how many it deleted. It touches no review.
	DeleteAll(ctx context.Context) (int64, error)

	// RemoveForVerdict deletes the rows attached to detectionID and upserts that
	// detection's review with the status verified and no reason, in one
	// transaction, and returns how many rows it deleted. The review write fails
	// when the detection does not exist; both changes are then rolled back.
	RemoveForVerdict(ctx context.Context, detectionID uint, verified entities.VerificationStatus) (int64, error)
}
