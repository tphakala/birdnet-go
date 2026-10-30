package migration

import (
	"context"
	"fmt"
	"maps"
	"math"
	"slices"
	"time"

	"github.com/tphakala/birdnet-go/internal/datastore"
	datastoreV2 "github.com/tphakala/birdnet-go/internal/datastore/v2"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// relatedReconcileBatchSize is the number of legacy rows read per batch when
// reconciling reviews and locks. The rows are small, so a table of 50k reviews
// takes about 50 reads.
const relatedReconcileBatchSize = 1000

// predictionsLabelCacheSize is the initial capacity of the species label cache
// used while syncing predictions.
const predictionsLabelCacheSize = 2000

// Operation names for log fields of the related data sync.
const (
	operationRelatedTailSync = "related_tail_sync"
)

// TailSyncResult reports what one SyncTail call did.
type TailSyncResult struct {
	CommentsCopied    int  // Comments saved to v2
	PredictionsCopied int  // Predictions saved to v2
	ReviewsInserted   int  // Reviews missing in v2 that were inserted
	ReviewsUpdated    int  // Reviews whose verdict differed and was updated
	LocksInserted     int  // Locks missing in v2 that were inserted
	LocksRemoved      int  // v2 locks the legacy store no longer has, unlocked
	Blocked           bool // A pass stopped at a detection that is not in v2 yet
	FullScanCompleted bool // Reviews and locks were reconciled completely
}

// predictionsPass is the outcome of one syncPredictions call.
type predictionsPass struct {
	copied  int  // Predictions saved
	skipped int  // Rows skipped by design (orphan, low confidence, unresolved label)
	blocked bool // The pass stopped at a detection that is not in v2 yet
}

// SyncTail copies related data written to the legacy store since the last sync
// to v2. Comments and predictions continue from the persisted cursors; when
// fullScan is true, locks and reviews are reconciled against the whole legacy
// tables as well. It writes neither the migration phase nor the progress
// counters. It returns datastoreV2.ErrRelatedSyncNotRecorded when the related
// data was not copied by this version for the current migration attempt.
// Phases that fail do not stop the others; the errors are joined.
func (m *RelatedDataMigrator) SyncTail(ctx context.Context, fullScan bool) (TailSyncResult, error) {
	var result TailSyncResult
	if m.legacyStore == nil {
		return result, nil
	}

	state, err := m.stateManager.GetState()
	if err != nil {
		return result, fmt.Errorf("failed to read migration state: %w", err)
	}
	cursors, ok := state.RelatedSyncCursors()
	if !ok {
		return result, datastoreV2.ErrRelatedSyncNotRecorded
	}

	var errs []error
	copied, _, blocked, err := m.syncComments(ctx, cursors.CommentID, state.LastMigratedID)
	result.CommentsCopied = copied
	result.Blocked = blocked
	if err != nil {
		errs = append(errs, fmt.Errorf("comments sync failed: %w", err))
	}

	if ctx.Err() == nil {
		predictions, predErr := m.syncPredictions(ctx, cursors, state.LastMigratedID, false)
		result.PredictionsCopied = predictions.copied
		result.Blocked = result.Blocked || predictions.blocked
		if predErr != nil {
			errs = append(errs, fmt.Errorf("predictions sync failed: %w", predErr))
		}
	}

	if fullScan && ctx.Err() == nil {
		scanStart := time.Now()
		var locksErr, reviewsErr error
		result.LocksInserted, result.LocksRemoved, locksErr = m.reconcileLocks(ctx)
		if locksErr != nil {
			errs = append(errs, fmt.Errorf("locks reconcile failed: %w", locksErr))
		}
		if ctx.Err() == nil {
			result.ReviewsInserted, result.ReviewsUpdated, reviewsErr = m.reconcileReviews(ctx)
			if reviewsErr != nil {
				errs = append(errs, fmt.Errorf("reviews reconcile failed: %w", reviewsErr))
			}
		}
		if locksErr == nil && reviewsErr == nil && ctx.Err() == nil {
			if recErr := m.stateManager.SetRelatedReconciledAt(scanStart); recErr != nil {
				errs = append(errs, fmt.Errorf("failed to record reconcile time: %w", recErr))
			} else {
				result.FullScanCompleted = true
			}
		}
	}

	m.logTailSync(&result, fullScan)
	return result, errors.Join(errs...)
}

// logTailSync logs a tail sync pass: Info when it changed something, Debug
// otherwise. Ids and counts only.
func (m *RelatedDataMigrator) logTailSync(result *TailSyncResult, fullScan bool) {
	changed := result.CommentsCopied + result.PredictionsCopied + result.ReviewsInserted +
		result.ReviewsUpdated + result.LocksInserted + result.LocksRemoved
	fields := []logger.Field{
		logger.String("operation", operationRelatedTailSync),
		logger.Bool("full_scan", fullScan),
		logger.Int("comments", result.CommentsCopied),
		logger.Int("predictions", result.PredictionsCopied),
		logger.Int("reviews_inserted", result.ReviewsInserted),
		logger.Int("reviews_updated", result.ReviewsUpdated),
		logger.Int("locks_inserted", result.LocksInserted),
		logger.Int("locks_removed", result.LocksRemoved),
		logger.Bool("blocked", result.Blocked),
	}
	if changed > 0 {
		m.logger.Info("related data tail sync copied rows", fields...)
		return
	}
	m.logger.Debug("related data tail sync found nothing to copy", fields...)
}

// idSet builds a set from ids.
func idSet(ids []uint) map[uint]struct{} {
	set := make(map[uint]struct{}, len(ids))
	for _, id := range ids {
		set[id] = struct{}{}
	}
	return set
}

// classifyNotes splits noteIDs into those present in v2 and those that are
// expected there but are not yet: a note above the detections watermark, or one
// on the dirty list (its migration failed and is retried). A note that is in
// neither set is an orphan and is skipped for good.
func (m *RelatedDataMigrator) classifyNotes(ctx context.Context, noteIDs []uint, lastMigratedID uint) (existing, pending map[uint]struct{}, err error) {
	existingIDs, err := m.detectionRepo.FilterExistingIDs(ctx, noteIDs)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to filter existing detection IDs: %w", err)
	}
	existing = idSet(existingIDs)

	pending = make(map[uint]struct{})
	var belowWatermark []uint
	for _, id := range noteIDs {
		if _, ok := existing[id]; ok {
			continue
		}
		if id > lastMigratedID {
			pending[id] = struct{}{}
		} else {
			belowWatermark = append(belowWatermark, id)
		}
	}
	dirty, err := m.stateManager.FilterDirtyIDs(belowWatermark)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to look up dirty detection IDs: %w", err)
	}
	for _, id := range dirty {
		pending[id] = struct{}{}
	}
	return existing, pending, nil
}

