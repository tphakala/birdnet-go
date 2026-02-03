package migration

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/tphakala/birdnet-go/internal/datastore"
	datastoreV2 "github.com/tphakala/birdnet-go/internal/datastore/v2"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/repository"
	"github.com/tphakala/birdnet-go/internal/detection"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// defaultRelatedDataBatchSize is the batch size for fetching related data during migration.
// Larger batches improve throughput for predictions migration.
const defaultRelatedDataBatchSize = 2000

// predictionsBatchSize is the batch size specifically for predictions migration.
// Predictions are simple INSERTs with ON CONFLICT DO NOTHING, so larger batches are efficient.
const predictionsBatchSize = 5000

// progressUpdateInterval controls how often we update progress in the database.
// Must be frequent enough for stable ETA calculation (rate = records / elapsed).
// If too infrequent, elapsed time increases between updates while records stays same,
// causing rate to appear to drop and ETA to bounce.
const progressUpdateInterval = 1

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
	stateManager  *datastoreV2.StateManager
	logger        logger.Logger
	batchSize     int

	// Cached lookup table IDs for label creation
	defaultModelID     uint  // Model ID to use for migrated labels
	speciesLabelTypeID uint  // "species" label type ID
	avesClassID        *uint // "Aves" taxonomic class ID (optional)
}

