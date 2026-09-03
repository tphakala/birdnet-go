package datastore

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/tphakala/birdnet-go/internal/datastore/entities"
	"github.com/tphakala/birdnet-go/internal/errors"
)

// RecentSpeciesData contains one species' aggregate values for one time bucket.
// Latest detection fields are populated only on the bucket containing that
// species' latest detection in the requested window.
type RecentSpeciesData struct {
	ScientificName      string
	CommonName          string
	SpeciesCode         string
	Bucket              int
	Count               int
	ConfidenceTotal     float64
	BucketMaxConfidence float64
	LatestDetectedAt    time.Time
	LatestConfidence    float64
	LatestDetectionID   uint
}

type recentSpeciesQueryRow struct {
	ScientificName      string          `gorm:"column:scientific_name"`
	CommonName          string          `gorm:"column:common_name"`
	SpeciesCode         string          `gorm:"column:species_code"`
	Bucket              int             `gorm:"column:bucket"`
	Count               int             `gorm:"column:detection_count"`
	ConfidenceTotal     float64         `gorm:"column:confidence_total"`
	BucketMaxConfidence float64         `gorm:"column:bucket_max_confidence"`
	LatestDate          sql.NullString  `gorm:"column:latest_date"`
	LatestTime          sql.NullString  `gorm:"column:latest_time"`
	LatestConfidence    sql.NullFloat64 `gorm:"column:latest_confidence"`
	LatestDetectionID   sql.NullInt64   `gorm:"column:latest_detection_id"`
}

// GetRecentSpeciesData returns bucketed species aggregates for the inclusive
// [start, end] time window. It performs one query and excludes false positives.
func (ds *DataStore) GetRecentSpeciesData(ctx context.Context, start, end time.Time, minConfidence float64, buckets int) ([]RecentSpeciesData, error) {
	windowSeconds := int64(end.Sub(start) / time.Second)
	if buckets <= 0 || windowSeconds <= 0 {
		return nil, errors.Newf("invalid recent species window: buckets=%d duration=%s", buckets, end.Sub(start)).
			Component("datastore").
			Category(errors.CategoryValidation).
			Build()
	}

	timestampExpr, err := recentSpeciesTimestampExpression(ds.DB.Name())
	if err != nil {
		return nil, err
	}
	bucketExpr, bucketArgs := recentSpeciesBucketExpression(start, end, buckets)

	query := fmt.Sprintf(`
		WITH filtered AS (
			SELECT
				n.id,
				COALESCE(NULLIF(n.scientific_name, ''), n.common_name) AS species_key,
				n.scientific_name,
				n.common_name,
				n.species_code,
				n.date AS detection_date,
				n.time AS detection_time,
				n.confidence,
				%s AS detected_at
			FROM notes n
			WHERE n.date >= ? AND n.date <= ?
				AND %s >= ? AND %s <= ?
				AND n.confidence >= ?
				AND COALESCE(NULLIF(n.scientific_name, ''), n.common_name) <> ''
				AND NOT EXISTS (
					SELECT 1 FROM note_reviews nr
					WHERE nr.note_id = n.id AND nr.verified = ?
				)
		),
		ranked AS (
			SELECT filtered.*,
				ROW_NUMBER() OVER (
					PARTITION BY species_key
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
			MAX(common_name) AS common_name,
			MAX(species_code) AS species_code,
			bucket,
			COUNT(*) AS detection_count,
			SUM(confidence) AS confidence_total,
			MAX(confidence) AS bucket_max_confidence,
			MAX(CASE WHEN latest_rank = 1 THEN detection_date END) AS latest_date,
			MAX(CASE WHEN latest_rank = 1 THEN detection_time END) AS latest_time,
			MAX(CASE WHEN latest_rank = 1 THEN confidence END) AS latest_confidence,
			MAX(CASE WHEN latest_rank = 1 THEN id END) AS latest_detection_id
		FROM bucketed
		GROUP BY species_key, bucket
		ORDER BY species_key, bucket
	`, timestampExpr, timestampExpr, timestampExpr, bucketExpr)

	startValue := start.Format(time.DateTime)
	endValue := end.Format(time.DateTime)
	queryArgs := make([]any, 0, 6+len(bucketArgs))
	queryArgs = append(queryArgs,
		start.Format(time.DateOnly),
		end.Format(time.DateOnly),
		startValue,
		endValue,
		minConfidence,
		string(entities.VerificationFalsePositive),
	)
	queryArgs = append(queryArgs, bucketArgs...)

	var rows []recentSpeciesQueryRow
	if err := ds.DB.WithContext(ctx).Raw(query, queryArgs...).Scan(&rows).Error; err != nil {
		return nil, errors.New(err).
			Component("datastore").
			Category(errors.CategoryDatabase).
			Context("operation", "get_recent_species_data").
			Build()
	}

	result := make([]RecentSpeciesData, 0, len(rows))
	for i := range rows {
		row := &rows[i]
		item := RecentSpeciesData{
			ScientificName:      row.ScientificName,
			CommonName:          row.CommonName,
			SpeciesCode:         row.SpeciesCode,
			Bucket:              row.Bucket,
			Count:               row.Count,
			ConfidenceTotal:     row.ConfidenceTotal,
			BucketMaxConfidence: row.BucketMaxConfidence,
		}
		if row.LatestDate.Valid && row.LatestTime.Valid {
			latestDetectedAt, err := time.ParseInLocation(
				time.DateTime,
				row.LatestDate.String+" "+row.LatestTime.String,
				start.Location(),
			)
			if err != nil {
				return nil, errors.New(err).
					Component("datastore").
					Category(errors.CategoryDatabase).
					Context("operation", "parse_recent_species_timestamp").
					Build()
			}
			item.LatestDetectedAt = latestDetectedAt
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

func recentSpeciesTimestampExpression(dialect string) (string, error) {
	switch dialect {
	case DialectSQLite:
		return "datetime(n.date || ' ' || n.time)", nil
	case DialectMySQL:
		return "STR_TO_DATE(CONCAT(n.date, ' ', n.time), '%Y-%m-%d %H:%i:%s')", nil
	default:
		return "", errors.Newf("unsupported database dialect %q", dialect).
			Component("datastore").
			Category(errors.CategoryValidation).
			Build()
	}
}

// recentSpeciesBucketExpression builds second-precision boundaries from actual
// elapsed time. Comparing the stored wall-clock timestamps to these boundaries
// preserves bucket widths when the local clock crosses a DST transition.
func recentSpeciesBucketExpression(start, end time.Time, buckets int) (expression string, args []any) {
	var query strings.Builder
	query.WriteString("CASE")
	args = make([]any, 0, buckets-1)
	window := end.Sub(start)
	for bucket := range buckets - 1 {
		offset := window * time.Duration(bucket+1) / time.Duration(buckets)
		boundary := start.Add(offset)
		if boundary.Nanosecond() != 0 {
			boundary = boundary.Add(time.Second).Truncate(time.Second)
		}
		fmt.Fprintf(&query, " WHEN detected_at < ? THEN %d", bucket)
		args = append(args, boundary.Format(time.DateTime))
	}
	fmt.Fprintf(&query, " ELSE %d END", buckets-1)
	return query.String(), args
}
