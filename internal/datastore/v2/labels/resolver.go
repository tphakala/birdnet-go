package labels

import (
	"errors"
	"fmt"
	"sync"

	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"gorm.io/gorm"
)

// Resolver handles lazy label creation and caching.
type Resolver struct {
	db    *gorm.DB
	cache sync.Map // map[cacheKey]*entities.Label
}

type cacheKey struct {
	modelID  uint
	rawLabel string
}

// NewResolver creates a new label resolver.
func NewResolver(db *gorm.DB) *Resolver {
	return &Resolver{db: db}
}

// Resolve returns the label ID for a model's raw label, creating entries as needed.
// This is the main entry point for lazy label resolution.
func (r *Resolver) Resolve(model *entities.AIModel, rawLabel string) (*entities.Label, error) {
	if model == nil {
		return nil, errors.New("model cannot be nil")
	}

	key := cacheKey{modelID: model.ID, rawLabel: rawLabel}

	// Check cache first
	if cached, ok := r.cache.Load(key); ok {
		return cached.(*entities.Label), nil
	}

	// Try to find existing model_label mapping
	var modelLabel entities.ModelLabel
	err := r.db.Preload("Label").
		Where("model_id = ? AND raw_label = ?", model.ID, rawLabel).
		First(&modelLabel).Error

	if err == nil {
		r.cache.Store(key, modelLabel.Label)
		return modelLabel.Label, nil
	}

	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("failed to lookup model label: %w", err)
	}

	// Parse the raw label
	parsed := ParseRawLabel(rawLabel, model.ModelType)

	// Find or create the label
	label, err := r.findOrCreateLabel(parsed)
	if err != nil {
		return nil, err
	}

	// Create model_label mapping
	modelLabel = entities.ModelLabel{
		ModelID:  model.ID,
		LabelID:  label.ID,
		RawLabel: rawLabel,
	}
	if err := r.db.Create(&modelLabel).Error; err != nil {
		// Handle race condition - another goroutine may have created it
		createErr := err
		if err := r.db.Where("model_id = ? AND raw_label = ?", model.ID, rawLabel).
			First(&modelLabel).Error; err != nil {
			// Re-fetch failed; return the original create error for better context
			return nil, fmt.Errorf("failed to create model label mapping: %w", createErr)
		}
	} else {
		// Update model's label count (best-effort, non-critical).
		// This is a denormalized counter for performance; the true count can be
		// derived from the model_labels table. Errors are intentionally ignored.
		// Only increment when we successfully created a new mapping (not on race condition).
		_ = r.db.Model(model).Update("label_count", gorm.Expr("label_count + 1")).Error
	}

	r.cache.Store(key, label)
	return label, nil
}

// findOrCreateLabel finds an existing label by scientific name or creates a new one.
func (r *Resolver) findOrCreateLabel(parsed ParsedLabel) (*entities.Label, error) {
	var label entities.Label

	// For species, lookup by scientific name
	if parsed.LabelType == entities.LabelTypeSpecies && parsed.ScientificName != "" {
		err := r.db.Where("scientific_name = ?", parsed.ScientificName).First(&label).Error
		if err == nil {
			return &label, nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("failed to lookup label: %w", err)
		}
	}

	// For non-species, lookup by scientific_name field (stores the label identifier)
	if parsed.LabelType != entities.LabelTypeSpecies {
		err := r.db.Where("scientific_name = ? AND label_type = ?",
			parsed.ScientificName, parsed.LabelType).First(&label).Error
		if err == nil {
			return &label, nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("failed to lookup non-species label: %w", err)
		}
	}

	// Create new label
	scientificName := parsed.ScientificName
	var taxonomicClass *string
	if parsed.TaxonomicClass != "" {
		taxonomicClass = &parsed.TaxonomicClass
	}

	label = entities.Label{
		ScientificName: &scientificName,
		LabelType:      parsed.LabelType,
		TaxonomicClass: taxonomicClass,
	}

	if err := r.db.Create(&label).Error; err != nil {
		// Handle race condition - another goroutine may have created the label
		createErr := err
		if parsed.LabelType == entities.LabelTypeSpecies {
			if err := r.db.Where("scientific_name = ?", parsed.ScientificName).
				First(&label).Error; err != nil {
				// Re-fetch failed; return the original create error for better context
				return nil, fmt.Errorf("failed to create label: %w", createErr)
			}
		} else {
			// For non-species, also handle race condition by fetching existing
			if err := r.db.Where("scientific_name = ? AND label_type = ?",
				parsed.ScientificName, parsed.LabelType).First(&label).Error; err != nil {
				// Re-fetch failed; return the original create error for better context
				return nil, fmt.Errorf("failed to create non-species label: %w", createErr)
			}
		}
	}

	return &label, nil
}

// GetModel retrieves or creates an AI model by name and version.
func (r *Resolver) GetModel(name, version string, modelType entities.ModelType) (*entities.AIModel, error) {
	var model entities.AIModel

	err := r.db.Where("name = ? AND version = ?", name, version).First(&model).Error
	if err == nil {
		return &model, nil
	}

	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("failed to lookup model: %w", err)
	}

	// Create new model
	model = entities.AIModel{
		Name:      name,
		Version:   version,
		ModelType: modelType,
	}

	if err := r.db.Create(&model).Error; err != nil {
		// Handle race condition
		if err := r.db.Where("name = ? AND version = ?", name, version).
			First(&model).Error; err != nil {
			return nil, fmt.Errorf("failed to create model: %w", err)
		}
	}

	return &model, nil
}

// ClearCache clears the resolver cache.
func (r *Resolver) ClearCache() {
	r.cache = sync.Map{}
}
