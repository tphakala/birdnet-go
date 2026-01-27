package migration

import (
	"context"
	"fmt"

	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/repository"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// RelatedDataMigrator handles migration of detection-related data
// (reviews, comments, locks, predictions) from legacy to V2.
type RelatedDataMigrator struct {
	legacyStore   datastore.Interface
	detectionRepo repository.DetectionRepository
	labelRepo     repository.LabelRepository
	logger        logger.Logger
}

// RelatedDataMigratorConfig configures the related data migrator.
type RelatedDataMigratorConfig struct {
	LegacyStore   datastore.Interface
	DetectionRepo repository.DetectionRepository
	LabelRepo     repository.LabelRepository
	Logger        logger.Logger
}

// NewRelatedDataMigrator creates a new related data migrator.
func NewRelatedDataMigrator(cfg *RelatedDataMigratorConfig) *RelatedDataMigrator {
	return &RelatedDataMigrator{
		legacyStore:   cfg.LegacyStore,
		detectionRepo: cfg.DetectionRepo,
		labelRepo:     cfg.LabelRepo,
		logger:        cfg.Logger,
	}
}

// MigrateAll migrates all related data from legacy to V2.
// Should be called after detection migration is complete.
func (m *RelatedDataMigrator) MigrateAll(ctx context.Context) error {
	if m.legacyStore == nil {
		m.logger.Debug("no legacy store provided, skipping related data migration")
		return nil
	}

	m.logger.Info("starting related data migration")

	// Migrate in order: reviews, comments, locks, predictions
	if err := m.migrateReviews(ctx); err != nil {
		return fmt.Errorf("reviews migration failed: %w", err)
	}

	if err := m.migrateComments(ctx); err != nil {
		return fmt.Errorf("comments migration failed: %w", err)
	}

	if err := m.migrateLocks(ctx); err != nil {
		return fmt.Errorf("locks migration failed: %w", err)
	}

	if err := m.migratePredictions(ctx); err != nil {
		return fmt.Errorf("predictions migration failed: %w", err)
	}

	m.logger.Info("related data migration completed")
	return nil
}

// migrateReviews migrates all note reviews to detection reviews.
func (m *RelatedDataMigrator) migrateReviews(ctx context.Context) error {
	legacyReviews, err := m.legacyStore.GetAllReviews()
	if err != nil {
		m.logger.Warn("failed to get legacy reviews", logger.Error(err))
		return nil // Non-fatal
	}

	if len(legacyReviews) == 0 {
		m.logger.Debug("no reviews to migrate")
		return nil
	}

	v2Reviews := make([]*entities.DetectionReview, 0, len(legacyReviews))
	for i := range legacyReviews {
		r := &legacyReviews[i]
		// NoteID maps directly to DetectionID (preserved during migration)
		v2Reviews = append(v2Reviews, &entities.DetectionReview{
			DetectionID: r.NoteID,
			Verified:    entities.VerificationStatus(r.Verified),
			CreatedAt:   r.CreatedAt,
			UpdatedAt:   r.UpdatedAt,
		})
	}

	if err := m.detectionRepo.SaveReviewsBatch(ctx, v2Reviews); err != nil {
		m.logger.Warn("failed to save some reviews", logger.Error(err))
	}

	m.logger.Info("reviews migration completed",
		logger.Int("total", len(legacyReviews)),
		logger.Int("migrated", len(v2Reviews)))
	return nil
}

// migrateComments migrates all note comments to detection comments.
func (m *RelatedDataMigrator) migrateComments(ctx context.Context) error {
	legacyComments, err := m.legacyStore.GetAllComments()
	if err != nil {
		m.logger.Warn("failed to get legacy comments", logger.Error(err))
		return nil // Non-fatal
	}

	if len(legacyComments) == 0 {
		m.logger.Debug("no comments to migrate")
		return nil
	}

	v2Comments := make([]*entities.DetectionComment, 0, len(legacyComments))
	for i := range legacyComments {
		c := &legacyComments[i]
		v2Comments = append(v2Comments, &entities.DetectionComment{
			DetectionID: c.NoteID,
			Entry:       c.Entry,
			CreatedAt:   c.CreatedAt,
			UpdatedAt:   c.UpdatedAt,
		})
	}

	if err := m.detectionRepo.SaveCommentsBatch(ctx, v2Comments); err != nil {
		m.logger.Warn("failed to save some comments", logger.Error(err))
	}

	m.logger.Info("comments migration completed",
		logger.Int("total", len(legacyComments)),
		logger.Int("migrated", len(v2Comments)))
	return nil
}

// migrateLocks migrates all note locks to detection locks.
func (m *RelatedDataMigrator) migrateLocks(ctx context.Context) error {
	legacyLocks, err := m.legacyStore.GetAllLocks()
	if err != nil {
		m.logger.Warn("failed to get legacy locks", logger.Error(err))
		return nil // Non-fatal
	}

	if len(legacyLocks) == 0 {
		m.logger.Debug("no locks to migrate")
		return nil
	}

	v2Locks := make([]*entities.DetectionLock, 0, len(legacyLocks))
	for i := range legacyLocks {
		l := &legacyLocks[i]
		v2Locks = append(v2Locks, &entities.DetectionLock{
			DetectionID: l.NoteID,
			LockedAt:    l.LockedAt,
		})
	}

	if err := m.detectionRepo.SaveLocksBatch(ctx, v2Locks); err != nil {
		m.logger.Warn("failed to save some locks", logger.Error(err))
	}

	m.logger.Info("locks migration completed",
		logger.Int("total", len(legacyLocks)),
		logger.Int("migrated", len(v2Locks)))
	return nil
}

// migratePredictions migrates all results to detection predictions.
func (m *RelatedDataMigrator) migratePredictions(ctx context.Context) error {
	legacyResults, err := m.legacyStore.GetAllResults()
	if err != nil {
		m.logger.Warn("failed to get legacy results", logger.Error(err))
		return nil // Non-fatal
	}

	if len(legacyResults) == 0 {
		m.logger.Debug("no predictions to migrate")
		return nil
	}

	// Group results by NoteID to assign proper ranks
	resultsByNote := make(map[uint][]datastore.Results)
	for i := range legacyResults {
		r := &legacyResults[i]
		resultsByNote[r.NoteID] = append(resultsByNote[r.NoteID], *r)
	}

	v2Predictions := make([]*entities.DetectionPrediction, 0, len(legacyResults))
	var skipped int
	for noteID, results := range resultsByNote {
		for rank, r := range results {
			// Resolve label for species name
			label, err := m.labelRepo.GetOrCreate(ctx, r.Species, entities.LabelTypeSpecies)
			if err != nil {
				m.logger.Debug("failed to resolve label for prediction",
					logger.String("species", r.Species),
					logger.Error(err))
				skipped++
				continue
			}

			v2Predictions = append(v2Predictions, &entities.DetectionPrediction{
				DetectionID: noteID,
				LabelID:     label.ID,
				Confidence:  float64(r.Confidence),
				Rank:        rank + 2, // Primary is rank 1 (in Detection), additional start at 2
			})
		}
	}

	if err := m.detectionRepo.SavePredictionsBatch(ctx, v2Predictions); err != nil {
		m.logger.Warn("failed to save some predictions", logger.Error(err))
	}

	m.logger.Info("predictions migration completed",
		logger.Int("total", len(legacyResults)),
		logger.Int("migrated", len(v2Predictions)),
		logger.Int("skipped", skipped))
	return nil
}