// syncComments copies legacy comments after cursor to v2 and persists the
// cursor. A comment whose detection is expected in v2 but is not there yet
// stops the pass at that comment (blocked), so the next pass retries it. A save
// or cursor error stops the pass without advancing the cursor past unsaved rows.
func (m *RelatedDataMigrator) syncComments(ctx context.Context, cursor, lastMigratedID uint) (copied, skipped int, blocked bool, err error) {
	for {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return copied, skipped, blocked, fmt.Errorf("context cancelled during comments sync: %w", ctxErr)
		}

		batch, fetchErr := m.legacyStore.GetCommentsBatch(cursor, m.batchSize)
		if fetchErr != nil {
			return copied, skipped, blocked, fmt.Errorf("failed to fetch comments batch (afterID=%d): %w", cursor, fetchErr)
		}
		if len(batch) == 0 {
			break
		}

		noteIDs := make([]uint, 0, len(batch))
		for i := range batch {
			noteIDs = append(noteIDs, batch[i].NoteID)
		}
		existing, pending, classifyErr := m.classifyNotes(ctx, slices.Compact(slices.Sorted(slices.Values(noteIDs))), lastMigratedID)
		if classifyErr != nil {
			return copied, skipped, blocked, classifyErr
		}

		handled := cursor
		comments := make([]*entities.DetectionComment, 0, len(batch))
		for i := range batch {
			c := &batch[i]
			if _, ok := existing[c.NoteID]; ok {
				legacyID := c.ID
				comments = append(comments, &entities.DetectionComment{
					DetectionID: c.NoteID,
					Entry:       c.Entry,
					CreatedAt:   c.CreatedAt,
					UpdatedAt:   c.UpdatedAt,
					LegacyID:    &legacyID,
				})
			} else if _, ok := pending[c.NoteID]; ok {
				blocked = true
				break
			} else {
				skipped++
			}
			handled = c.ID
		}

		if len(comments) > 0 {
			if saveErr := m.detectionRepo.SaveCommentsBatch(ctx, comments); saveErr != nil {
				return copied, skipped, blocked, fmt.Errorf("failed to save comments batch: %w", saveErr)
			}
			copied += len(comments)
		}
		if handled != cursor {
			if setErr := m.stateManager.SetRelatedCommentsCursor(handled); setErr != nil {
				return copied, skipped, blocked, fmt.Errorf("failed to persist comments cursor: %w", setErr)
			}
			cursor = handled
		}

		if blocked || len(batch) < m.batchSize {
			break
		}
	}

	if copied > 0 || skipped > 0 || blocked {
		m.logger.Info("comments sync completed",
			logger.Int("copied", copied),
			logger.Int("skipped", skipped),
			logger.Bool("blocked", blocked))
	}
	return copied, skipped, blocked, nil
}

