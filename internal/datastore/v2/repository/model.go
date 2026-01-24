package repository

import (
	"context"

	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
)

// ModelRepository provides access to the ai_models and model_labels tables.
type ModelRepository interface {
	// GetOrCreate retrieves an existing model or creates a new one.
	// Matches on name + version combination.
	GetOrCreate(ctx context.Context, name, version string, modelType entities.ModelType) (*entities.AIModel, error)

	// GetByID retrieves a model by its ID.
	// Returns ErrModelNotFound if not found.
	GetByID(ctx context.Context, id uint) (*entities.AIModel, error)

	// GetByNameVersion retrieves a model by name and version.
	// Returns ErrModelNotFound if not found.
	GetByNameVersion(ctx context.Context, name, version string) (*entities.AIModel, error)

	// GetAll retrieves all registered models.
	GetAll(ctx context.Context) ([]*entities.AIModel, error)

	// RegisterModelLabel creates a mapping from a model's raw label output to a normalized label.
	// This is used to track which models can detect which labels and their raw output format.
	RegisterModelLabel(ctx context.Context, modelID, labelID uint, rawLabel string) error

	// GetModelLabels retrieves all label mappings for a model.
	GetModelLabels(ctx context.Context, modelID uint) ([]*entities.ModelLabel, error)

	// ResolveLabelFromRaw resolves a model's raw label output to a normalized label.
	// Returns ErrLabelNotFound if the mapping doesn't exist.
	ResolveLabelFromRaw(ctx context.Context, modelID uint, rawLabel string) (*entities.Label, error)

	// Count returns the total number of registered models.
	Count(ctx context.Context) (int64, error)

	// Delete removes a model by ID.
	// Returns ErrModelNotFound if not found.
	// Note: This may fail if there are detections referencing this model.
	Delete(ctx context.Context, id uint) error

	// UpdateLabelCount updates the cached label count for a model.
	// The count represents how many distinct labels this model can detect.
	UpdateLabelCount(ctx context.Context, modelID uint) error

	// Exists checks if a model with the given ID exists.
	Exists(ctx context.Context, id uint) (bool, error)
}
