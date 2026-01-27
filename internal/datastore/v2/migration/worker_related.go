package migration

import (
	"context"
	"fmt"
	"slices"

	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/repository"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// relatedDataBatchSize is the batch size for fetching related data during migration.
// Smaller than detection batch size since related data tables are typically smaller.
const relatedDataBatchSize = 500

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
// Returns error if any migration fails - caller decides whether to treat as fatal.
func (m *RelatedDataMigrator) MigrateAll(ctx context.Context) error {
	if m.legacyStore == nil {
		m.logger.Debug("no legacy store provided, skipping related data migration")
		return nil
	}

	// Check context before starting
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("context cancelled before migration: %w", err)
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

// migrateReviews migrates all note reviews to detection reviews using batched retrieval.
func (m *RelatedDataMigrator) migrateReviews(ctx context.Context) error {
	var totalMigrated, totalProcessed int
	var lastID uint

	for {
		// Check context cancellation
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("context cancelled during reviews migration: %w", err)
		}

		// Fetch batch
		batch, err := m.legacyStore.GetReviewsBatch(lastID, relatedDataBatchSize)
		if err != nil {
			return fmt.Errorf("failed to fetch reviews batch (afterID=%d): %w", lastID, err)
		}

		if len(batch) == 0 {
			break // No more data
		}

		// Convert to V2 entities
		v2Reviews := make([]*entities.DetectionReview, 0, len(batch))
		for i := range batch {
			r := &batch[i]
			v2Reviews = append(v2Reviews, &entities.DetectionReview{
				DetectionID: r.NoteID,
				Verified:    entities.VerificationStatus(r.Verified),
				CreatedAt:   r.CreatedAt,
				UpdatedAt:   r.UpdatedAt,
			})
			lastID = r.ID // Track last ID for next batch
		}

		// Save batch
		if err := m.detectionRepo.SaveReviewsBatch(ctx, v2Reviews); err != nil {
			m.logger.Warn("failed to save reviews batch", logger.Error(err))
			// Continue with next batch - ON CONFLICT DO NOTHING handles duplicates
		}

		totalProcessed += len(batch)
		totalMigrated += len(v2Reviews)

		// If we got fewer than batch size, we're done
		if len(batch) < relatedDataBatchSize {
			break
		}
	}

	if totalProcessed > 0 {
		m.logger.Info("reviews migration completed",
			logger.Int("processed", totalProcessed),
			logger.Int("attempted", totalMigrated))
	} else {
		m.logger.Debug("no reviews to migrate")
	}
	return nil
}

// migrateComments migrates all note comments to detection comments using batched retrieval.
func (m *RelatedDataMigrator) migrateComments(ctx context.Context) error {
	var totalMigrated, totalProcessed int
	var lastID uint

	for {
		// Check context cancellation
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("context cancelled during comments migration: %w", err)
		}

		// Fetch batch
		batch, err := m.legacyStore.GetCommentsBatch(lastID, relatedDataBatchSize)
		if err != nil {
			return fmt.Errorf("failed to fetch comments batch (afterID=%d): %w", lastID, err)
		}

		if len(batch) == 0 {
			break // No more data
		}

		// Convert to V2 entities
		v2Comments := make([]*entities.DetectionComment, 0, len(batch))
		for i := range batch {
			c := &batch[i]
			v2Comments = append(v2Comments, &entities.DetectionComment{
				DetectionID: c.NoteID,
				Entry:       c.Entry,
				CreatedAt:   c.CreatedAt,
				UpdatedAt:   c.UpdatedAt,
			})
			lastID = c.ID // Track last ID for next batch
		}

		// Save batch
		if err := m.detectionRepo.SaveCommentsBatch(ctx, v2Comments); err != nil {
			m.logger.Warn("failed to save comments batch", logger.Error(err))
		}

		totalProcessed += len(batch)
		totalMigrated += len(v2Comments)

		if len(batch) < relatedDataBatchSize {
			break
		}
	}

	if totalProcessed > 0 {
		m.logger.Info("comments migration completed",
			logger.Int("processed", totalProcessed),
			logger.Int("attempted", totalMigrated))
	} else {
		m.logger.Debug("no comments to migrate")
	}
	return nil
}