// predictionsBatchStep places the rows of one legacy results batch. It returns
// the prefix of batch that was handled (saved or skipped for good); a row of a
// detection that is expected in v2 but not there yet ends the prefix and sets
// blocked. Label resolution and save errors are returned.
func (m *RelatedDataMigrator) predictionsBatchStep(
	ctx context.Context,
	batch []datastore.Results,
	lastMigratedID uint,
	labelCache map[string]uint,
	rankTracker *predictionRankTracker,
) (handled []datastore.Results, saved, skipped int, blocked bool, err error) {
	existing, pending, err := m.classifyNotes(ctx, collectDetectionIDs(batch), lastMigratedID)
	if err != nil {
		return nil, 0, 0, false, err
	}

	handled = batch
	for i := range batch {
		noteID := batch[i].NoteID
		if _, ok := existing[noteID]; ok {
			continue
		}
		if _, ok := pending[noteID]; ok {
			handled = batch[:i]
			blocked = true
			break
		}
	}
	if len(handled) == 0 {
		return handled, 0, 0, blocked, nil
	}

	// Filter by confidence BEFORE label resolution to avoid creating orphaned labels.
	if resolveErr := m.resolveSpeciesLabels(ctx, filterBatchByConfidence(handled), labelCache); resolveErr != nil {
		return nil, 0, 0, false, resolveErr
	}

	predictions, skipped := m.convertResultsToPredictions(handled, existing, labelCache, rankTracker)
	if len(predictions) > 0 {
		if saveErr := m.detectionRepo.SavePredictionsBatch(ctx, predictions); saveErr != nil {
			return nil, 0, 0, false, fmt.Errorf("failed to save predictions batch: %w", saveErr)
		}
	}
	return handled, len(predictions), skipped, blocked, nil
}

// syncPredictions copies legacy results after the (note id, result id) cursor
// in from to v2, using keyset pagination so a detection's results stay together
// and ranks are assigned in order. The persisted cursor only moves to the last
// row of a complete detection, so a resumed pass recomputes ranks from the
// detection's first result. Blocking and error rules are those of syncComments.
// reportProgress updates the migration phase progress (one-time copy only; the
// tail sync must not touch the detections progress counters).
func (m *RelatedDataMigrator) syncPredictions(ctx context.Context, from entities.RelatedSyncCursors, lastMigratedID uint, reportProgress bool) (pass predictionsPass, err error) {
	lastNoteID, lastResultID := from.PredictionNoteID, from.PredictionResultID
	var rankTracker predictionRankTracker
	labelCache := make(map[string]uint, predictionsLabelCacheSize)
	batchCount := 0

	for {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return pass, fmt.Errorf("context cancelled during predictions sync: %w", ctxErr)
		}

		batch, fetchErr := m.legacyStore.GetResultsBatch(lastNoteID, lastResultID, m.predictionsBatch)
		if fetchErr != nil {
			return pass, fmt.Errorf("failed to fetch results batch: %w", fetchErr)
		}
		if len(batch) == 0 {
			break
		}
		batchCount++

		handled, saved, skipped, blocked, stepErr := m.predictionsBatchStep(ctx, batch, lastMigratedID, labelCache, &rankTracker)
		if stepErr != nil {
			return pass, stepErr
		}
		pass.copied += saved
		pass.skipped += skipped
		pass.blocked = blocked

		if len(handled) > 0 {
			last := handled[len(handled)-1]
			lastNoteID, lastResultID = last.NoteID, last.ID
			complete := blocked || len(batch) < m.predictionsBatch
			if noteID, resultID, ok := noteBoundaryCursor(handled, complete); ok {
				if setErr := m.stateManager.SetRelatedPredictionsCursor(noteID, resultID); setErr != nil {
					return pass, fmt.Errorf("failed to persist predictions cursor: %w", setErr)
				}
			}
		}

		if reportProgress {
			m.updatePredictionsProgress(batchCount, pass.copied, pass.skipped)
		}
		if blocked || len(batch) < m.predictionsBatch {
			break
		}
	}

	if reportProgress {
		// Use copied+skipped as "processed" for accurate progress percentage
		if updateErr := m.stateManager.UpdatePhaseProgress(int64(pass.copied + pass.skipped)); updateErr != nil {
			m.logger.Warn("failed to update final predictions progress", logger.Error(updateErr))
		}
	}
	if pass.copied > 0 || pass.skipped > 0 || pass.blocked {
		m.logger.Info("predictions sync completed",
			logger.Int("copied", pass.copied),
			logger.Int("skipped", pass.skipped),
			logger.Bool("blocked", pass.blocked))
	}
	return pass, nil
}

