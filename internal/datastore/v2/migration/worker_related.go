package migration

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/tphakala/birdnet-go/internal/datastore"
	datastoreV2 "github.com/tphakala/birdnet-go/internal/datastore/v2"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/repository"
	"github.com/tphakala/birdnet-go/internal/detection"
	"github.com/tphakala/birdnet-go/internal/errors"
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

// MigrateResult contains statistics from the one-time related data migration.
// ReviewsMigrated counts inserted and updated reviews, LocksMigrated inserted locks.
type MigrateResult struct {
	ReviewsMigrated     int
	CommentsMigrated    int
	LocksMigrated       int
	PredictionsMigrated int
	SkippedRecords      int  // Count of individual records skipped by design
	Blocked             bool // A pass stopped at a detection that is not in v2 yet; the tail sync continues it
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

	// predictionsBatch is the number of legacy results read per batch.
	predictionsBatch int

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

	// PredictionsBatchSize is the number of legacy results read per batch.
	// If 0, uses predictionsBatchSize. Tests set it small to force batches
	// that split a detection's results.
	PredictionsBatchSize int

	// Required: Cached lookup table IDs
	DefaultModelID     uint  // Model ID to use for migrated labels (typically default BirdNET)
	SpeciesLabelTypeID uint  // "species" label type ID
	AvesClassID        *uint // "Aves" taxonomic class ID (optional)
}

// NewRelatedDataMigrator creates a new related data migrator.
// BatchSize and PredictionsBatchSize default when zero or negative.
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
	predictionsBatch := cfg.PredictionsBatchSize
	if predictionsBatch <= 0 {
		predictionsBatch = predictionsBatchSize
	}
	return &RelatedDataMigrator{
		legacyStore:        cfg.LegacyStore,
		detectionRepo:      cfg.DetectionRepo,
		labelRepo:          cfg.LabelRepo,
		stateManager:       cfg.StateManager,
		logger:             cfg.Logger,
		batchSize:          batchSize,
		predictionsBatch:   predictionsBatch,
		defaultModelID:     cfg.DefaultModelID,
		speciesLabelTypeID: cfg.SpeciesLabelTypeID,
		avesClassID:        cfg.AvesClassID,
	}
}

// MigrateAll migrates all related data from legacy to V2.
// Should be called after detection migration is complete.
// It binds the sync cursors to the current migration attempt and resumes from
// them, so a rerun for the same attempt adds nothing that is already in v2.
// Rows whose detection is expected in v2 but not there yet are left for the
// tail sync, which continues from the same cursors; that is not an error.
// Returns an error if any phase failed - caller decides severity.
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

	cursors, err := m.stateManager.BeginRelatedSync()
	if err != nil {
		return fmt.Errorf("failed to begin related data sync: %w", err)
	}
	state, err := m.stateManager.GetState()
	if err != nil {
		return fmt.Errorf("failed to read migration state: %w", err)
	}
	lastMigratedID := state.LastMigratedID
	scanStart := time.Now()

	var (
		result MigrateResult
		errs   []error
	)

	// Migrate in order: reviews, comments, locks, predictions. A failed phase
	// does not stop the others, unless the context ended.
	reviewsInserted, reviewsUpdated, err := m.reconcileReviews(ctx)
	result.ReviewsMigrated = reviewsInserted + reviewsUpdated
	reviewsOK := err == nil
	if err != nil {
		errs = append(errs, fmt.Errorf("reviews migration failed: %w", err))
	}

	if ctx.Err() == nil {
		commentsCopied, commentsSkipped, commentsBlocked, commentsErr := m.syncComments(ctx, cursors.CommentID, lastMigratedID)
		result.CommentsMigrated = commentsCopied
		result.SkippedRecords += commentsSkipped
		result.Blocked = result.Blocked || commentsBlocked
		if commentsErr != nil {
			errs = append(errs, fmt.Errorf("comments migration failed: %w", commentsErr))
		}
	}

	locksOK := false
	if ctx.Err() == nil {
		locksInserted, _, locksErr := m.reconcileLocks(ctx)
		result.LocksMigrated = locksInserted
		locksOK = locksErr == nil
		if locksErr != nil {
			errs = append(errs, fmt.Errorf("locks migration failed: %w", locksErr))
		}
	}

	if ctx.Err() == nil {
		predictions, predictionsErr := m.syncPredictions(ctx, cursors, lastMigratedID, true)
		result.PredictionsMigrated = predictions.copied
		result.SkippedRecords += predictions.skipped
		result.Blocked = result.Blocked || predictions.blocked
		if predictionsErr != nil {
			errs = append(errs, fmt.Errorf("predictions migration failed: %w", predictionsErr))
		}
	}

	if reviewsOK && locksOK {
		if recErr := m.stateManager.SetRelatedReconciledAt(scanStart); recErr != nil {
			errs = append(errs, fmt.Errorf("failed to record reconcile time: %w", recErr))
		}
	}

	m.logger.Info("related data migration complete",
		logger.Int("reviews", result.ReviewsMigrated),
		logger.Int("comments", result.CommentsMigrated),
		logger.Int("locks", result.LocksMigrated),
		logger.Int("predictions", result.PredictionsMigrated),
		logger.Int("skipped_records", result.SkippedRecords),
		logger.Bool("blocked", result.Blocked),
		logger.Int("failed_phases", len(errs)))

	// Skipped records are expected (low confidence predictions, missing
	// detections, etc. are intentionally filtered); only failures are errors.
	return errors.Join(errs...)
}

