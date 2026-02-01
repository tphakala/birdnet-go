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

// labelsTable returns the appropriate table name for labels.
func (r *modelRepository) labelsTable() string {
	if r.useV2Prefix {
		return tableV2Labels
	}
	return tableLabels
}

// GetOrCreate retrieves an existing model or creates a new one.
// Matches on name + version + variant combination.
func (r *modelRepository) GetOrCreate(ctx context.Context, name, version, variant string, modelType entities.ModelType, classifierPath *string) (*entities.AIModel, error) {
	var model entities.AIModel

	err := r.db.WithContext(ctx).Table(r.tableName()).
		Where("name = ? AND version = ? AND variant = ?", name, version, variant).
		First(&model).Error
	if err == nil {
		return &model, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}

	// Create new model
	model = entities.AIModel{
		Name:           name,
		Version:        version,
		Variant:        variant,
		ModelType:      modelType,
		ClassifierPath: classifierPath,
	}

	createErr := r.db.WithContext(ctx).Table(r.tableName()).Create(&model).Error
	if createErr != nil {
		// Handle race condition
		findErr := r.db.WithContext(ctx).Table(r.tableName()).
			Where("name = ? AND version = ? AND variant = ?", name, version, variant).
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

// GetByNameVersionVariant retrieves a model by name, version, and variant.
func (r *modelRepository) GetByNameVersionVariant(ctx context.Context, name, version, variant string) (*entities.AIModel, error) {
	var model entities.AIModel
	err := r.db.WithContext(ctx).Table(r.tableName()).
		Where("name = ? AND version = ? AND variant = ?", name, version, variant).
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
		Order("name ASC, version ASC, variant ASC").
		Find(&models).Error
	return models, err
}

// Count returns the total number of registered models.
func (r *modelRepository) Count(ctx context.Context) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).Table(r.tableName()).Count(&count).Error
	return count, err
}

// CountLabels returns the count of labels for a specific model.
// This is derived from the labels table where model_id matches.
func (r *modelRepository) CountLabels(ctx context.Context, modelID uint) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).Table(r.labelsTable()).
		Where("model_id = ?", modelID).
		Count(&count).Error
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

// Exists checks if a model with the given ID exists.
func (r *modelRepository) Exists(ctx context.Context, id uint) (bool, error) {
	var count int64
	err := r.db.WithContext(ctx).Table(r.tableName()).
		Where("id = ?", id).
		Count(&count).Error
	return count > 0, err
}
