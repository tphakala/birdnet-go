package repository

import (
	"context"
	"fmt"

	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/detection"
	"github.com/tphakala/birdnet-go/internal/errors"
	"gorm.io/gorm"
)

// Pieces of the neighbor statements.
const (
	columnDetectionModelID = "model_id"
	selectMaxDetectionID   = "MAX(" + columnDetectionID + ")"
	selectMinDetectionID   = "MIN(" + columnDetectionID + ")"
	belowAnchorCondition   = columnDetectionID + " < ?"
	aboveAnchorCondition   = columnDetectionID + " > ?"
	ownLabelCondition      = columnDetectionModelID + " = ? AND " + columnLabelID + " = ?"
	adjacentIDsStatement   = "SELECT (?) AS previous_id, (?) AS next_id"
)

// GetNeighborIDs returns the nearest lower and higher detection ID overall and
// among detections of the same species. The species labels are every label whose
// scientific name is the one extracted from the detection's own label, in any
// model, so both ends of a step see the same set of labels.
func (r *detectionRepository) GetNeighborIDs(ctx context.Context, id uint) (*DetectionNeighbors, error) {
	det, err := r.Get(ctx, id)
	if err != nil {
		return nil, err
	}

	neighbors := &DetectionNeighbors{}
	neighbors.Previous, neighbors.Next, err = r.adjacentIDs(ctx, id, nil)
	if err != nil {
		return nil, fmt.Errorf("find adjacent detections: %w", err)
	}

	label, err := NewLabelRepository(r.db, r.metrics, r.useV2Prefix, r.isMySQL).GetByID(ctx, det.LabelID)
	if err != nil {
		if errors.Is(err, ErrLabelNotFound) {
			return neighbors, nil
		}
		return nil, fmt.Errorf("load detection label: %w", err)
	}

	match, args := LabelMatchForScientificName(detection.ExtractScientificName(label.ScientificName))
	var speciesLabels []entities.Label
	if err := r.db.WithContext(ctx).Table(r.labelsTable()).Where(match, args...).Find(&speciesLabels).Error; err != nil {
		return nil, fmt.Errorf("load labels of the species: %w", err)
	}
	for i := range speciesLabels {
		previous, next, err := r.adjacentIDs(ctx, id, &speciesLabels[i])
		if err != nil {
			return nil, fmt.Errorf("find adjacent detections of the species: %w", err)
		}
		neighbors.PreviousSameSpecies = max(neighbors.PreviousSameSpecies, previous)
		if next != 0 && (neighbors.NextSameSpecies == 0 || next < neighbors.NextSameSpecies) {
			neighbors.NextSameSpecies = next
		}
	}
	return neighbors, nil
}

// adjacentIDs returns the greatest detection ID below id and the smallest above
// it, restricted to detections of label when it is not nil. A missing neighbor is
// 0. Both come from one statement of two scalar subqueries.
//
// The form is load-bearing. With the label, both model_id and label_id are
// equality conditions, so each subquery is one seek in idx_detection_model_label
// (the engines append the primary key to the index), on SQLite, MySQL and
// MariaDB alike. MAX and MIN are used instead of ORDER BY id LIMIT 1 because
// MySQL may answer that by walking the primary key until it meets a row of the
// label. Dropping model_id, or matching several labels or models with IN, makes
// the engines range-scan the index. The lookup relies on a detection carrying
// the model of its label.
func (r *detectionRepository) adjacentIDs(ctx context.Context, id uint, label *entities.Label) (previous, next uint, err error) {
	side := func(aggregate, condition string) *gorm.DB {
		q := r.db.Table(r.tableName()).Select(aggregate).Where(condition, id)
		if label != nil {
			q = q.Where(ownLabelCondition, label.ModelID, label.ID)
		}
		return q
	}

	// A NULL aggregate (no such neighbor) scans into a struct field as 0.
	var result struct {
		PreviousID uint
		NextID     uint
	}
	err = r.db.WithContext(ctx).
		Raw(adjacentIDsStatement, side(selectMaxDetectionID, belowAnchorCondition), side(selectMinDetectionID, aboveAnchorCondition)).
		Scan(&result).Error
	if err != nil {
		return 0, 0, err
	}
	return result.PreviousID, result.NextID, nil
}