// collectDetectionIDs extracts unique detection IDs from a batch of results.
func collectDetectionIDs(batch []datastore.Results) []uint {
	idSet := make(map[uint]struct{}, len(batch))
	for i := range batch {
		idSet[batch[i].NoteID] = struct{}{}
	}
	return slices.Collect(maps.Keys(idSet))
}

// minPredictionConfidence is the minimum confidence threshold for migrating predictions.
// Set to 0.3 (30%) to reduce table size by excluding secondary predictions that are
// unlikely to be correct. They provide little analytical value and would only
// increase storage requirements.
const minPredictionConfidence = 0.3

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
// A label lookup failure is returned so the caller stops before advancing past
// rows it could not place.
func (m *RelatedDataMigrator) resolveSpeciesLabels(ctx context.Context, batch []datastore.Results, cache map[string]uint) error {
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
		return nil
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
		return fmt.Errorf("failed to batch resolve labels: %w", err)
	}

	// Update cache: raw species -> label ID (via scientific name)
	for rawSpecies, sciName := range speciesMapping {
		if label, found := labels[sciName]; found {
			cache[rawSpecies] = label.ID
		}
	}
	return nil
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

// convertResultsToPredictions converts a batch of legacy results to v2 predictions.
// Rows whose detection is not in existingIDSet, whose label did not resolve or
// whose confidence is too low are counted as skipped.
func (m *RelatedDataMigrator) convertResultsToPredictions(
	batch []datastore.Results,
	existingIDSet map[uint]struct{},
	labelCache map[string]uint,
	rankTracker *predictionRankTracker,
) (predictions []*entities.DetectionPrediction, skipped int) {
	predictions = make([]*entities.DetectionPrediction, 0, len(batch))

	for i := range batch {
		r := &batch[i]

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

		// Skip rows below the threshold that filterBatchByConfidence applied
		// before label resolution, so a row is kept or dropped the same way
		// whether or not its species happens to be cached already.
		if r.Confidence < minPredictionConfidence {
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

	return predictions, skipped
}

// noteBoundaryCursor returns the (note id, result id) of the last row of rows
// at which the predictions cursor may be persisted. A resumed pass recomputes
// the ranks of a note from its first result, so the cursor must sit on the last
// row of a complete note: the last row of the last note that is followed by a
// row of a different note. When complete is true no row follows rows (the read
// returned fewer rows than asked, or the pass stopped right after them), so the
// last row qualifies. ok is false when rows hold only part of one note.
func noteBoundaryCursor(rows []datastore.Results, complete bool) (noteID, resultID uint, ok bool) {
	if len(rows) == 0 {
		return 0, 0, false
	}
	last := rows[len(rows)-1]
	if complete {
		return last.NoteID, last.ID, true
	}
	for i := len(rows) - 2; i >= 0; i-- {
		if rows[i].NoteID != last.NoteID {
			return rows[i].NoteID, rows[i].ID, true
		}
	}
	return 0, 0, false
}

// updatePredictionsProgress updates progress in state manager and logs periodically.
func (m *RelatedDataMigrator) updatePredictionsProgress(batchCount, migrated, skipped int) {
	const progressLogInterval = 50

	// Update state manager for UI (every N batches to reduce DB writes)
	if m.stateManager != nil && batchCount%progressUpdateInterval == 0 {
		processed := int64(migrated + skipped)
		if err := m.stateManager.UpdatePhaseProgress(processed); err != nil {
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
