package repository

import (
	"context"

	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
)

// ModelRepository provides access to the ai_models table.
type ModelRepository interface {
	// GetOrCreate retrieves an existing model or creates a new one.
	// Matches on name + version + variant combination.
	GetOrCreate(ctx context.Context, name, version, variant string, modelType entities.ModelType, classifierPath *string) (*entities.AIModel, error)

	// GetByID retrieves a model by its ID.
	// Returns ErrModelNotFound if not found.
	GetByID(ctx context.Context, id uint) (*entities.AIModel, error)

	// GetByNameVersionVariant retrieves a model by name, version, and variant.
	// Returns ErrModelNotFound if not found.
	GetByNameVersionVariant(ctx context.Context, name, version, variant string) (*entities.AIModel, error)

	// GetAll retrieves all registered models.
	GetAll(ctx context.Context) ([]*entities.AIModel, error)

	// Count returns the total number of registered models.
	Count(ctx context.Context) (int64, error)

	// CountLabels returns the count of labels for a specific model.
	// This is derived from the labels table where model_id matches.
	CountLabels(ctx context.Context, modelID uint) (int64, error)

	// Delete removes a model by ID.
	// Returns ErrModelNotFound if not found.
	// Note: This may fail if there are detections or labels referencing this model.
	Delete(ctx context.Context, id uint) error

	// Exists checks if a model with the given ID exists.
	Exists(ctx context.Context, id uint) (bool, error)
}
