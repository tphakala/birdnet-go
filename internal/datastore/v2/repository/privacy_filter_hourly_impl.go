package repository

import (
	"context"
	"fmt"

	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	// maxFuncSQLite and maxFuncMySQL are the two-argument maximum functions of
	// the dialects.
	maxFuncSQLite = "max"
	maxFuncMySQL  = "GREATEST"

	// privacyHourlyMaxScoreColumn is the column that keeps a maximum instead of a sum.
	privacyHourlyMaxScoreColumn = "max_score"
)

// privacyHourlyKeyColumns are the columns of the unique index
// idx_privacy_filter_hourly_key, the conflict target of AddDelta.
var privacyHourlyKeyColumns = []clause.Column{
	{Name: "hour_start"}, {Name: "source_id"}, {Name: "model_id"}, {Name: "trigger_type"}, {Name: "threshold_milli"},
}

// privacyFilterHourlyRepository implements PrivacyFilterHourlyRepository.
type privacyFilterHourlyRepository struct {
	db          *gorm.DB
	metrics     *datastore.Metrics
	useV2Prefix bool
	isMySQL     bool
}

// NewPrivacyFilterHourlyRepository creates a new PrivacyFilterHourlyRepository.
func NewPrivacyFilterHourlyRepository(db *gorm.DB, metrics *datastore.Metrics, useV2Prefix, isMySQL bool) PrivacyFilterHourlyRepository {
	return &privacyFilterHourlyRepository{
		db:          db,
		metrics:     metrics,
		useV2Prefix: useV2Prefix,
		isMySQL:     isMySQL,
	}
}

func (r *privacyFilterHourlyRepository) tableName() string {
	if r.useV2Prefix {
		return tableV2PrivacyFilterHourlies
	}
	return tablePrivacyFilterHourlies
}

// AddDelta adds the counters of row to its key's row.
//
// Each assignment reads only its own column. MySQL evaluates ON DUPLICATE KEY
// UPDATE assignments left to right and lets later ones see earlier results,
// while SQLite reads the old row; self-referential assignments make both agree.
// The deltas are bound parameters, so VALUES() / excluded. is not used.
func (r *privacyFilterHourlyRepository) AddDelta(ctx context.Context, row *entities.PrivacyFilterHourly) error {
	if row == nil || row.SourceID == 0 {
		return ErrPrivacyRollupNoSource
	}
	maxFunc := maxFuncSQLite
	if r.isMySQL {
		maxFunc = maxFuncMySQL
	}
	sums := []struct {
		column string
		delta  int64
	}{
		{"hits", row.Hits}, {"held", row.Held}, {"ignored", row.Ignored}, {"confirmed", row.Confirmed},
		{"discards", row.Discards}, {"unscanned_saves", row.UnscannedSaves},
		{"margin_lt_50", row.MarginLt50}, {"margin_lt_100", row.MarginLt100},
		{"margin_lt_200", row.MarginLt200}, {"margin_ge_200", row.MarginGe200},
	}
	assignments := make(clause.Set, 0, len(sums)+1)
	for _, s := range sums {
		assignments = append(assignments, clause.Assignment{
			Column: clause.Column{Name: s.column},
			Value:  gorm.Expr("? + ?", clause.Column{Name: s.column}, s.delta),
		})
	}
	assignments = append(assignments, clause.Assignment{
		Column: clause.Column{Name: privacyHourlyMaxScoreColumn},
		Value:  gorm.Expr(maxFunc+"(?, ?)", clause.Column{Name: privacyHourlyMaxScoreColumn}, row.MaxScore),
	})

	return datastore.RetryOnLock(ctx, "v2_add_privacy_filter_hourly", func() error {
		// Create writes the ID back, so work on a copy. One autocommit statement
		// per call: a lock error then means the statement did not apply, which is
		// what makes the retry safe.
		insert := *row
		insert.ID = 0
		insert.Source = nil
		err := r.db.WithContext(ctx).Session(&gorm.Session{SkipDefaultTransaction: true}).
			Table(r.tableName()).
			Clauses(clause.OnConflict{Columns: privacyHourlyKeyColumns, DoUpdates: assignments}).
			Create(&insert).Error
		if err != nil {
			return fmt.Errorf("add privacy filter hourly delta: %w", err)
		}
		return nil
	}, r.metrics)
}

// DeleteBefore deletes rows older than cutoffHourStart.
func (r *privacyFilterHourlyRepository) DeleteBefore(ctx context.Context, cutoffHourStart int64) (int64, error) {
	var deleted int64
	err := datastore.RetryOnLock(ctx, "v2_delete_privacy_filter_hourly", func() error {
		res := r.db.WithContext(ctx).Table(r.tableName()).
			Where("hour_start < ?", cutoffHourStart).
			Delete(&entities.PrivacyFilterHourly{})
		if res.Error != nil {
			return fmt.Errorf("delete old privacy filter hourly rows: %w", res.Error)
		}
		deleted = res.RowsAffected
		return nil
	}, r.metrics)
	return deleted, err
}
