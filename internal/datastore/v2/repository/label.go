package repository

import (
	"context"

	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
)

// LabelRepository provides access to the labels table.
// Labels are model-specific - the same species can have separate entries for different models.
type LabelRepository interface {
	// GetOrCreate retrieves an existing label or creates a new one.
	// Labels are unique per (scientific_name, model_id).
	GetOrCreate(ctx context.Context, scientificName string, modelID, labelTypeID uint, taxonomicClassID *uint) (*entities.Label, error)

	// GetByID retrieves a label by its ID.
	// Returns ErrLabelNotFound if not found.
	GetByID(ctx context.Context, id uint) (*entities.Label, error)

	// GetByIDs retrieves multiple labels by their IDs in a single query.
	// Returns a map of ID to Label for efficient lookup.
	// Handles large ID sets by chunking to avoid SQL parameter limits.
	GetByIDs(ctx context.Context, ids []uint) (map[uint]*entities.Label, error)

	// GetByScientificNameAndModel retrieves a label by scientific name and model ID.
	// Returns ErrLabelNotFound if not found.
	GetByScientificNameAndModel(ctx context.Context, name string, modelID uint) (*entities.Label, error)

	// GetByScientificNamesAndModel retrieves multiple labels by scientific names for a specific model.
	// Returns a slice of labels for efficient batch processing.
	// Handles large name sets by chunking to avoid SQL parameter limits.
	GetByScientificNamesAndModel(ctx context.Context, names []string, modelID uint) ([]*entities.Label, error)

	// BatchGetOrCreate retrieves or creates multiple labels in optimized batches.
	// Returns a map of scientificName -> Label for all requested names.
	// Uses bulk operations to minimize database round-trips.
	BatchGetOrCreate(ctx context.Context, scientificNames []string, modelID, labelTypeID uint, taxonomicClassID *uint) (map[string]*entities.Label, error)

	// GetAllByModel retrieves all labels for a specific model.
	GetAllByModel(ctx context.Context, modelID uint) ([]*entities.Label, error)

	// GetAllByLabelType retrieves all labels of a specific label type.
	GetAllByLabelType(ctx context.Context, labelTypeID uint) ([]*entities.Label, error)

	// Search finds labels matching the query string.
	// Searches in scientific_name field.
	Search(ctx context.Context, query string, limit int) ([]*entities.Label, error)

	// Count returns the total number of labels.
	Count(ctx context.Context) (int64, error)

	// CountByModel returns the count of labels for a specific model.
	CountByModel(ctx context.Context, modelID uint) (int64, error)

	// GetAll retrieves all labels.
	GetAll(ctx context.Context) ([]*entities.Label, error)

	// Delete removes a label by ID.
	// Returns ErrLabelNotFound if not found.
	// Note: This may fail if there are detections referencing this label.
	Delete(ctx context.Context, id uint) error

	// Exists checks if a label with the given ID exists.
	Exists(ctx context.Context, id uint) (bool, error)

	// GetByScientificName retrieves all labels matching a scientific name across all models.
	// Used for model-agnostic features like dynamic thresholds and image caching.
	// Returns empty slice if no labels found.
	GetByScientificName(ctx context.Context, name string) ([]*entities.Label, error)

	// GetLabelIDsByScientificName retrieves label IDs for a scientific name across all models.
	// Convenience method for filter queries that need IDs only.
	// Returns empty slice if no labels found.
	GetLabelIDsByScientificName(ctx context.Context, name string) ([]uint, error)
}

// LabelTypeRepository provides access to the label_types lookup table.
type LabelTypeRepository interface {
	// GetByName retrieves a label type by name.
	// Returns ErrLabelTypeNotFound if not found.
	GetByName(ctx context.Context, name string) (*entities.LabelType, error)

	// GetByID retrieves a label type by ID.
	// Returns ErrLabelTypeNotFound if not found.
	GetByID(ctx context.Context, id uint) (*entities.LabelType, error)

	// GetAll retrieves all label types.
	GetAll(ctx context.Context) ([]*entities.LabelType, error)

	// GetOrCreate retrieves an existing label type or creates a new one.
	GetOrCreate(ctx context.Context, name string) (*entities.LabelType, error)
}

// TaxonomicClassRepository provides access to the taxonomic_classes lookup table.
type TaxonomicClassRepository interface {
	// GetByName retrieves a taxonomic class by name.
	// Returns ErrTaxonomicClassNotFound if not found.
	GetByName(ctx context.Context, name string) (*entities.TaxonomicClass, error)

	// GetByID retrieves a taxonomic class by ID.
	// Returns ErrTaxonomicClassNotFound if not found.
	GetByID(ctx context.Context, id uint) (*entities.TaxonomicClass, error)

	// GetAll retrieves all taxonomic classes.
	GetAll(ctx context.Context) ([]*entities.TaxonomicClass, error)

	// GetOrCreate retrieves an existing taxonomic class or creates a new one.
	GetOrCreate(ctx context.Context, name string) (*entities.TaxonomicClass, error)
}
