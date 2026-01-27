package migration

import (
	"context"
	"fmt"

	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/repository"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// defaultRelatedDataBatchSize is the batch size for fetching related data during migration.
// Smaller than detection batch size since related data tables are typically smaller.
const defaultRelatedDataBatchSize = 500

// secondaryPredictionStartRank is the starting rank for additional predictions in migration.
// The primary prediction has rank 1 (stored in Detection entity), so secondary predictions start at 2.
const secondaryPredictionStartRank = 2

// MigrateResult contains statistics from related data migration.
type MigrateResult struct {
	ReviewsMigrated     int
	CommentsMigrated    int
	LocksMigrated       int
	PredictionsMigrated int
	BatchErrors         int // Count of failed batch save operations
	SkippedRecords      int // Count of individual records skipped due to errors
}

// RelatedDataMigrator handles migration of detection-related data
// (reviews, comments, locks, predictions) from legacy to V2.
type RelatedDataMigrator struct {
	legacyStore   datastore.Interface
	detectionRepo repository.DetectionRepository
	labelRepo     repository.LabelRepository
	logger        logger.Logger
	batchSize     int
}

// RelatedDataMigratorConfig configures the related data migrator.
type RelatedDataMigratorConfig struct {
	LegacyStore   datastore.Interface
	DetectionRepo repository.DetectionRepository
	LabelRepo     repository.LabelRepository
	Logger        logger.Logger
	BatchSize     int // If 0, uses defaultRelatedDataBatchSize
}

// NewRelatedDataMigrator creates a new related data migrator.
// Panics if cfg or required dependencies are nil since they are essential for migration.
func NewRelatedDataMigrator(cfg *RelatedDataMigratorConfig) *RelatedDataMigrator {
	if cfg == nil {
		panic("RelatedDataMigratorConfig cannot be nil")
	}
	if cfg.Logger == nil {
		panic("RelatedDataMigratorConfig.Logger cannot be nil")
	}
	if cfg.DetectionRepo == nil {
		panic("RelatedDataMigratorConfig.DetectionRepo cannot be nil")
	}
	if cfg.LabelRepo == nil {
		panic("RelatedDataMigratorConfig.LabelRepo cannot be nil")
	}
	batchSize := cfg.BatchSize
	if batchSize <= 0 {
		batchSize = defaultRelatedDataBatchSize
	}
	return &RelatedDataMigrator{
		legacyStore:   cfg.LegacyStore,
		detectionRepo: cfg.DetectionRepo,
		labelRepo:     cfg.LabelRepo,
		logger:        cfg.Logger,
		batchSize:     batchSize,
	}
}

// MigrateAll migrates all related data from legacy to V2.
// Should be called after detection migration is complete.
// Returns error if any migration fails or if batch errors occurred - caller decides severity.
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

	var result MigrateResult

	// Migrate in order: reviews, comments, locks, predictions
	reviewsMigrated, reviewsErrors, err := m.migrateReviews(ctx)
	if err != nil {
		return fmt.Errorf("reviews migration failed: %w", err)
	}
	result.ReviewsMigrated = reviewsMigrated
	result.BatchErrors += reviewsErrors

	commentsMigrated, commentsErrors, err := m.migrateComments(ctx)
	if err != nil {
		return fmt.Errorf("comments migration failed: %w", err)
	}
	result.CommentsMigrated = commentsMigrated
	result.BatchErrors += commentsErrors

	locksMigrated, locksErrors, err := m.migrateLocks(ctx)
	if err != nil {
		return fmt.Errorf("locks migration failed: %w", err)
	}
	result.LocksMigrated = locksMigrated
	result.BatchErrors += locksErrors

	predictionsMigrated, predictionsErrors, predictionsSkipped, err := m.migratePredictions(ctx)
	if err != nil {
		return fmt.Errorf("predictions migration failed: %w", err)
	}
	result.PredictionsMigrated = predictionsMigrated
	result.BatchErrors += predictionsErrors
	result.SkippedRecords = predictionsSkipped

	m.logger.Info("related data migration complete",
		logger.Int("reviews", result.ReviewsMigrated),
		logger.Int("comments", result.CommentsMigrated),
		logger.Int("locks", result.LocksMigrated),
		logger.Int("predictions", result.PredictionsMigrated),
		logger.Int("batch_errors", result.BatchErrors),
		logger.Int("skipped_records", result.SkippedRecords))

	// Return error if any batch operations failed so caller can track in migration state
	// This ensures SetRelatedDataError is called and UI shows warning to operators
	if result.BatchErrors > 0 || result.SkippedRecords > 0 {
		return fmt.Errorf("related data migration completed with %d batch errors and %d skipped records",
			result.BatchErrors, result.SkippedRecords)
	}

	return nil
}

