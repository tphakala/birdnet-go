package repository

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/speciesindex"
	"gorm.io/gorm"
)

// Detections columns a species change writes.
const (
	columnDetectionID      = "id"
	columnLabelID          = "label_id"
	columnOriginalLabelID  = "original_label_id"
	changeSpeciesOperation = "v2_change_detection_species"
	setOriginalIfNullExpr  = "COALESCE(" + columnOriginalLabelID + ", ?)"
)

// ChangeSpecies sets the species of a detection as a reviewer's correction.
//
// The reads of the detection, its lock and the label run before the
// transaction. The transaction holds two statements: the UPDATE of label_id and
// original_label_id, guarded by the lock check, and the review upsert. The UPDATE
// is the first statement of the transaction so that, on MySQL and MariaDB, no
// snapshot taken by an earlier read can hide a lock or a delete committed
// meanwhile. A detection deleted or locked after those reads is told apart after
// the transaction, as Update does, so the retry helper, which reads the word
// "locked" in an error as a busy database, never sees ErrDetectionLocked.
func (r *detectionRepository) ChangeSpecies(ctx context.Context, id uint, scientificName string) error {
	scientificName = strings.TrimSpace(scientificName)
	if scientificName == "" {
		return ErrLabelNotFound
	}

	det, err := r.Get(ctx, id)
	if err != nil {
		return err
	}
	locked, err := r.IsLocked(ctx, id)
	if err != nil {
		return fmt.Errorf("check detection lock: %w", err)
	}
	if locked {
		return ErrDetectionLocked
	}
	label, err := r.speciesLabel(ctx, scientificName, det.ModelID)
	if err != nil {
		return err
	}
	if label.ID == det.LabelID {
		return nil
	}

	var changed bool
	err = datastore.RetryTransactionOnLock(ctx, r.db, changeSpeciesOperation, func(tx *gorm.DB) error {
		changed = false
		// The old label is a bound parameter, never label_id itself: MySQL evaluates
		// SET left to right and GORM sorts the map keys, so label_id would already
		// hold the new value. The bound value is only used while original_label_id
		// is NULL, and then nobody has changed the label since the detection was
		// stored.
		result := tx.Table(r.tableName()).
			Where(columnDetectionID+" = ?", id).
			Where(r.notLockedCondition(), id).
			Updates(map[string]any{
				columnLabelID:         label.ID,
				columnOriginalLabelID: gorm.Expr(setOriginalIfNullExpr, det.LabelID),
			})
		if result.Error != nil {
			return fmt.Errorf("set detection label: %w", result.Error)
		}
		if result.RowsAffected == 0 {
			return nil
		}
		review := &entities.DetectionReview{DetectionID: id, Verified: entities.VerificationCorrect}
		if err := r.upsertReview(tx, review); err != nil {
			return fmt.Errorf("mark detection reviewed: %w", err)
		}
		changed = true
		return nil
	}, r.metrics)
	if err != nil {
		return err
	}
	if changed {
		return nil
	}

	// Nothing was written: the detection was deleted or locked after the reads.
	exists, err := r.Exists(ctx, id)
	if err != nil {
		return err
	}
	if !exists {
		return ErrDetectionNotFound
	}
	return ErrDetectionLocked
}

// speciesLabel returns the label of modelID for the species named scientificName.
// It takes, in this order: the model's label of that exact name; a label of the
// model whose name is a taxonomic alias of it (the predictions response gives
// canonical names, which a model's label may store under an older name); a new
// label for the model, made when only other models have a label of that name, with
// the label type and taxonomic class of the oldest of those. A species no model has
// a label for is ErrLabelNotFound.
func (r *detectionRepository) speciesLabel(ctx context.Context, scientificName string, modelID uint) (*entities.Label, error) {
	labels := NewLabelRepository(r.db, r.metrics, r.useV2Prefix, r.isMySQL)

	label, err := labels.GetByScientificNameAndModel(ctx, scientificName, modelID)
	if err == nil {
		return label, nil
	}
	if !errors.Is(err, ErrLabelNotFound) {
		return nil, fmt.Errorf("load species label: %w", err)
	}

	modelLabels, err := labels.GetAllByModel(ctx, modelID)
	if err != nil {
		return nil, fmt.Errorf("load model labels: %w", err)
	}
	species := speciesindex.CanonicalKey(scientificName)
	for _, candidate := range modelLabels {
		if speciesindex.CanonicalKey(candidate.ScientificName) == species {
			return candidate, nil
		}
	}

	known, err := labels.GetByScientificName(ctx, scientificName)
	if err != nil {
		return nil, fmt.Errorf("load labels of other models: %w", err)
	}
	if len(known) == 0 {
		return nil, ErrLabelNotFound
	}
	oldest := slices.MinFunc(known, func(a, b *entities.Label) int { return cmp.Compare(a.ID, b.ID) })
	label, err = labels.GetOrCreate(ctx, oldest.ScientificName, modelID, oldest.LabelTypeID, oldest.TaxonomicClassID)
	if err != nil {
		return nil, fmt.Errorf("create species label: %w", err)
	}
	return label, nil
}