// reconcileReviews makes v2 match the legacy reviews: missing reviews are
// inserted and reviews whose verdict differs are updated. Reviews are edited in
// place in the legacy store, so an id cursor cannot see edits; the whole table
// is compared on every call and only differences are written. A review whose
// detection is not in v2 yet is picked up by a later call. It stops at the first
// error.
func (m *RelatedDataMigrator) reconcileReviews(ctx context.Context) (inserted, updated int, err error) {
	var lastID uint
	for {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return inserted, updated, fmt.Errorf("context cancelled during reviews reconcile: %w", ctxErr)
		}

		batch, fetchErr := m.legacyStore.GetReviewsBatch(lastID, relatedReconcileBatchSize)
		if fetchErr != nil {
			return inserted, updated, fmt.Errorf("failed to fetch reviews batch (afterID=%d): %w", lastID, fetchErr)
		}
		if len(batch) == 0 {
			break
		}
		lastID = batch[len(batch)-1].ID

		batchInserted, batchUpdated, batchErr := m.reconcileReviewBatch(ctx, batch)
		inserted += batchInserted
		updated += batchUpdated
		if batchErr != nil {
			return inserted, updated, batchErr
		}
		if len(batch) < relatedReconcileBatchSize {
			break
		}
	}

	if inserted > 0 || updated > 0 {
		m.logger.Info("reviews reconcile completed",
			logger.Int("inserted", inserted),
			logger.Int("updated", updated))
	}
	return inserted, updated, nil
}

// reconcileReviewBatch compares one batch of legacy reviews with v2.
func (m *RelatedDataMigrator) reconcileReviewBatch(ctx context.Context, batch []datastore.NoteReview) (inserted, updated int, err error) {
	noteIDs := make([]uint, 0, len(batch))
	for i := range batch {
		noteIDs = append(noteIDs, batch[i].NoteID)
	}
	stored, err := m.detectionRepo.GetReviewsByDetectionIDs(ctx, noteIDs)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to load v2 reviews: %w", err)
	}

	var missing []*datastore.NoteReview
	for i := range batch {
		r := &batch[i]
		current, ok := stored[r.NoteID]
		if !ok {
			missing = append(missing, r)
			continue
		}
		if current.Verified == entities.VerificationStatus(r.Verified) {
			continue
		}
		review := &entities.DetectionReview{
			DetectionID: r.NoteID,
			Verified:    entities.VerificationStatus(r.Verified),
			CreatedAt:   r.CreatedAt,
		}
		if saveErr := m.detectionRepo.SaveReview(ctx, review); saveErr != nil {
			return inserted, updated, fmt.Errorf("failed to update review: %w", saveErr)
		}
		updated++
	}

	if len(missing) == 0 {
		return inserted, updated, nil
	}
	missingIDs := make([]uint, 0, len(missing))
	for _, r := range missing {
		missingIDs = append(missingIDs, r.NoteID)
	}
	existingIDs, err := m.detectionRepo.FilterExistingIDs(ctx, missingIDs)
	if err != nil {
		return inserted, updated, fmt.Errorf("failed to filter existing detection IDs: %w", err)
	}
	existing := idSet(existingIDs)
	reviews := make([]*entities.DetectionReview, 0, len(missing))
	for _, r := range missing {
		if _, ok := existing[r.NoteID]; !ok {
			continue
		}
		reviews = append(reviews, &entities.DetectionReview{
			DetectionID: r.NoteID,
			Verified:    entities.VerificationStatus(r.Verified),
			CreatedAt:   r.CreatedAt,
			UpdatedAt:   r.UpdatedAt,
		})
	}
	if len(reviews) > 0 {
		if saveErr := m.detectionRepo.SaveReviewsBatch(ctx, reviews); saveErr != nil {
			return inserted, updated, fmt.Errorf("failed to save reviews batch: %w", saveErr)
		}
		inserted += len(reviews)
	}
	return inserted, updated, nil
}

