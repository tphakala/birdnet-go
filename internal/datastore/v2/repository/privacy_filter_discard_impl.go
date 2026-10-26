package repository

import (
	"context"
	"fmt"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
)

// privacyFilterDiscardRepository implements PrivacyFilterDiscardRepository.
type privacyFilterDiscardRepository struct {
	db          *gorm.DB
	metrics     *datastore.Metrics
	useV2Prefix bool
}

// NewPrivacyFilterDiscardRepository creates a new PrivacyFilterDiscardRepository.
// The statements are dialect neutral, so the isMySQL argument of the other
// repository constructors is not taken.
func NewPrivacyFilterDiscardRepository(db *gorm.DB, metrics *datastore.Metrics, useV2Prefix bool) PrivacyFilterDiscardRepository {
	return &privacyFilterDiscardRepository{
		db:          db,
		metrics:     metrics,
		useV2Prefix: useV2Prefix,
	}
}

func (r *privacyFilterDiscardRepository) discardTable() string {
	if r.useV2Prefix {
		return tableV2PrivacyFilterDiscards
	}
	return tablePrivacyFilterDiscards
}

func (r *privacyFilterDiscardRepository) hitTable() string {
	if r.useV2Prefix {
		return tableV2PrivacyFilterDiscardHits
	}
	return tablePrivacyFilterDiscardHits
}

// Save inserts the discard and its hits in one transaction. The rows are copied
// inside the transaction function, so a retry after a lock error starts clean and
// a rolled back attempt leaves no partial discard.
func (r *privacyFilterDiscardRepository) Save(ctx context.Context, discard *entities.PrivacyFilterDiscard) error {
	if discard == nil {
		return ErrPrivacyDiscardNil
	}
	var savedID uint
	err := datastore.RetryTransactionOnLock(ctx, r.db, "v2_save_privacy_filter_discard", func(tx *gorm.DB) error {
		row := *discard
		row.ID = 0
		row.Source = nil
		row.Hits = nil
		if err := tx.Table(r.discardTable()).Omit(clause.Associations).Create(&row).Error; err != nil {
			return fmt.Errorf("insert privacy filter discard: %w", err)
		}
		if len(discard.Hits) > 0 {
			hits := make([]entities.PrivacyFilterDiscardHit, len(discard.Hits))
			for i := range discard.Hits {
				hits[i] = discard.Hits[i]
				hits[i].ID = 0
				hits[i].DiscardID = row.ID
			}
			if err := tx.Table(r.hitTable()).Create(&hits).Error; err != nil {
				return fmt.Errorf("insert privacy filter discard hits: %w", err)
			}
		}
		savedID = row.ID
		return nil
	}, r.metrics)
	if err != nil {
		return err
	}
	discard.ID = savedID
	return nil
}

// DeleteBefore deletes discards older than cutoffMillis; the foreign key cascade
// removes their hit rows.
func (r *privacyFilterDiscardRepository) DeleteBefore(ctx context.Context, cutoffMillis int64) (int64, error) {
	var deleted int64
	err := datastore.RetryOnLock(ctx, "v2_delete_privacy_filter_discards", func() error {
		res := r.db.WithContext(ctx).Table(r.discardTable()).
			Where("discarded_at < ?", cutoffMillis).
			Delete(&entities.PrivacyFilterDiscard{})
		if res.Error != nil {
			return fmt.Errorf("delete old privacy filter discards: %w", res.Error)
		}
		deleted = res.RowsAffected
		return nil
	}, r.metrics)
	return deleted, err
}
