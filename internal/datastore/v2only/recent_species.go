package v2only

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/tphakala/birdnet-go/internal/datastore"
	legacyentities "github.com/tphakala/birdnet-go/internal/datastore/entities"
	"github.com/tphakala/birdnet-go/internal/detection"
	"github.com/tphakala/birdnet-go/internal/errors"
)

type recentSpeciesQueryRow struct {
	ScientificName      string          `gorm:"column:scientific_name"`
	Bucket              int             `gorm:"column:bucket"`
	Count               int             `gorm:"column:detection_count"`
	ConfidenceTotal     float64         `gorm:"column:confidence_total"`
	BucketMaxConfidence float64         `gorm:"column:bucket_max_confidence"`
	LatestDetectedAt    sql.NullInt64   `gorm:"column:latest_detected_at"`
	LatestConfidence    sql.NullFloat64 `gorm:"column:latest_confidence"`
	LatestDetectionID   sql.NullInt64   `gorm:"column:latest_detection_id"`
}

// GetRecentSpeciesData returns bucketed species aggregates for the inclusive
// [start, end] time window. It performs one query and excludes false positives.
func (ds *Datastore) GetRecentSpeciesData(ctx context.Context, start, end time.Time, minConfidence float64, buckets int) ([]datastore.RecentSpeciesData, error) {
	windowSeconds := end.Unix() - start.Unix()
	if buckets <= 0 || windowSeconds <= 0 {
		return nil, errors.Newf("invalid recent species window: buckets=%d duration=%s", buckets, end.Sub(start)).
			Component("datastore").
			Category(errors.CategoryValidation).
			Build()
	}

	db := ds.manager.DB()
	dialect := db.Name()
	var bucketCast string
	switch dialect {
	case datastore.DialectSQLite:
		bucketCast = "CAST((elapsed_seconds * %d) / %d AS INTEGER)"
	case datastore.DialectMySQL:
		bucketCast = "FLOOR((elapsed_seconds * %d) / %d)"
	default:
		return nil, errors.Newf("unsupported database dialect %q", dialect).
			Component("datastore").
			Category(errors.CategoryValidation).
			Build()
	}

	bucketValue := fmt.Sprintf(bucketCast, buckets, windowSeconds)
	bucketExpr := fmt.Sprintf(
		"CASE WHEN %s >= %d THEN %d ELSE %s END",
		bucketValue, buckets, buckets-1, bucketValue,
	)
	prefix := ds.manager.TablePrefix()
	query := fmt.Sprintf(`
		WITH filtered AS (
			SELECT
				d.id,
				l.scientific_name,
				d.confidence,
				d.detected_at,
				d.detected_at - ? AS elapsed_seconds
			FROM %sdetections d
			JOIN %slabels l ON l.id = d.label_id
			WHERE d.detected_at >= ? AND d.detected_at <= ?
				AND d.confidence >= ?
				AND l.scientific_name <> ''
				AND NOT EXISTS (
					SELECT 1 FROM %sdetection_reviews dr
					WHERE dr.detection_id = d.id AND dr.verified = ?
				)
		),
		ranked AS (
			SELECT filtered.*,
				ROW_NUMBER() OVER (
					PARTITION BY scientific_name
					ORDER BY detected_at DESC, id DESC
				) AS latest_rank
			FROM filtered
		),
		bucketed AS (
			SELECT ranked.*, %s AS bucket
			FROM ranked
		)
		SELECT
			MAX(scientific_name) AS scientific_name,
			bucket,
			COUNT(*) AS detection_count,
			SUM(confidence) AS confidence_total,
			MAX(confidence) AS bucket_max_confidence,
			MAX(CASE WHEN latest_rank = 1 THEN detected_at END) AS latest_detected_at,
			MAX(CASE WHEN latest_rank = 1 THEN confidence END) AS latest_confidence,
			MAX(CASE WHEN latest_rank = 1 THEN id END) AS latest_detection_id
		FROM bucketed
		GROUP BY scientific_name, bucket
		ORDER BY scientific_name, bucket
	`, prefix, prefix, prefix, bucketExpr)

	var rows []recentSpeciesQueryRow
	if err := db.WithContext(ctx).Raw(
		query,
		start.Unix(),
		start.Unix(),
		end.Unix(),
		minConfidence,
		string(legacyentities.VerificationFalsePositive),
	).Scan(&rows).Error; err != nil {
		return nil, errors.New(err).
			Component("datastore").
			Category(errors.CategoryDatabase).
			Context("operation", "get_recent_species_data").
			Build()
	}

	result := make([]datastore.RecentSpeciesData, 0, len(rows))
	for i := range rows {
		row := &rows[i]
		scientificName := detection.ExtractScientificName(row.ScientificName)
		item := datastore.RecentSpeciesData{
			ScientificName:      scientificName,
			CommonName:          ds.resolveCommonName(scientificName),
			SpeciesCode:         ds.speciesCodeMap[scientificName],
			Bucket:              row.Bucket,
			Count:               row.Count,
			ConfidenceTotal:     row.ConfidenceTotal,
			BucketMaxConfidence: row.BucketMaxConfidence,
		}
		if row.LatestDetectedAt.Valid {
			item.LatestDetectedAt = time.Unix(row.LatestDetectedAt.Int64, 0).In(ds.timezone)
		}
		if row.LatestConfidence.Valid {
			item.LatestConfidence = row.LatestConfidence.Float64
		}
		if row.LatestDetectionID.Valid {
			item.LatestDetectionID = uint(row.LatestDetectionID.Int64)
		}
		result = append(result, item)
	}

	return result, nil
}
