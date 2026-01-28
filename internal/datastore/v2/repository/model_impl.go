package repository

import (
	"context"
	"errors"

	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"gorm.io/gorm"
)

// modelRepository implements ModelRepository.
type modelRepository struct {
	db          *gorm.DB
	useV2Prefix bool
	isMySQL     bool
}

// NewModelRepository creates a new ModelRepository.
// Parameters:
//   - db: GORM database connection
//   - useV2Prefix: true to use v2_ table prefix (MySQL migration mode)
//   - isMySQL: true for MySQL dialect (affects date/time SQL expressions)
func NewModelRepository(db *gorm.DB, useV2Prefix, isMySQL bool) ModelRepository {
	return &modelRepository{
		db:          db,
		useV2Prefix: useV2Prefix,
		isMySQL:     isMySQL,
	}
}

// tableName returns the appropriate table name for ai_models.
func (r *modelRepository) tableName() string {
	if r.useV2Prefix {
		return tableV2AIModels
	}
	return tableAIModels
}

// modelLabelsTable returns the appropriate table name for model_labels.
func (r *modelRepository) modelLabelsTable() string {
	if r.useV2Prefix {
		return tableV2ModelLabels
	}
	return tableModelLabels
}

// labelsTable returns the appropriate table name for labels.
func (r *modelRepository) labelsTable() string {
	if r.useV2Prefix {
		return tableV2Labels
	}
	return tableLabels
}

// GetOrCreate retrieves an existing model or creates a new one.
func (r *modelRepository) GetOrCreate(ctx context.Context, name, version string, modelType entities.ModelType) (*entities.AIModel, error) {
	var model entities.AIModel

	err := r.db.WithContext(ctx).Table(r.tableName()).
		Where("name = ? AND version = ?", name, version).
		First(&model).Error
	if err == nil {
		return &model, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}

	// Create new model
	model = entities.AIModel{
		Name:      name,
		Version:   version,
		ModelType: modelType,
	}

	createErr := r.db.WithContext(ctx).Table(r.tableName()).Create(&model).Error
	if createErr != nil {
		// Handle race condition - another goroutine may have created it.
		// Try to fetch the existing record; if that also fails, return the original create error.
		findErr := r.db.WithContext(ctx).Table(r.tableName()).
			Where("name = ? AND version = ?", name, version).
			First(&model).Error
		if findErr != nil {
			return nil, createErr
		}
	}

	return &model, nil
}

// GetByID retrieves a model by its ID.
func (r *modelRepository) GetByID(ctx context.Context, id uint) (*entities.AIModel, error) {
	var model entities.AIModel
	err := r.db.WithContext(ctx).Table(r.tableName()).First(&model, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrModelNotFound
	}
	if err != nil {
		return nil, err
	}
	return &model, nil
}

// GetByNameVersion retrieves a model by name and version.
func (r *modelRepository) GetByNameVersion(ctx context.Context, name, version string) (*entities.AIModel, error) {
	var model entities.AIModel
	err := r.db.WithContext(ctx).Table(r.tableName()).
		Where("name = ? AND version = ?", name, version).
		First(&model).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrModelNotFound
	}
	if err != nil {
		return nil, err
	}
	return &model, nil
}

// GetAll retrieves all registered models.
func (r *modelRepository) GetAll(ctx context.Context) ([]*entities.AIModel, error) {
	var models []*entities.AIModel
	err := r.db.WithContext(ctx).Table(r.tableName()).
		Order("name ASC, version ASC").
		Find(&models).Error
	return models, err
}

// RegisterModelLabel creates a mapping from a model's raw label output to a normalized label.
func (r *modelRepository) RegisterModelLabel(ctx context.Context, modelID, labelID uint, rawLabel string) error {
	modelLabel := entities.ModelLabel{
		ModelID:  modelID,
		LabelID:  labelID,
		RawLabel: rawLabel,
	}

	err := r.db.WithContext(ctx).Table(r.modelLabelsTable()).Create(&modelLabel).Error
	if err != nil {
		// Check if it's a duplicate key error (already exists)
		// Try to find it to confirm it exists
		var existing entities.ModelLabel
		findErr := r.db.WithContext(ctx).Table(r.modelLabelsTable()).
			Where("model_id = ? AND raw_label = ?", modelID, rawLabel).
			First(&existing).Error
		if findErr == nil {
			// Already exists, not an error
			return nil
		}
		return err
	}

	return nil
}

// GetModelLabels retrieves all label mappings for a model.
func (r *modelRepository) GetModelLabels(ctx context.Context, modelID uint) ([]*entities.ModelLabel, error) {
	var labels []*entities.ModelLabel
	err := r.db.WithContext(ctx).Table(r.modelLabelsTable()).
		Where("model_id = ?", modelID).
		Find(&labels).Error
	return labels, err
}

// ResolveLabelFromRaw resolves a model's raw label output to a normalized label.
func (r *modelRepository) ResolveLabelFromRaw(ctx context.Context, modelID uint, rawLabel string) (*entities.Label, error) {
	var modelLabel entities.ModelLabel
	err := r.db.WithContext(ctx).Table(r.modelLabelsTable()).
		Where("model_id = ? AND raw_label = ?", modelID, rawLabel).
		First(&modelLabel).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrLabelNotFound
	}
	if err != nil {
		return nil, err
	}

	// Get the label
	var label entities.Label
	err = r.db.WithContext(ctx).Table(r.labelsTable()).First(&label, modelLabel.LabelID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrLabelNotFound
	}
	if err != nil {
		return nil, err
	}

	return &label, nil
}

// Count returns the total number of registered models.
func (r *modelRepository) Count(ctx context.Context) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).Table(r.tableName()).Count(&count).Error
	return count, err
}

// Delete removes a model by ID.
func (r *modelRepository) Delete(ctx context.Context, id uint) error {
	result := r.db.WithContext(ctx).Table(r.tableName()).Delete(&entities.AIModel{}, id)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrModelNotFound
	}
	return nil
}

// UpdateLabelCount updates the cached label count for a model.
func (r *modelRepository) UpdateLabelCount(ctx context.Context, modelID uint) error {
	var count int64
	err := r.db.WithContext(ctx).Table(r.modelLabelsTable()).
		Where("model_id = ?", modelID).
		Count(&count).Error
	if err != nil {
		return err
	}

	return r.db.WithContext(ctx).Table(r.tableName()).
		Where("id = ?", modelID).
		Update("label_count", count).Error
}

// Exists checks if a model with the given ID exists.
func (r *modelRepository) Exists(ctx context.Context, id uint) (bool, error) {
	var count int64
	err := r.db.WithContext(ctx).Table(r.tableName()).
		Where("id = ?", id).
		Count(&count).Error
	return count > 0, err
}
