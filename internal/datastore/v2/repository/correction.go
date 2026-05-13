// Package repository - correction.go
//
// Helper for applying an operator-driven species correction to the v2
// schema. Shared between the v2only adapter (where v2 is the only store)
// and DualWriteRepository (where v2 is the dual-write target alongside the
// legacy v1 store). Centralising the model + label lookup + detection
// update + review save keeps the v2-specific semantics in one place and
// out of the wrappers.

package repository

import (
	"context"
	"fmt"

	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/detection"
)

// WriteSpeciesCorrection applies a species correction to v2_detections and
// upserts the corresponding v2_detection_reviews row. The caller resolves
// (name, version, variant) for the chosen model and the new scientific name;
// this helper looks up the matching v2_ai_models and v2_labels rows, writes
// the detection update via DetectionRepository.Update (which enforces the
// detection_locks guard atomically), and saves the review.
//
// Returns wrapped errors for any of the underlying repository failures —
// notably repository.ErrModelNotFound, repository.ErrLabelNotFound, and
// repository.ErrDetectionLocked, which callers can match via errors.Is.
//
// Designed to be called either inside a transaction (when the caller wants
// to atomically pair the v2 write with other work) or directly against the
// session-scoped repositories; the repositories handle their own SQL and
// don't require an enclosing tx.
func WriteSpeciesCorrection(
	ctx context.Context,
	modelRepo ModelRepository,
	labelRepo LabelRepository,
	detRepo DetectionRepository,
	detectionID uint,
	scientificName string,
	model detection.ModelInfo,
	confidence float64,
) error {
	if model.Name == "" || model.Version == "" {
		return fmt.Errorf("model name/version required for v2 correction")
	}

	aiModel, err := modelRepo.GetByNameVersionVariant(ctx, model.Name, model.Version, model.Variant)
	if err != nil {
		return fmt.Errorf("v2 ai_models lookup failed: %w", err)
	}

	label, err := labelRepo.GetByScientificNameAndModel(ctx, scientificName, aiModel.ID)
	if err != nil {
		return fmt.Errorf("v2 labels lookup failed: %w", err)
	}

	if err := detRepo.Update(ctx, detectionID, map[string]any{
		"label_id":   label.ID,
		"model_id":   aiModel.ID,
		"confidence": confidence,
	}); err != nil {
		return fmt.Errorf("v2 detection update failed: %w", err)
	}

	if err := detRepo.SaveReview(ctx, &entities.DetectionReview{
		DetectionID: detectionID,
		Verified:    entities.VerificationCorrect,
	}); err != nil {
		return fmt.Errorf("v2 review save failed: %w", err)
	}
	return nil
}