// migrateReviews migrates all note reviews to detection reviews using batched retrieval.
// Returns (migrated count, batch errors count).
func (m *RelatedDataMigrator) migrateReviews(ctx context.Context) (migrated, batchErrs int, err error) {
	var lastID uint

	for {
		// Check context cancellation
		if ctxErr := ctx.Err(); ctxErr != nil {
			return migrated, batchErrs, fmt.Errorf("context cancelled during reviews migration: %w", ctxErr)
		}

		// Fetch batch
		batch, fetchErr := m.legacyStore.GetReviewsBatch(lastID, m.batchSize)
		if fetchErr != nil {
			return migrated, batchErrs, fmt.Errorf("failed to fetch reviews batch (afterID=%d): %w", lastID, fetchErr)
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

		// Save batch - errors are logged but tracked since:
		// 1. ON CONFLICT DO NOTHING handles duplicates from re-runs
		// 2. Individual batch failures don't invalidate other batches
		// 3. Migration can be re-run to catch any missed records
		// 4. Batch errors are now tracked and reported to operators
		if saveErr := m.detectionRepo.SaveReviewsBatch(ctx, v2Reviews); saveErr != nil {
			m.logger.Warn("failed to save reviews batch", logger.Error(saveErr))
			batchErrs++
		}

		migrated += len(batch)

		// If we got fewer than batch size, we're done
		if len(batch) < m.batchSize {
			break
		}
	}

	if migrated > 0 {
		m.logger.Info("reviews migration completed",
			logger.Int("migrated", migrated),
			logger.Int("batch_errors", batchErrs))
	} else {
		m.logger.Debug("no reviews to migrate")
	}
	return migrated, batchErrs, nil
}

// migrateComments migrates all note comments to detection comments using batched retrieval.
// Returns (migrated count, batch errors count).
func (m *RelatedDataMigrator) migrateComments(ctx context.Context) (migrated, batchErrs int, err error) {
	var lastID uint

	for {
		// Check context cancellation
		if ctxErr := ctx.Err(); ctxErr != nil {
			return migrated, batchErrs, fmt.Errorf("context cancelled during comments migration: %w", ctxErr)
		}

		// Fetch batch
		batch, fetchErr := m.legacyStore.GetCommentsBatch(lastID, m.batchSize)
		if fetchErr != nil {
			return migrated, batchErrs, fmt.Errorf("failed to fetch comments batch (afterID=%d): %w", lastID, fetchErr)
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

		// Save batch - errors are logged but tracked since:
		// 1. ON CONFLICT DO NOTHING handles duplicates from re-runs
		// 2. Individual batch failures don't invalidate other batches
		// 3. Migration can be re-run to catch any missed records
		// 4. Batch errors are now tracked and reported to operators
		if saveErr := m.detectionRepo.SaveCommentsBatch(ctx, v2Comments); saveErr != nil {
			m.logger.Warn("failed to save comments batch", logger.Error(saveErr))
			batchErrs++
		}

		migrated += len(batch)

		if len(batch) < m.batchSize {
			break
		}
	}

	if migrated > 0 {
		m.logger.Info("comments migration completed",
			logger.Int("migrated", migrated),
			logger.Int("batch_errors", batchErrs))
	} else {
		m.logger.Debug("no comments to migrate")
	}
	return migrated, batchErrs, nil
}

// migrateLocks migrates all note locks to detection locks using batched retrieval.
// Returns (migrated count, batch errors count).
func (m *RelatedDataMigrator) migrateLocks(ctx context.Context) (migrated, batchErrs int, err error) {
	var lastNoteID uint

	for {
		// Check context cancellation
		if ctxErr := ctx.Err(); ctxErr != nil {
			return migrated, batchErrs, fmt.Errorf("context cancelled during locks migration: %w", ctxErr)
		}

		// Fetch batch (locks use NoteID as the cursor since they don't have an ID field)
		batch, fetchErr := m.legacyStore.GetLocksBatch(lastNoteID, m.batchSize)
		if fetchErr != nil {
			return migrated, batchErrs, fmt.Errorf("failed to fetch locks batch (afterNoteID=%d): %w", lastNoteID, fetchErr)
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

		// Save batch - errors are logged but tracked since:
		// 1. ON CONFLICT DO NOTHING handles duplicates from re-runs
		// 2. Individual batch failures don't invalidate other batches
		// 3. Migration can be re-run to catch any missed records
		// 4. Batch errors are now tracked and reported to operators
		if saveErr := m.detectionRepo.SaveLocksBatch(ctx, v2Locks); saveErr != nil {
			m.logger.Warn("failed to save locks batch", logger.Error(saveErr))
			batchErrs++
		}

		migrated += len(batch)

		if len(batch) < m.batchSize {
			break
		}
	}

	if migrated > 0 {
		m.logger.Info("locks migration completed",
			logger.Int("migrated", migrated),
			logger.Int("batch_errors", batchErrs))
	} else {
		m.logger.Debug("no locks to migrate")
	}
	return migrated, batchErrs, nil
}

// migratePredictions migrates all results to detection predictions using batched retrieval.
// Uses keyset pagination with dual cursor (note_id, id) to keep predictions grouped by detection,
// ensuring correct rank assignment even when predictions span multiple batches.
// Returns (migrated count, batch errors count, skipped records count).
func (m *RelatedDataMigrator) migratePredictions(ctx context.Context) (migrated, batchErrs, skipped int, err error) {
	// Keyset pagination cursors - dual cursor ensures correct ordering
	var lastNoteID, lastResultID uint

	// Rank tracking - only need current state since data is contiguous by note_id
	var currentRankNoteID uint
	var currentRank int

	for {
		// Check context cancellation
		if ctxErr := ctx.Err(); ctxErr != nil {
			return migrated, batchErrs, skipped, fmt.Errorf("context cancelled during predictions migration: %w", ctxErr)
		}

		// Fetch batch using keyset pagination with dual cursor
		batch, fetchErr := m.legacyStore.GetResultsBatch(lastNoteID, lastResultID, m.batchSize)
		if fetchErr != nil {
			return migrated, batchErrs, skipped, fmt.Errorf("failed to fetch results batch (afterNoteID=%d, afterResultID=%d): %w",
				lastNoteID, lastResultID, fetchErr)
		}

		if len(batch) == 0 {
			break // No more data
		}

		// Convert to V2 entities
		v2Predictions := make([]*entities.DetectionPrediction, 0, len(batch))
		var batchSkipped int

		for i := range batch {
			r := &batch[i]

			// Update pagination cursors
			lastNoteID = r.NoteID
			lastResultID = r.ID

			// Calculate rank - reset if new note, increment if same note
			if r.NoteID != currentRankNoteID {
				currentRankNoteID = r.NoteID
				currentRank = secondaryPredictionStartRank // Primary prediction is rank 1 (in Detection)
			} else {
				currentRank++
			}

			// Resolve label for species name
			label, labelErr := m.labelRepo.GetOrCreate(ctx, r.Species, entities.LabelTypeSpecies)
			if labelErr != nil {
				// Log at Warn level so failures are visible in production logs
				m.logger.Warn("failed to resolve label for prediction, skipping",
					logger.String("species", r.Species),
					logger.Error(labelErr))
				batchSkipped++
				continue
			}

			v2Predictions = append(v2Predictions, &entities.DetectionPrediction{
				DetectionID: r.NoteID,
				LabelID:     label.ID,
				Confidence:  float64(r.Confidence),
				Rank:        currentRank,
			})
		}

		// Save batch - errors are logged but tracked since:
		// 1. ON CONFLICT DO NOTHING handles duplicates from re-runs
		// 2. Individual batch failures don't invalidate other batches
		// 3. Migration can be re-run to catch any missed records
		// 4. Batch errors are now tracked and reported to operators
		if saveErr := m.detectionRepo.SavePredictionsBatch(ctx, v2Predictions); saveErr != nil {
			m.logger.Warn("failed to save predictions batch", logger.Error(saveErr))
			batchErrs++
		}

		migrated += len(v2Predictions)
		skipped += batchSkipped

		if len(batch) < m.batchSize {
			break
		}
	}

	if migrated > 0 || skipped > 0 {
		m.logger.Info("predictions migration completed",
			logger.Int("migrated", migrated),
			logger.Int("batch_errors", batchErrs),
			logger.Int("skipped", skipped))
	} else {
		m.logger.Debug("no predictions to migrate")
	}
	return migrated, batchErrs, skipped, nil
}