// migrateLocks migrates all note locks to detection locks using batched retrieval.
func (m *RelatedDataMigrator) migrateLocks(ctx context.Context) error {
	var totalMigrated, totalProcessed int
	var lastNoteID uint

	for {
		// Check context cancellation
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("context cancelled during locks migration: %w", err)
		}

		// Fetch batch (locks use NoteID as the cursor since they don't have an ID field)
		batch, err := m.legacyStore.GetLocksBatch(lastNoteID, relatedDataBatchSize)
		if err != nil {
			return fmt.Errorf("failed to fetch locks batch (afterNoteID=%d): %w", lastNoteID, err)
		}

		if len(batch) == 0 {
			break // No more data
		}

		// Convert to V2 entities
		v2Locks := make([]*entities.DetectionLock, 0, len(batch))
		for i := range batch {
			l := &batch[i]
			v2Locks = append(v2Locks, &entities.DetectionLock{
				DetectionID: l.NoteID,
				LockedAt:    l.LockedAt,
			})
			lastNoteID = l.NoteID // Track last NoteID for next batch
		}

		// Save batch
		if err := m.detectionRepo.SaveLocksBatch(ctx, v2Locks); err != nil {
			m.logger.Warn("failed to save locks batch", logger.Error(err))
		}

		totalProcessed += len(batch)
		totalMigrated += len(v2Locks)

		if len(batch) < relatedDataBatchSize {
			break
		}
	}

	if totalProcessed > 0 {
		m.logger.Info("locks migration completed",
			logger.Int("processed", totalProcessed),
			logger.Int("attempted", totalMigrated))
	} else {
		m.logger.Debug("no locks to migrate")
	}
	return nil
}

// migratePredictions migrates all results to detection predictions using batched retrieval.
func (m *RelatedDataMigrator) migratePredictions(ctx context.Context) error {
	var totalMigrated, totalProcessed, totalSkipped int
	var lastID uint

	for {
		// Check context cancellation
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("context cancelled during predictions migration: %w", err)
		}

		// Fetch batch
		batch, err := m.legacyStore.GetResultsBatch(lastID, relatedDataBatchSize)
		if err != nil {
			return fmt.Errorf("failed to fetch results batch (afterID=%d): %w", lastID, err)
		}

		if len(batch) == 0 {
			break // No more data
		}

		// Group results by NoteID to assign proper ranks
		resultsByNote := make(map[uint][]datastore.Results)
		for i := range batch {
			r := &batch[i]
			resultsByNote[r.NoteID] = append(resultsByNote[r.NoteID], *r)
			lastID = r.ID // Track last ID for next batch
		}

		// Sort note IDs for deterministic ordering
		noteIDs := make([]uint, 0, len(resultsByNote))
		for noteID := range resultsByNote {
			noteIDs = append(noteIDs, noteID)
		}
		slices.Sort(noteIDs)

		// Convert to V2 entities
		v2Predictions := make([]*entities.DetectionPrediction, 0, len(batch))
		var batchSkipped int
		for _, noteID := range noteIDs {
			results := resultsByNote[noteID]
			for rank, r := range results {
				// Resolve label for species name
				label, err := m.labelRepo.GetOrCreate(ctx, r.Species, entities.LabelTypeSpecies)
				if err != nil {
					m.logger.Debug("failed to resolve label for prediction",
						logger.String("species", r.Species),
						logger.Error(err))
					batchSkipped++
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

		// Save batch
		if err := m.detectionRepo.SavePredictionsBatch(ctx, v2Predictions); err != nil {
			m.logger.Warn("failed to save predictions batch", logger.Error(err))
		}

		totalProcessed += len(batch)
		totalMigrated += len(v2Predictions)
		totalSkipped += batchSkipped

		if len(batch) < relatedDataBatchSize {
			break
		}
	}

	if totalProcessed > 0 {
		m.logger.Info("predictions migration completed",
			logger.Int("processed", totalProcessed),
			logger.Int("attempted", totalMigrated),
			logger.Int("skipped", totalSkipped))
	} else {
		m.logger.Debug("no predictions to migrate")
	}
	return nil
}
