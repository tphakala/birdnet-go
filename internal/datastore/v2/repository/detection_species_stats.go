package repository

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/detection"
)

// Named arguments of the species statistics statement.
const (
	statsArgFalsePositive = "falsePositive"
	statsArgCorrect       = "correct"
	statsArgLabels        = "labels"
)

// speciesStatsStatement is the one aggregate behind GetSpeciesStats. The first
// two %s are the detections and reviews tables.
//
// The form is load-bearing. The label IDs are literal so the engines seek a label
// index instead of joining through the labels table. One statement keeps the
// counts consistent with each other when a review is saved concurrently. COUNT
// of a CASE is used instead of SUM because SUM over no rows is NULL (and DECIMAL
// on MySQL), while COUNT is a BIGINT and never NULL. The CASE keeps unreviewed
// rows because comparing a missing review with 'false_positive' is NULL, which
// takes the ELSE arm. Rows are selected by label_id alone, as the species summary
// does. The column aliases are the snake_case of the SpeciesStats fields.
const speciesStatsStatement = `
SELECT
  COUNT(CASE WHEN dr.verified = @falsePositive THEN NULL ELSE 1 END)           AS detections,
  MIN(CASE WHEN dr.verified = @falsePositive THEN NULL ELSE d.detected_at END) AS first_detected_at,
  MAX(CASE WHEN dr.verified = @falsePositive THEN NULL ELSE d.detected_at END) AS last_detected_at,
  AVG(CASE WHEN dr.verified = @falsePositive THEN NULL ELSE d.confidence END)  AS avg_confidence,
  MAX(CASE WHEN dr.verified = @falsePositive THEN NULL ELSE d.confidence END)  AS max_confidence,
  COUNT(CASE WHEN dr.verified = @correct THEN 1 END)                           AS correct_reviews,
  COUNT(CASE WHEN dr.verified = @falsePositive THEN 1 END)                     AS false_positive_reviews
FROM %s d
LEFT JOIN %s dr ON dr.detection_id = d.id
WHERE d.label_id IN @labels`

// GetSpeciesStats returns the all-time statistics of one species. The species is
// the scientific name extracted from scientificName, selected across every
// model's labels with the rule GetNeighborIDs uses, so both agree on what a
// species is.
func (r *detectionRepository) GetSpeciesStats(ctx context.Context, scientificName string) (*SpeciesStats, error) {
	// A name with no species part would match every label that is empty or starts
	// with an underscore.
	species := detection.ExtractScientificName(scientificName)
	if species == "" {
		return &SpeciesStats{}, nil
	}

	match, args := LabelMatchForScientificName(species)
	var labelIDs []uint
	if err := r.db.WithContext(ctx).Table(r.labelsTable()).Where(match, args...).Pluck(columnDetectionID, &labelIDs).Error; err != nil {
		return nil, fmt.Errorf("load labels of the species: %w", err)
	}
	if len(labelIDs) == 0 {
		return &SpeciesStats{}, nil
	}

	var stats SpeciesStats
	statement := fmt.Sprintf(speciesStatsStatement, r.tableName(), r.reviewsTable())
	err := r.db.WithContext(ctx).Raw(statement,
		sql.Named(statsArgFalsePositive, string(entities.VerificationFalsePositive)),
		sql.Named(statsArgCorrect, string(entities.VerificationCorrect)),
		sql.Named(statsArgLabels, labelIDs),
	).Scan(&stats).Error
	if err != nil {
		return nil, fmt.Errorf("aggregate species statistics: %w", err)
	}
	return &stats, nil
}
