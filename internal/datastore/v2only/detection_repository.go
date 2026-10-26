package v2only

import (
	"context"
	"fmt"

	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/detection"
)

// detectionRepository is the datastore.DetectionRepository of a v2 database.
// Save writes the detection's own instant through the shared v2 write path; every
// other method is served by the datastore.Interface bridge over the same Datastore
// until it moves to v2 reads.
type detectionRepository struct {
	datastore.DetectionRepository
	ds *Datastore
}

// NewDetectionRepository returns the detection repository for a v2 database.
// Save stores Result.Timestamp.Unix() as the detection instant (the bridge used
// for legacy stores formats and re-parses wall-clock text, which shifts one
// occurrence of a repeated DST hour), and stores each additional result's full
// float64 confidence. The remaining methods go through the datastore.Interface
// bridge in the datastore's timezone.
func NewDetectionRepository(ds *Datastore) datastore.DetectionRepository {
	return &detectionRepository{
		DetectionRepository: datastore.NewDetectionRepository(ds, ds.timezone),
		ds:                  ds,
	}
}

// Save persists a detection result and its additional predictions, and sets
// result.ID to the new row's ID. It honours ctx for the database transaction (the
// transaction also has its own timeout); model contributions run after the commit,
// detached from ctx, and are best effort.
func (r *detectionRepository) Save(ctx context.Context, result *detection.Result, additionalResults []detection.AdditionalResult) error {
	id, err := r.ds.saveDetection(ctx, writeFromResult(result, additionalResults))
	if err != nil {
		return fmt.Errorf("failed to save detection: %w", err)
	}
	result.ID = id
	return nil
}
