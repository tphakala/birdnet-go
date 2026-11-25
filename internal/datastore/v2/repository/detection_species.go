package repository

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/detection"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/labels/soundevent"
	"github.com/tphakala/birdnet-go/internal/openfauna"
	"github.com/tphakala/birdnet-go/internal/speciesindex"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Detections columns a species change writes.
const (
	columnDetectionID      = "id"
	columnLabelID          = "label_id"
	columnOriginalLabelID  = "original_label_id"
	changeSpeciesOperation = "v2_change_detection_species"
	setOriginalIfNullExpr  = "COALESCE(" + columnOriginalLabelID + ", " + columnLabelID + ")"
)

// SpeciesOffer describes a species that no label of the detection's model matches,
// from the caller's live sources (the species lists and the loaded models). A nil
// *SpeciesOffer means the species is not offered. Empty Models means a species list
// offers it but no loaded model can classify it.
type SpeciesOffer struct {
	// Models lists the loaded models that can classify the species, in a stable
	// order chosen by the caller.
	Models []OfferedModel
}

// OfferedModel is one loaded model that can classify an offered species.
type OfferedModel struct {
	// Model is the model's ai_models identity; matching uses Name and Version
	// and prefers an exact Variant match when present.
	Model detection.ModelInfo
	// ScientificName is the model's own scientific name of the species.
	ScientificName string
}