// lockedIDsUpTo lists the detection IDs locked in v2 in the range (lo, hi],
// ascending, paging through the repository.
func (m *RelatedDataMigrator) lockedIDsUpTo(ctx context.Context, lo, hi uint) ([]uint, error) {
	var locked []uint
	after := lo
	for {
		ids, err := m.detectionRepo.GetLockedDetectionIDsAfter(ctx, after, relatedReconcileBatchSize)
		if err != nil {
			return nil, fmt.Errorf("failed to list v2 locks: %w", err)
		}
		for _, id := range ids {
			if id > hi {
				return locked, nil
			}
			locked = append(locked, id)
		}
		if len(ids) < relatedReconcileBatchSize {
			return locked, nil
		}
		after = ids[len(ids)-1]
	}
}

// reconcileLocks makes the v2 locks match the legacy locks: missing locks are
// inserted and v2 locks that the legacy store no longer has (the detection was
// unlocked) are removed. Legacy and v2 are walked in the same note id order, one
// legacy batch and the v2 range it covers at a time. A lock whose detection is
// not in v2 yet is picked up by a later call. It stops at the first error.
func (m *RelatedDataMigrator) reconcileLocks(ctx context.Context) (inserted, removed int, err error) {
	var lo uint
	for {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return inserted, removed, fmt.Errorf("context cancelled during locks reconcile: %w", ctxErr)
		}

		batch, fetchErr := m.legacyStore.GetLocksBatch(lo, relatedReconcileBatchSize)
		if fetchErr != nil {
			return inserted, removed, fmt.Errorf("failed to fetch locks batch (afterNoteID=%d): %w", lo, fetchErr)
		}
		last := len(batch) < relatedReconcileBatchSize
		hi := uint(math.MaxUint)
		if !last {
			hi = batch[len(batch)-1].NoteID
		}

		batchInserted, batchRemoved, batchErr := m.reconcileLockRange(ctx, batch, lo, hi)
		inserted += batchInserted
		removed += batchRemoved
		if batchErr != nil {
			return inserted, removed, batchErr
		}
		if last {
			break
		}
		lo = hi
	}

	if inserted > 0 || removed > 0 {
		m.logger.Info("locks reconcile completed",
			logger.Int("inserted", inserted),
			logger.Int("removed", removed))
	}
	return inserted, removed, nil
}

// reconcileLockRange reconciles the legacy locks in batch, which cover the note
// id range (lo, hi], with the v2 locks in the same range.
func (m *RelatedDataMigrator) reconcileLockRange(ctx context.Context, batch []datastore.NoteLock, lo, hi uint) (inserted, removed int, err error) {
	v2Locked, err := m.lockedIDsUpTo(ctx, lo, hi)
	if err != nil {
		return 0, 0, err
	}
	v2Set := idSet(v2Locked)

	legacySet := make(map[uint]struct{}, len(batch))
	var missing []*datastore.NoteLock
	for i := range batch {
		l := &batch[i]
		legacySet[l.NoteID] = struct{}{}
		if _, ok := v2Set[l.NoteID]; !ok {
			missing = append(missing, l)
		}
	}

	if len(missing) > 0 {
		missingIDs := make([]uint, 0, len(missing))
		for _, l := range missing {
			missingIDs = append(missingIDs, l.NoteID)
		}
		existingIDs, filterErr := m.detectionRepo.FilterExistingIDs(ctx, missingIDs)
		if filterErr != nil {
			return 0, 0, fmt.Errorf("failed to filter existing detection IDs: %w", filterErr)
		}
		existing := idSet(existingIDs)
		locks := make([]*entities.DetectionLock, 0, len(missing))
		for _, l := range missing {
			if _, ok := existing[l.NoteID]; ok {
				locks = append(locks, &entities.DetectionLock{DetectionID: l.NoteID, LockedAt: l.LockedAt})
			}
		}
		if len(locks) > 0 {
			if saveErr := m.detectionRepo.SaveLocksBatch(ctx, locks); saveErr != nil {
				return 0, 0, fmt.Errorf("failed to save locks batch: %w", saveErr)
			}
			inserted = len(locks)
		}
	}

	for _, id := range slices.Sorted(maps.Keys(v2Set)) {
		if _, ok := legacySet[id]; ok {
			continue
		}
		if unlockErr := m.detectionRepo.Unlock(ctx, id); unlockErr != nil {
			return inserted, removed, fmt.Errorf("failed to remove lock: %w", unlockErr)
		}
		removed++
	}
	return inserted, removed, nil
}
