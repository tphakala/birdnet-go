package repository

import (
	"context"

	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
)

// LabelRepository provides access to the labels table.
type LabelRepository interface {
	// GetOrCreate retrieves an existing label or creates a new one.
	// For species, matches on scientific_name.
	// For non-species, matches on scientific_name and label_type.
	GetOrCreate(ctx context.Context, scientificName string, labelType entities.LabelType) (*entities.Label, error)

	// GetByID retrieves a label by its ID.
	// Returns ErrLabelNotFound if not found.
	GetByID(ctx context.Context, id uint) (*entities.Label, error)

	// GetByIDs retrieves multiple labels by their IDs in a single query.
	// Returns a map of ID to Label for efficient lookup.
	// Handles large ID sets by chunking to avoid SQL parameter limits.
	GetByIDs(ctx context.Context, ids []uint) (map[uint]*entities.Label, error)

	// GetByScientificName retrieves a label by scientific name.
	// Returns ErrLabelNotFound if not found.
	GetByScientificName(ctx context.Context, name string) (*entities.Label, error)

	// GetByScientificNames retrieves multiple labels by scientific names in a single query.
	// Returns a slice of labels for efficient batch processing.
	// Handles large name sets by chunking to avoid SQL parameter limits.
	GetByScientificNames(ctx context.Context, names []string) ([]*entities.Label, error)

	// BatchGetOrCreate retrieves or creates multiple labels in optimized batches.
	// Returns a map of scientificName -> Label for all requested names.
	// Uses bulk operations to minimize database round-trips.
	BatchGetOrCreate(ctx context.Context, scientificNames []string, labelType entities.LabelType) (map[string]*entities.Label, error)

	// GetAllByType retrieves all labels of a specific type.
	GetAllByType(ctx context.Context, labelType entities.LabelType) ([]*entities.Label, error)

	// Search finds labels matching the query string.
	// Searches in scientific_name field.
	Search(ctx context.Context, query string, limit int) ([]*entities.Label, error)

	// Count returns the total number of labels.
	Count(ctx context.Context) (int64, error)

	// CountByType returns the count of labels for a specific type.
	CountByType(ctx context.Context, labelType entities.LabelType) (int64, error)

	// GetAll retrieves all labels.
	GetAll(ctx context.Context) ([]*entities.Label, error)

	// Delete removes a label by ID.
	// Returns ErrLabelNotFound if not found.
	// Note: This may fail if there are detections referencing this label.
	Delete(ctx context.Context, id uint) error

	// Exists checks if a label with the given ID exists.
	Exists(ctx context.Context, id uint) (bool, error)

	// GetRawLabelForLabel retrieves the raw_label string from model_labels
	// for a given model and label combination. This is useful for extracting
	// common names which are stored in the raw_label format "ScientificName_CommonName".
	// Returns empty string if no mapping exists.
	GetRawLabelForLabel(ctx context.Context, modelID, labelID uint) (string, error)

	// GetRawLabelsForLabels batch retrieves raw_labels from model_labels for multiple
	// model/label combinations. Returns a map keyed by "modelID:labelID" string.
	// This is more efficient than calling GetRawLabelForLabel in a loop.
	GetRawLabelsForLabels(ctx context.Context, pairs []ModelLabelPair) (map[string]string, error)
}

// ModelLabelPair represents a model_id and label_id combination for batch queries.
type ModelLabelPair struct {
	ModelID uint
	LabelID uint
}