// RelatedDataMigratorConfig configures the related data migrator.
type RelatedDataMigratorConfig struct {
	LegacyStore   datastore.Interface
	DetectionRepo repository.DetectionRepository
	LabelRepo     repository.LabelRepository
	StateManager  *datastoreV2.StateManager // For progress tracking
	Logger        logger.Logger
	BatchSize     int // If 0, uses defaultRelatedDataBatchSize

	// Required: Cached lookup table IDs
	DefaultModelID     uint  // Model ID to use for migrated labels (typically default BirdNET)
	SpeciesLabelTypeID uint  // "species" label type ID
	AvesClassID        *uint // "Aves" taxonomic class ID (optional)
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
	if cfg.StateManager == nil {
		panic("RelatedDataMigratorConfig.StateManager cannot be nil")
	}
	batchSize := cfg.BatchSize
	if batchSize <= 0 {
		batchSize = defaultRelatedDataBatchSize
	}
	return &RelatedDataMigrator{
		legacyStore:        cfg.LegacyStore,
		detectionRepo:      cfg.DetectionRepo,
		labelRepo:          cfg.LabelRepo,
		stateManager:       cfg.StateManager,
		logger:             cfg.Logger,
		batchSize:          batchSize,
		defaultModelID:     cfg.DefaultModelID,
		speciesLabelTypeID: cfg.SpeciesLabelTypeID,
		avesClassID:        cfg.AvesClassID,
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

	// Set phase 2 at the START of related data migration
	// This ensures UI switches from "Phase 1: Detections" immediately when detections complete.
	// Reviews/comments/locks are fast and included in the predictions phase for simplicity.
	// Note: stateManager is guaranteed non-nil by constructor validation
	var totalPredictions int64
	if count, countErr := m.legacyStore.CountResults(); countErr != nil {
		m.logger.Warn("failed to count predictions for progress tracking", logger.Error(countErr))
		// Use 0 - progress will be indeterminate but phase will still switch
	} else {
		totalPredictions = count
		m.logger.Info("counted predictions for phase 2", logger.Int64("total_predictions", totalPredictions))
	}

	// Always set phase 2, even if count is 0 (progress will show 0/0 which is fine)
	m.logger.Info("setting migration phase to predictions",
		logger.String("phase", string(entities.MigrationPhasePredictions)),
		logger.Int("phase_number", datastoreV2.MigrationPhaseNumberPredictions),
		logger.Int("total_phases", datastoreV2.MigrationTotalPhases),
		logger.Int64("total_records", totalPredictions))
	if phaseErr := m.stateManager.SetPhaseWithProgress(entities.MigrationPhasePredictions, datastoreV2.MigrationPhaseNumberPredictions, datastoreV2.MigrationTotalPhases, totalPredictions); phaseErr != nil {
		m.logger.Warn("failed to set predictions phase progress", logger.Error(phaseErr))
	} else {
		m.logger.Info("successfully set migration phase to predictions")
	}

	var result MigrateResult

	// Migrate in order: reviews, comments, locks, predictions
	reviewsMigrated, reviewsErrors, reviewsSkipped, err := m.migrateReviews(ctx)
	if err != nil {
		return fmt.Errorf("reviews migration failed: %w", err)
	}
	result.ReviewsMigrated = reviewsMigrated
	result.BatchErrors += reviewsErrors
	result.SkippedRecords += reviewsSkipped

	commentsMigrated, commentsErrors, commentsSkipped, err := m.migrateComments(ctx)
	if err != nil {
		return fmt.Errorf("comments migration failed: %w", err)
	}
	result.CommentsMigrated = commentsMigrated
	result.BatchErrors += commentsErrors
	result.SkippedRecords += commentsSkipped

	locksMigrated, locksErrors, locksSkipped, err := m.migrateLocks(ctx)
	if err != nil {
		return fmt.Errorf("locks migration failed: %w", err)
	}
	result.LocksMigrated = locksMigrated
	result.BatchErrors += locksErrors
	result.SkippedRecords += locksSkipped

	predictionsMigrated, predictionsErrors, predictionsSkipped, err := m.migratePredictions(ctx)
	if err != nil {
		return fmt.Errorf("predictions migration failed: %w", err)
	}
	result.PredictionsMigrated = predictionsMigrated
	result.BatchErrors += predictionsErrors
	result.SkippedRecords += predictionsSkipped

	m.logger.Info("related data migration complete",
		logger.Int("reviews", result.ReviewsMigrated),
		logger.Int("comments", result.CommentsMigrated),
		logger.Int("locks", result.LocksMigrated),
		logger.Int("predictions", result.PredictionsMigrated),
		logger.Int("batch_errors", result.BatchErrors),
		logger.Int("skipped_records", result.SkippedRecords))

	// Return error only if batch operations failed - skipped records are expected
	// (low confidence predictions, missing detections, etc. are intentionally filtered)
	if result.BatchErrors > 0 {
		return fmt.Errorf("related data migration completed with %d batch errors", result.BatchErrors)
	}

	return nil
}

// migrateReviews migrates all note reviews to detection reviews using batched retrieval.
// Returns (migrated count, batch errors count, skipped count).
func (m *RelatedDataMigrator) migrateReviews(ctx context.Context) (migrated, batchErrs, skipped int, err error) {
	var lastID uint

	for {
		// Check context cancellation
		if ctxErr := ctx.Err(); ctxErr != nil {
			return migrated, batchErrs, skipped, fmt.Errorf("context cancelled during reviews migration: %w", ctxErr)
		}

		// Fetch batch
		batch, fetchErr := m.legacyStore.GetReviewsBatch(lastID, m.batchSize)
		if fetchErr != nil {
			return migrated, batchErrs, skipped, fmt.Errorf("failed to fetch reviews batch (afterID=%d): %w", lastID, fetchErr)
		}

		if len(batch) == 0 {
			break // No more data
		}

		// Collect unique detection IDs from batch to check existence
		detectionIDSet := make(map[uint]struct{})
		for i := range batch {
			detectionIDSet[batch[i].NoteID] = struct{}{}
		}
		detectionIDs := slices.Collect(maps.Keys(detectionIDSet))

		// Filter to only IDs that exist in v2 detections table
		existingIDs, filterErr := m.detectionRepo.FilterExistingIDs(ctx, detectionIDs)
		if filterErr != nil {
			return migrated, batchErrs, skipped, fmt.Errorf("failed to filter existing detection IDs: %w", filterErr)
		}
		existingIDSet := make(map[uint]struct{}, len(existingIDs))
		for _, id := range existingIDs {
			existingIDSet[id] = struct{}{}
		}

		// Convert to V2 entities, skipping non-existent detections
		v2Reviews := make([]*entities.DetectionReview, 0, len(batch))
		for i := range batch {
			r := &batch[i]
			lastID = r.ID // Track last ID for next batch

			// Skip if detection doesn't exist in v2
			if _, exists := existingIDSet[r.NoteID]; !exists {
				skipped++
				continue
			}

			v2Reviews = append(v2Reviews, &entities.DetectionReview{
				DetectionID: r.NoteID,
				Verified:    entities.VerificationStatus(r.Verified),
				CreatedAt:   r.CreatedAt,
				UpdatedAt:   r.UpdatedAt,
			})
		}

		// Save batch if there are any valid reviews
		if len(v2Reviews) > 0 {
			if saveErr := m.detectionRepo.SaveReviewsBatch(ctx, v2Reviews); saveErr != nil {
				m.logger.Warn("failed to save reviews batch", logger.Error(saveErr))
				batchErrs++
			}
		}

		migrated += len(v2Reviews)

		// If we got fewer than batch size, we're done
		if len(batch) < m.batchSize {
			break
		}
	}

	if migrated > 0 || skipped > 0 {
		m.logger.Info("reviews migration completed",
			logger.Int("migrated", migrated),
			logger.Int("batch_errors", batchErrs),
			logger.Int("skipped", skipped))
	} else {
		m.logger.Debug("no reviews to migrate")
	}
	return migrated, batchErrs, skipped, nil
}

// migrateComments migrates all note comments to detection comments using batched retrieval.
// Returns (migrated count, batch errors count, skipped count).
func (m *RelatedDataMigrator) migrateComments(ctx context.Context) (migrated, batchErrs, skipped int, err error) {
	var lastID uint

	for {
		// Check context cancellation
		if ctxErr := ctx.Err(); ctxErr != nil {
			return migrated, batchErrs, skipped, fmt.Errorf("context cancelled during comments migration: %w", ctxErr)
		}

		// Fetch batch
		batch, fetchErr := m.legacyStore.GetCommentsBatch(lastID, m.batchSize)
		if fetchErr != nil {
			return migrated, batchErrs, skipped, fmt.Errorf("failed to fetch comments batch (afterID=%d): %w", lastID, fetchErr)
		}

		if len(batch) == 0 {
			break // No more data
		}

		// Collect unique detection IDs from batch to check existence
		detectionIDSet := make(map[uint]struct{})
		for i := range batch {
			detectionIDSet[batch[i].NoteID] = struct{}{}
		}
		detectionIDs := slices.Collect(maps.Keys(detectionIDSet))

		// Filter to only IDs that exist in v2 detections table
		existingIDs, filterErr := m.detectionRepo.FilterExistingIDs(ctx, detectionIDs)
		if filterErr != nil {
			return migrated, batchErrs, skipped, fmt.Errorf("failed to filter existing detection IDs: %w", filterErr)
		}
		existingIDSet := make(map[uint]struct{}, len(existingIDs))
		for _, id := range existingIDs {
			existingIDSet[id] = struct{}{}
		}

		// Convert to V2 entities, skipping non-existent detections
		v2Comments := make([]*entities.DetectionComment, 0, len(batch))
		for i := range batch {
			c := &batch[i]
			lastID = c.ID // Track last ID for next batch

			// Skip if detection doesn't exist in v2
			if _, exists := existingIDSet[c.NoteID]; !exists {
				skipped++
				continue
			}

			v2Comments = append(v2Comments, &entities.DetectionComment{
				DetectionID: c.NoteID,
				Entry:       c.Entry,
				CreatedAt:   c.CreatedAt,
				UpdatedAt:   c.UpdatedAt,
			})
		}

		// Save batch if there are any valid comments
		if len(v2Comments) > 0 {
			if saveErr := m.detectionRepo.SaveCommentsBatch(ctx, v2Comments); saveErr != nil {
				m.logger.Warn("failed to save comments batch", logger.Error(saveErr))
				batchErrs++
			}
		}

		migrated += len(v2Comments)

		if len(batch) < m.batchSize {
			break
		}
	}

	if migrated > 0 || skipped > 0 {
		m.logger.Info("comments migration completed",
			logger.Int("migrated", migrated),
			logger.Int("batch_errors", batchErrs),
			logger.Int("skipped", skipped))
	} else {
		m.logger.Debug("no comments to migrate")
	}
	return migrated, batchErrs, skipped, nil
}

// migrateLocks migrates all note locks to detection locks using batched retrieval.
// Returns (migrated count, batch errors count, skipped count).
func (m *RelatedDataMigrator) migrateLocks(ctx context.Context) (migrated, batchErrs, skipped int, err error) {
	var lastNoteID uint

	for {
		// Check context cancellation
		if ctxErr := ctx.Err(); ctxErr != nil {
			return migrated, batchErrs, skipped, fmt.Errorf("context cancelled during locks migration: %w", ctxErr)
		}

		// Fetch batch (locks use NoteID as the cursor since they don't have an ID field)
		batch, fetchErr := m.legacyStore.GetLocksBatch(lastNoteID, m.batchSize)
		if fetchErr != nil {
			return migrated, batchErrs, skipped, fmt.Errorf("failed to fetch locks batch (afterNoteID=%d): %w", lastNoteID, fetchErr)
		}

		if len(batch) == 0 {
			break // No more data
		}

		// Collect unique detection IDs from batch to check existence
		detectionIDSet := make(map[uint]struct{})
		for i := range batch {
			detectionIDSet[batch[i].NoteID] = struct{}{}
		}
		detectionIDs := slices.Collect(maps.Keys(detectionIDSet))

		// Filter to only IDs that exist in v2 detections table
		existingIDs, filterErr := m.detectionRepo.FilterExistingIDs(ctx, detectionIDs)
		if filterErr != nil {
			return migrated, batchErrs, skipped, fmt.Errorf("failed to filter existing detection IDs: %w", filterErr)
		}
		existingIDSet := make(map[uint]struct{}, len(existingIDs))
		for _, id := range existingIDs {
			existingIDSet[id] = struct{}{}
		}

		// Convert to V2 entities, skipping non-existent detections
		v2Locks := make([]*entities.DetectionLock, 0, len(batch))
		for i := range batch {
			l := &batch[i]
			lastNoteID = l.NoteID // Track last NoteID for next batch

			// Skip if detection doesn't exist in v2
			if _, exists := existingIDSet[l.NoteID]; !exists {
				skipped++
				continue
			}

			v2Locks = append(v2Locks, &entities.DetectionLock{
				DetectionID: l.NoteID,
				LockedAt:    l.LockedAt,
			})
		}

		// Save batch if there are any valid locks
		if len(v2Locks) > 0 {
			if saveErr := m.detectionRepo.SaveLocksBatch(ctx, v2Locks); saveErr != nil {
				m.logger.Warn("failed to save locks batch", logger.Error(saveErr))
				batchErrs++
			}
		}

		migrated += len(v2Locks)

		if len(batch) < m.batchSize {
			break
		}
	}

	if migrated > 0 || skipped > 0 {
		m.logger.Info("locks migration completed",
			logger.Int("migrated", migrated),
			logger.Int("batch_errors", batchErrs),
			logger.Int("skipped", skipped))
	} else {
		m.logger.Debug("no locks to migrate")
	}
	return migrated, batchErrs, skipped, nil
}

// collectDetectionIDs extracts unique detection IDs from a batch of results.
func collectDetectionIDs(batch []datastore.Results) []uint {
	idSet := make(map[uint]struct{}, len(batch))
	for i := range batch {
		idSet[batch[i].NoteID] = struct{}{}
	}
	ids := slices.Collect(maps.Keys(idSet))
	return ids
}

// minPredictionConfidence is the minimum confidence threshold for migrating predictions.
// Set to 0.2 (20%) to reduce table size by excluding predictions that are almost
// certainly incorrect. Predictions below this threshold provide no analytical value
// and would only increase storage requirements.
const minPredictionConfidence = 0.2

// filterBatchByConfidence returns only results that meet the minimum confidence threshold.
// This should be called BEFORE resolveSpeciesLabels to avoid creating orphaned labels.
func filterBatchByConfidence(batch []datastore.Results) []datastore.Results {
	filtered := make([]datastore.Results, 0, len(batch))
	for i := range batch {
		if batch[i].Confidence >= minPredictionConfidence {
			filtered = append(filtered, batch[i])
		}
	}
	return filtered
}

// resolveSpeciesLabels ensures all species in the batch have label IDs in the cache.
// It parses raw BirdNET species strings to extract the scientific name before creating labels.
// Uses batch resolution to minimize database round-trips (N species -> 2-3 queries instead of N).
func (m *RelatedDataMigrator) resolveSpeciesLabels(ctx context.Context, batch []datastore.Results, cache map[string]uint) {
	// Collect unique species not in cache, map raw species -> scientific name
	speciesMapping := make(map[string]string) // raw species -> scientific name

	for i := range batch {
		species := batch[i].Species
		if _, cached := cache[species]; cached {
			continue
		}
		if _, seen := speciesMapping[species]; seen {
			continue
		}

		// Parse the species string to extract the scientific name.
		// Raw BirdNET labels are in format "ScientificName_CommonName_Code" or "ScientificName_CommonName".
		// We need the scientific name for the label, not the raw string.
		parsed := detection.ParseSpeciesString(species)
		scientificName := parsed.ScientificName
		if scientificName == "" {
			scientificName = species // fallback to original if parsing fails
		}
		speciesMapping[species] = scientificName
	}

	if len(speciesMapping) == 0 {
		return
	}

	// Extract unique scientific names (multiple raw species may map to same scientific name)
	// Skip empty or whitespace-only names to avoid creating invalid labels
	scientificNames := make([]string, 0, len(speciesMapping))
	seen := make(map[string]struct{})
	for _, sciName := range speciesMapping {
		trimmed := strings.TrimSpace(sciName)
		if trimmed == "" {
			continue
		}
		if _, exists := seen[trimmed]; !exists {
			seen[trimmed] = struct{}{}
			scientificNames = append(scientificNames, trimmed)
		}
	}

	// Batch resolve all at once
	labels, err := m.labelRepo.BatchGetOrCreate(ctx, scientificNames, m.defaultModelID, m.speciesLabelTypeID, m.avesClassID)
	if err != nil {
		m.logger.Warn("failed to batch resolve labels", logger.Error(err))
		return
	}

	// Update cache: raw species -> label ID (via scientific name)
	for rawSpecies, sciName := range speciesMapping {
		if label, found := labels[sciName]; found {
			cache[rawSpecies] = label.ID
		}
	}
}

// predictionRankTracker tracks rank assignment for predictions within detections.
type predictionRankTracker struct {
	currentNoteID uint
	currentRank   int
}

// nextRank returns the next rank for a prediction, resetting for new detections.
func (t *predictionRankTracker) nextRank(noteID uint) int {
	if noteID != t.currentNoteID {
		t.currentNoteID = noteID
		t.currentRank = secondaryPredictionStartRank
	} else {
		t.currentRank++
	}
	return t.currentRank
}

// migratePredictions migrates all results to detection predictions using batched retrieval.
// Uses keyset pagination with dual cursor (note_id, id) to keep predictions grouped by detection,
// ensuring correct rank assignment even when predictions span multiple batches.
// Returns (migrated count, batch errors count, skipped records count).
func (m *RelatedDataMigrator) migratePredictions(ctx context.Context) (migrated, batchErrs, skipped int, err error) {
	var lastNoteID, lastResultID uint
	var rankTracker predictionRankTracker
	batchCount := 0
	speciesLabelCache := make(map[string]uint, 2000)

	m.logger.Info("starting predictions migration")

	for {
		if ctx.Err() != nil {
			return migrated, batchErrs, skipped, fmt.Errorf("context cancelled during predictions migration: %w", ctx.Err())
		}

		batch, fetchErr := m.legacyStore.GetResultsBatch(lastNoteID, lastResultID, predictionsBatchSize)
		if fetchErr != nil {
			return migrated, batchErrs, skipped, fmt.Errorf("failed to fetch results batch: %w", fetchErr)
		}
		if len(batch) == 0 {
			break
		}
		batchCount++

		// Get existing detection IDs to filter invalid foreign keys
		existingIDs, filterErr := m.detectionRepo.FilterExistingIDs(ctx, collectDetectionIDs(batch))
		if filterErr != nil {
			return migrated, batchErrs, skipped, fmt.Errorf("failed to filter existing detection IDs: %w", filterErr)
		}
		existingIDSet := make(map[uint]struct{}, len(existingIDs))
		for _, id := range existingIDs {
			existingIDSet[id] = struct{}{}
		}

		// Filter batch to only include predictions that meet minimum confidence threshold
		// This MUST happen before label resolution to avoid creating orphaned labels
		filteredBatch := filterBatchByConfidence(batch)

		// Resolve species labels for filtered batch only
		m.resolveSpeciesLabels(ctx, filteredBatch, speciesLabelCache)

		// Convert batch to v2 predictions
		v2Predictions, batchSkipped, newLastNoteID, newLastResultID := m.convertResultsToPredictions(
			batch, existingIDSet, speciesLabelCache, &rankTracker)
		lastNoteID, lastResultID = newLastNoteID, newLastResultID

		// Save batch
		if len(v2Predictions) > 0 {
			if saveErr := m.detectionRepo.SavePredictionsBatch(ctx, v2Predictions); saveErr != nil {
				m.logger.Warn("failed to save predictions batch", logger.Error(saveErr))
				batchErrs++
			}
		}

		migrated += len(v2Predictions)
		skipped += batchSkipped

		// Update progress periodically
		m.updatePredictionsProgress(batchCount, migrated, skipped)

		if len(batch) < predictionsBatchSize {
			break
		}
	}

	// Final progress update to ensure UI shows completion
	// Use migrated+skipped as "processed" for accurate progress percentage
	if m.stateManager != nil {
		processed := int64(migrated + skipped)
		if updateErr := m.stateManager.UpdateProgress(0, processed); updateErr != nil {
			m.logger.Warn("failed to update final predictions progress", logger.Error(updateErr))
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

// convertResultsToPredictions converts a batch of legacy results to v2 predictions.
// Returns the converted predictions, skip count, and updated pagination cursors.
func (m *RelatedDataMigrator) convertResultsToPredictions(
	batch []datastore.Results,
	existingIDSet map[uint]struct{},
	labelCache map[string]uint,
	rankTracker *predictionRankTracker,
) (predictions []*entities.DetectionPrediction, skipped int, lastNoteID, lastResultID uint) {
	predictions = make([]*entities.DetectionPrediction, 0, len(batch))

	for i := range batch {
		r := &batch[i]
		lastNoteID, lastResultID = r.NoteID, r.ID

		// Skip if detection doesn't exist in v2
		if _, exists := existingIDSet[r.NoteID]; !exists {
			skipped++
			continue
		}

		// Skip if label resolution failed
		labelID, hasLabel := labelCache[r.Species]
		if !hasLabel {
			skipped++
			continue
		}

		// Skip low-confidence predictions (< 10%)
		if r.Confidence < 0.1 {
			skipped++
			continue
		}

		predictions = append(predictions, &entities.DetectionPrediction{
			DetectionID: r.NoteID,
			LabelID:     labelID,
			Confidence:  float64(r.Confidence),
			Rank:        rankTracker.nextRank(r.NoteID),
		})
	}

	return predictions, skipped, lastNoteID, lastResultID
}

// updatePredictionsProgress updates progress in state manager and logs periodically.
func (m *RelatedDataMigrator) updatePredictionsProgress(batchCount, migrated, skipped int) {
	const progressLogInterval = 50

	// Update state manager for UI (every N batches to reduce DB writes)
	if m.stateManager != nil && batchCount%progressUpdateInterval == 0 {
		processed := int64(migrated + skipped)
		if err := m.stateManager.UpdateProgress(0, processed); err != nil {
			m.logger.Warn("failed to update predictions progress", logger.Error(err))
		}
	}

	// Log progress periodically
	if batchCount%progressLogInterval == 0 {
		m.logger.Info("predictions migration progress",
			logger.Int("batches_processed", batchCount),
			logger.Int("migrated", migrated),
			logger.Int("skipped", skipped))
	}
}
