package repository

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
)

// noiseExampleRepository implements NoiseExampleRepository.
type noiseExampleRepository struct {
	db          *gorm.DB
	metrics     *datastore.Metrics
	useV2Prefix bool
}

// NewNoiseExampleRepository creates a NoiseExampleRepository. The statements are
// dialect neutral, so the isMySQL argument of some other constructors is not
// taken.
func NewNoiseExampleRepository(db *gorm.DB, metrics *datastore.Metrics, useV2Prefix bool) NoiseExampleRepository {
	return &noiseExampleRepository{db: db, metrics: metrics, useV2Prefix: useV2Prefix}
}

func (r *noiseExampleRepository) table() string {
	if r.useV2Prefix {
		return tableV2NoiseExamples
	}
	return tableNoiseExamples
}

func (r *noiseExampleRepository) detectionsTable() string {
	if r.useV2Prefix {
		return tableV2Detections
	}
	return tableDetections
}

func (r *noiseExampleRepository) reviewsTable() string {
	if r.useV2Prefix {
		return tableV2DetectionReviews
	}
	return tableDetectionReviews
}

// insertRows inserts copies of rows owned by owner (nil for detached rows). The
// input is copied so a retry after a lock error starts from the caller's values.
func (r *noiseExampleRepository) insertRows(tx *gorm.DB, owner *uint, rows []entities.NoiseExample) error {
	if len(rows) == 0 {
		return nil
	}
	copies := make([]entities.NoiseExample, len(rows))
	for i := range rows {
		copies[i] = rows[i]
		copies[i].ID = 0
		copies[i].DetectionID = owner
		copies[i].Detection = nil
	}
	if err := tx.Table(r.table()).Create(&copies).Error; err != nil {
		return fmt.Errorf("insert noise examples: %w", err)
	}
	return nil
}

func (r *noiseExampleRepository) deleteAttached(tx *gorm.DB, detectionID uint) (int64, error) {
	res := tx.Table(r.table()).Where("detection_id = ?", detectionID).Delete(&entities.NoiseExample{})
	if res.Error != nil {
		return 0, fmt.Errorf("delete noise examples of detection: %w", res.Error)
	}
	return res.RowsAffected, nil
}

// ReplaceForDetection replaces the rows attached to one detection.
func (r *noiseExampleRepository) ReplaceForDetection(ctx context.Context, detectionID uint, rows []entities.NoiseExample) error {
	return datastore.RetryTransactionOnLock(ctx, r.db, "v2_replace_noise_examples", func(tx *gorm.DB) error {
		if _, err := r.deleteAttached(tx, detectionID); err != nil {
			return err
		}
		return r.insertRows(tx, &detectionID, rows)
	}, r.metrics)
}

// InsertDetached inserts rows that have no detection.
func (r *noiseExampleRepository) InsertDetached(ctx context.Context, rows []entities.NoiseExample) error {
	return datastore.RetryTransactionOnLock(ctx, r.db, "v2_insert_detached_noise_examples", func(tx *gorm.DB) error {
		return r.insertRows(tx, nil, rows)
	}, r.metrics)
}

// ListAll returns the whole table.
func (r *noiseExampleRepository) ListAll(ctx context.Context) ([]entities.NoiseExample, error) {
	var rows []entities.NoiseExample
	if err := r.db.WithContext(ctx).Table(r.table()).Order("id").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("list noise examples: %w", err)
	}
	return rows, nil
}

// DetachOrphans clears links to detections that are gone.
func (r *noiseExampleRepository) DetachOrphans(ctx context.Context) (int64, error) {
	var detached int64
	err := datastore.RetryOnLock(ctx, "v2_detach_noise_examples", func() error {
		res := r.db.WithContext(ctx).Exec(fmt.Sprintf(
			"UPDATE %s SET detection_id = NULL WHERE detection_id IS NOT NULL AND detection_id NOT IN (SELECT id FROM %s)",
			r.table(), r.detectionsTable()))
		if res.Error != nil {
			return fmt.Errorf("detach orphaned noise examples: %w", res.Error)
		}
		detached = res.RowsAffected
		return nil
	}, r.metrics)
	return detached, err
}

type spaceCount struct {
	EmbeddingSpace string
	Count          int64
}

func (r *noiseExampleRepository) countBySpace(query *gorm.DB) (map[string]int64, error) {
	var counts []spaceCount
	err := query.Table(r.table()).
		Select("embedding_space, COUNT(*) AS count").
		Group("embedding_space").
		Scan(&counts).Error
	if err != nil {
		return nil, fmt.Errorf("count noise examples: %w", err)
	}
	bySpace := make(map[string]int64, len(counts))
	for _, c := range counts {
		bySpace[c.EmbeddingSpace] = c.Count
	}
	return bySpace, nil
}

// CountForDetection counts the rows attached to one detection, by space.
func (r *noiseExampleRepository) CountForDetection(ctx context.Context, detectionID uint) (map[string]int64, error) {
	return r.countBySpace(r.db.WithContext(ctx).Where("detection_id = ?", detectionID))
}

// CountBySpace counts every row, by space.
func (r *noiseExampleRepository) CountBySpace(ctx context.Context) (map[string]int64, error) {
	return r.countBySpace(r.db.WithContext(ctx))
}

// DeleteAll deletes the whole table. The always true condition is how GORM is
// told the missing filter is intended.
func (r *noiseExampleRepository) DeleteAll(ctx context.Context) (int64, error) {
	var deleted int64
	err := datastore.RetryOnLock(ctx, "v2_delete_all_noise_examples", func() error {
		res := r.db.WithContext(ctx).Table(r.table()).Where("1 = 1").Delete(&entities.NoiseExample{})
		if res.Error != nil {
			return fmt.Errorf("delete all noise examples: %w", res.Error)
		}
		deleted = res.RowsAffected
		return nil
	}, r.metrics)
	return deleted, err
}

// RemoveForVerdict deletes the detection's examples and writes its review with the
// given status and no reason, in one transaction. It wraps the write the way
// SaveReview does: a locked database is retried and the repository metrics see it.
func (r *noiseExampleRepository) RemoveForVerdict(ctx context.Context, detectionID uint, verified entities.VerificationStatus) (int64, error) {
	var removed int64
	err := datastore.RetryTransactionOnLock(ctx, r.db, "v2_remove_noise_for_verdict", func(tx *gorm.DB) error {
		n, err := r.deleteAttached(tx, detectionID)
		if err != nil {
			return err
		}
		now := time.Now()
		review := entities.DetectionReview{DetectionID: detectionID, Verified: verified, UpdatedAt: now}
		err = tx.Table(r.reviewsTable()).
			Clauses(clause.OnConflict{
				Columns: []clause.Column{{Name: "detection_id"}},
				DoUpdates: clause.Assignments(map[string]any{
					"verified":   string(verified),
					columnReason: nil,
					"updated_at": now,
				}),
			}).
			Create(&review).Error
		if err != nil {
			return fmt.Errorf("write review: %w", err)
		}
		removed = n
		return nil
	}, r.metrics)
	return removed, err
}