// ChangeSpecies sets the species of a detection as a reviewer's correction.
//
// The reads of the detection, its lock and the label run before the
// transaction. The transaction holds two statements: the UPDATE of label_id and
// original_label_id, guarded by the lock check, and the review upsert. The
// original is the label the row holds when the UPDATE runs, not the one read
// before it. The UPDATE is the first statement of the transaction so that, on
// MySQL and MariaDB, no snapshot taken by an earlier read can hide a lock or a
// delete committed meanwhile. A detection deleted or locked after those reads is
// told apart after the transaction, as Update does. A request for the species
// the current label already stands for, compared by canonical key, returns
// before any label is resolved or made. A species the detection's model has no
// label of is made from offer (see speciesLabel); without an offer it is refused.
func (r *detectionRepository) ChangeSpecies(ctx context.Context, id uint, scientificName string, offer *SpeciesOffer) error {
	scientificName = strings.TrimSpace(scientificName)
	// A name without a species part (empty, "_" or "_Blackbird") has an empty key,
	// which would match a label whose key is empty too.
	species := speciesindex.CanonicalKey(scientificName)
	if scientificName == "" || species == "" {
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
	// The species the detection already stands for, under whichever spelling or
	// label ID, is no change. This runs before label resolution so that no label
	// is created for it. A current label that is gone cannot be compared.
	current, err := NewLabelRepository(r.db, r.metrics, r.useV2Prefix, r.isMySQL).GetByID(ctx, det.LabelID)
	switch {
	case err == nil:
		if speciesindex.CanonicalKey(current.ScientificName) == species {
			return nil
		}
	case !errors.Is(err, ErrLabelNotFound):
		return fmt.Errorf("load current label: %w", err)
	}
	label, err := r.speciesLabel(ctx, scientificName, det, offer)
	if err != nil {
		return err
	}
	if label.ID == det.LabelID {
		return nil
	}

	var changed bool
	err = datastore.RetryTransactionOnLock(ctx, r.db, changeSpeciesOperation, func(tx *gorm.DB) error {
		changed = false
		// The original is recorded from the row's own label_id, so it is the label
		// the row holds when the UPDATE runs. The assignments are an ordered list,
		// not a map, on purpose: MySQL and MariaDB evaluate SET left to right and
		// see the values already assigned, so original_label_id must come before
		// label_id or it would record the new label. (SQLite and PostgreSQL read the
		// old row for every right-hand side.) GORM sorts the keys of an update map
		// alphabetically, which would put label_id first.
		result := tx.Table(r.tableName()).
			Where(columnDetectionID+" = ?", id).
			Where(r.notLockedCondition(), id).
			Clauses(clause.Set{
				{Column: clause.Column{Name: columnOriginalLabelID}, Value: gorm.Expr(setOriginalIfNullExpr)},
				{Column: clause.Column{Name: columnLabelID}, Value: label.ID},
			}).
			Updates(map[string]any{})
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

// speciesLabel returns the label of the detection's model for the species named
// scientificName. It takes, in this order: the model's label of that exact name; a
// label of the model whose name is a taxonomic alias of it (the predictions
// response gives canonical names, which a model's label may store under an older
// name), the oldest by label ID when the model holds several spellings of the
// species; a new label for the model made from offer (see createOfferedLabel). A
// species the model has no label of and that is not offered is ErrLabelNotFound.
func (r *detectionRepository) speciesLabel(ctx context.Context, scientificName string, det *entities.Detection, offer *SpeciesOffer) (*entities.Label, error) {
	labels := NewLabelRepository(r.db, r.metrics, r.useV2Prefix, r.isMySQL)

	label, err := labels.GetByScientificNameAndModel(ctx, scientificName, det.ModelID)
	if err == nil {
		return label, nil
	}
	if !errors.Is(err, ErrLabelNotFound) {
		return nil, fmt.Errorf("load species label: %w", err)
	}

	modelLabels, err := labels.GetAllByModel(ctx, det.ModelID)
	if err != nil {
		return nil, fmt.Errorf("load model labels: %w", err)
	}
	species := speciesindex.CanonicalKey(scientificName)
	aliases := slices.DeleteFunc(modelLabels, func(candidate *entities.Label) bool {
		return speciesindex.CanonicalKey(candidate.ScientificName) != species
	})
	if len(aliases) > 0 {
		return oldestLabel(aliases), nil
	}

	if offer == nil {
		return nil, ErrLabelNotFound
	}
	return r.createOfferedLabel(ctx, scientificName, det.ModelID, offer)
}

// offeredSpelling is an offered model with its identity defaulted and its
// scientific name reduced to the species part.
type offeredSpelling struct {
	info detection.ModelInfo
	name string
	// modelType is the ai_models type of the row; set only on the detection's own
	// model.
	modelType entities.ModelType
}

// createOfferedLabel gets or creates the label of modelID for an offered species.
//
// The name is what the save path would store: the scientific name the detection's
// own model spells the species with, run through openfauna.CanonicalName, so a
// later detection of the species by that model finds the row. When the model does
// not know the species (it never stores it), the first offered model's spelling is
// used, and without any model the requested name. The label type comes from the
// sound-event registry by the stored name, so it never depends on which models are
// loaded. A taxonomic class is set only for the species type: none for a sound
// class name; the detection model's own class when it knows the species; otherwise
// the class every offered model that implies one agrees on.
func (r *detectionRepository) createOfferedLabel(ctx context.Context, requested string, modelID uint, offer *SpeciesOffer) (*entities.Label, error) {
	offered := make([]offeredSpelling, 0, len(offer.Models))
	for _, m := range offer.Models {
		if sci := strings.TrimSpace(detection.ExtractScientificName(m.ScientificName)); sci != "" {
			offered = append(offered, offeredSpelling{info: m.Model.WithDefaults(), name: sci})
		}
	}

	var own *offeredSpelling
	if len(offered) > 0 {
		row, err := NewModelRepository(r.db, r.metrics, r.useV2Prefix, r.isMySQL).GetByID(ctx, modelID)
		switch {
		case err == nil:
			// Match on Name and Version because a custom classifier in a registry slot
			// stores Variant "custom" while DetectionModelInfoForID reports "default".
			// Prefer an exact Variant match when one is present.
			for i := range offered {
				if offered[i].info.Name != row.Name || offered[i].info.Version != row.Version {
					continue
				}
				if own == nil || offered[i].info.Variant == row.Variant {
					own = &offered[i]
					if offered[i].info.Variant == row.Variant {
						break
					}
				}
			}
			if own != nil {
				own.modelType = row.ModelType
			}
		case !errors.Is(err, ErrModelNotFound):
			return nil, fmt.Errorf("load detection model: %w", err)
		}
	}

	name := strings.TrimSpace(detection.ExtractScientificName(requested))
	switch {
	case own != nil:
		name = own.name
	case len(offered) > 0:
		name = offered[0].name
	}
	name = openfauna.CanonicalName(name)

	typeName := entities.LabelTypeSpecies
	var className string
	if category, ok := soundevent.CategoryOf(name); ok {
		typeName = string(category)
	} else if !soundevent.IsName(name) {
		className = offeredTaxonomicClass(own, offered)
	}

	labelType, err := NewLabelTypeRepository(r.db, r.metrics, r.useV2Prefix).GetOrCreate(ctx, typeName)
	if err != nil {
		return nil, fmt.Errorf("get label type %q: %w", typeName, err)
	}
	var classID *uint
	if className != "" {
		class, err := NewTaxonomicClassRepository(r.db, r.metrics, r.useV2Prefix).GetOrCreate(ctx, className)
		if err != nil {
			return nil, fmt.Errorf("get taxonomic class %q: %w", className, err)
		}
		classID = &class.ID
	}
	label, err := NewLabelRepository(r.db, r.metrics, r.useV2Prefix, r.isMySQL).GetOrCreate(ctx, name, modelID, labelType.ID, classID)
	if err != nil {
		return nil, fmt.Errorf("create species label: %w", err)
	}
	return label, nil
}

// offeredTaxonomicClass returns the taxonomic class name for a new species label,
// "" for none. When the detection's own model knows the species its model type
// decides, as in its save path; otherwise the class the offered models imply, when
// they all agree.
func offeredTaxonomicClass(own *offeredSpelling, offered []offeredSpelling) string {
	if own != nil {
		return own.modelType.TaxonomicClass()
	}
	var agreed string
	for _, m := range offered {
		class := detection.ResolveModelType(m.info.Name, m.info.Version).TaxonomicClass()
		switch {
		case class == "":
		case agreed == "":
			agreed = class
		case agreed != class:
			return ""
		}
	}
	return agreed
}

// oldestLabel returns the label with the lowest ID, which is the oldest. labels
// must not be empty.
func oldestLabel(labels []*entities.Label) *entities.Label {
	return slices.MinFunc(labels, func(a, b *entities.Label) int { return cmp.Compare(a.ID, b.ID) })
}
