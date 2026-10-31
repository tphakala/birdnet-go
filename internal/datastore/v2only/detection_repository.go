package v2only

import (
	"context"
	"fmt"

	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/detection"
)

// detectionRepository is the datastore.DetectionRepository of a v2 database.
// Save writes the detection's own instant through the shared v2 write path, and the
// read methods map v2 entities straight to results with that stored instant. The
// remaining methods are served by the datastore.Interface bridge over the same
// Datastore.
type detectionRepository struct {
	datastore.DetectionRepository
	ds *Datastore
}

// NewDetectionRepository returns the detection repository for a v2 database.
// Save stores Result.Timestamp.Unix() as the detection instant, and stores each
// additional result's full float64 confidence. The reads return that stored instant
// in the datastore's timezone as Result.Timestamp (the bridge used for legacy stores
// formats and re-parses wall-clock text, which shifts one occurrence of a repeated
// DST hour). The remaining methods go through the datastore.Interface bridge.
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

// Get retrieves a detection by ID with the instant stored for it.
func (r *detectionRepository) Get(ctx context.Context, id string) (*detection.Result, error) {
	det, err := r.ds.getDetection(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("failed to get detection %s: %w", id, err)
	}
	return r.ds.detectionToResult(ctx, det), nil
}

// GetAdditionalResults returns the stored alternate predictions of a detection. The
// species names and codes resolve like those of the detection's own species, and the
// confidence is the full float64 (the bridge would give the scientific name as common
// name, no code and a float32). The order is unspecified. A row whose label is missing
// is skipped.
func (r *detectionRepository) GetAdditionalResults(ctx context.Context, id string) ([]detection.AdditionalResult, error) {
	detID, err := parseID(id)
	if err != nil {
		return nil, fmt.Errorf("failed to get additional results for detection %s: %w", id, err)
	}
	preds, err := r.ds.detection.GetPredictions(ctx, detID)
	if err != nil {
		return nil, fmt.Errorf("failed to get additional results for detection %s: %w", id, err)
	}
	if len(preds) == 0 {
		return nil, nil
	}

	// (detection_id, label_id) is unique, so the label IDs are already distinct.
	labelIDs := make([]uint, 0, len(preds))
	for _, pred := range preds {
		labelIDs = append(labelIDs, pred.LabelID)
	}
	labels, err := r.ds.label.GetByIDs(ctx, labelIDs)
	if err != nil {
		return nil, fmt.Errorf("failed to load labels of additional results for detection %s: %w", id, err)
	}

	results := make([]detection.AdditionalResult, 0, len(preds))
	for _, pred := range preds {
		label, ok := labels[pred.LabelID]
		if !ok || label == nil {
			continue
		}
		sci := detection.ExtractScientificName(label.ScientificName)
		if sci == "" {
			continue
		}
		results = append(results, detection.AdditionalResult{
			Species: detection.Species{
				ScientificName: sci,
				CommonName:     r.ds.resolveCommonName(sci),
				Code:           r.ds.speciesCodeMap[sci],
			},
			Confidence: pred.Confidence,
		})
	}
	return results, nil
}

// GetRecent retrieves the most recent detections.
func (r *detectionRepository) GetRecent(ctx context.Context, limit int) ([]*detection.Result, error) {
	dets, err := r.ds.recentDetections(ctx, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to get recent detections: %w", err)
	}
	results, err := r.ds.detectionsToResults(ctx, dets)
	if err != nil {
		return nil, fmt.Errorf("failed to get recent detections: %w", err)
	}
	return results, nil
}

// Search finds detections matching the given filters.
func (r *detectionRepository) Search(ctx context.Context, filters *datastore.DetectionFilters) ([]*detection.Result, int64, error) {
	advanced, err := filters.ToAdvancedSearchFilters(r.ds.timezone)
	if err != nil {
		return nil, 0, err
	}
	dets, total, err := r.ds.searchAdvanced(ctx, &advanced)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to search detections: %w", err)
	}
	results, err := r.ds.detectionsToResults(ctx, dets)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to search detections: %w", err)
	}
	return results, total, nil
}

// SearchAdvanced finds detections matching the filters as given.
func (r *detectionRepository) SearchAdvanced(ctx context.Context, filters *datastore.AdvancedSearchFilters) ([]*detection.Result, int64, error) {
	if filters == nil {
		return nil, 0, datastore.ErrNilAdvancedSearchFilters
	}
	dets, total, err := r.ds.searchAdvanced(ctx, filters)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to search detections: %w", err)
	}
	results, err := r.ds.detectionsToResults(ctx, dets)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to search detections: %w", err)
	}
	return results, total, nil
}

// GetBySpecies retrieves detections for a specific species.
func (r *detectionRepository) GetBySpecies(ctx context.Context, species, date, hour string, duration, limit, offset int) ([]*detection.Result, int64, error) {
	if limit <= 0 {
		limit = datastore.DefaultDetectionLimit
	}

	dets, err := r.ds.speciesDetections(ctx, species, date, hour, duration, false, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to get species detections: %w", err)
	}
	total, err := r.ds.countSpeciesDetections(ctx, species, date, hour, duration)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to count species detections: %w", err)
	}
	results, err := r.ds.detectionsToResults(ctx, dets)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to get species detections: %w", err)
	}
	return results, total, nil
}

// GetByDateRange retrieves detections within a date range.
func (r *detectionRepository) GetByDateRange(ctx context.Context, startDate, endDate string, limit, offset int) ([]*detection.Result, int64, error) {
	advanced, err := datastore.DateRangeSearchFilters(startDate, endDate, limit, offset, r.ds.timezone)
	if err != nil {
		return nil, 0, err
	}
	dets, total, err := r.ds.searchAdvanced(ctx, &advanced)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to get detections by date range: %w", err)
	}
	results, err := r.ds.detectionsToResults(ctx, dets)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to get detections by date range: %w", err)
	}
	return results, total, nil
}

// GetHourly retrieves detections for a specific hour on a date.
func (r *detectionRepository) GetHourly(ctx context.Context, date, hour string, duration, limit, offset int) ([]*detection.Result, int64, error) {
	dets, err := r.ds.hourlyDetections(ctx, date, hour, duration, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to get hourly detections: %w", err)
	}
	total, err := r.ds.countHourlyDetections(ctx, date, hour, duration)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to count hourly detections: %w", err)
	}
	results, err := r.ds.detectionsToResults(ctx, dets)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to get hourly detections: %w", err)
	}
	return results, total, nil
}
