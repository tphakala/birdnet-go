package repository

import (
	"context"
	"errors"

	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"gorm.io/gorm"
)

// labelRepository implements LabelRepository.
type labelRepository struct {
	db          *gorm.DB
	useV2Prefix bool // For MySQL: use v2_ table prefix
}

// NewLabelRepository creates a new LabelRepository.
// Set useV2Prefix to true for MySQL to use v2_ table prefix.
func NewLabelRepository(db *gorm.DB, useV2Prefix bool) LabelRepository {
	return &labelRepository{
		db:          db,
		useV2Prefix: useV2Prefix,
	}
}

// tableName returns the appropriate table name based on database type.
func (r *labelRepository) tableName() string {
	if r.useV2Prefix {
		return tableV2Labels
	}
	return tableLabels
}

// GetOrCreate retrieves an existing label or creates a new one.
func (r *labelRepository) GetOrCreate(ctx context.Context, scientificName string, labelType entities.LabelType) (*entities.Label, error) {
	var label entities.Label

	// For species, lookup by scientific name only
	if labelType == entities.LabelTypeSpecies {
		err := r.db.WithContext(ctx).Table(r.tableName()).
			Where("scientific_name = ?", scientificName).
			First(&label).Error
		if err == nil {
			return &label, nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
	} else {
		// For non-species, lookup by scientific_name and label_type
		err := r.db.WithContext(ctx).Table(r.tableName()).
			Where("scientific_name = ? AND label_type = ?", scientificName, labelType).
			First(&label).Error
		if err == nil {
			return &label, nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
	}

	// Create new label
	label = entities.Label{
		ScientificName: &scientificName,
		LabelType:      labelType,
	}

	createErr := r.db.WithContext(ctx).Table(r.tableName()).Create(&label).Error
	if createErr != nil {
		// Handle race condition - another goroutine may have created it.
		// Try to fetch the existing record; if that also fails, return the original create error.
		var findErr error
		if labelType == entities.LabelTypeSpecies {
			findErr = r.db.WithContext(ctx).Table(r.tableName()).
				Where("scientific_name = ?", scientificName).
				First(&label).Error
		} else {
			findErr = r.db.WithContext(ctx).Table(r.tableName()).
				Where("scientific_name = ? AND label_type = ?", scientificName, labelType).
				First(&label).Error
		}
		if findErr != nil {
			// Record doesn't exist, so creation failed for another reason
			return nil, createErr
		}
	}

	return &label, nil
}

// GetByID retrieves a label by its ID.
func (r *labelRepository) GetByID(ctx context.Context, id uint) (*entities.Label, error) {
	var label entities.Label
	err := r.db.WithContext(ctx).Table(r.tableName()).First(&label, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrLabelNotFound
	}
	if err != nil {
		return nil, err
	}
	return &label, nil
}

// GetByScientificName retrieves a label by scientific name.
func (r *labelRepository) GetByScientificName(ctx context.Context, name string) (*entities.Label, error) {
	var label entities.Label
	err := r.db.WithContext(ctx).Table(r.tableName()).
		Where("scientific_name = ?", name).
		First(&label).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrLabelNotFound
	}
	if err != nil {
		return nil, err
	}
	return &label, nil
}

// GetAllByType retrieves all labels of a specific type.
func (r *labelRepository) GetAllByType(ctx context.Context, labelType entities.LabelType) ([]*entities.Label, error) {
	var labels []*entities.Label
	err := r.db.WithContext(ctx).Table(r.tableName()).
		Where("label_type = ?", labelType).
		Order("scientific_name ASC").
		Find(&labels).Error
	return labels, err
}

// Search finds labels matching the query string.
func (r *labelRepository) Search(ctx context.Context, query string, limit int) ([]*entities.Label, error) {
	var labels []*entities.Label
	searchPattern := "%" + query + "%"
	err := r.db.WithContext(ctx).Table(r.tableName()).
		Where("scientific_name LIKE ?", searchPattern).
		Limit(limit).
		Order("scientific_name ASC").
		Find(&labels).Error
	return labels, err
}

// Count returns the total number of labels.
func (r *labelRepository) Count(ctx context.Context) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).Table(r.tableName()).Count(&count).Error
	return count, err
}

// CountByType returns the count of labels for a specific type.
func (r *labelRepository) CountByType(ctx context.Context, labelType entities.LabelType) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).Table(r.tableName()).
		Where("label_type = ?", labelType).
		Count(&count).Error
	return count, err
}

// GetAll retrieves all labels.
func (r *labelRepository) GetAll(ctx context.Context) ([]*entities.Label, error) {
	var labels []*entities.Label
	err := r.db.WithContext(ctx).Table(r.tableName()).
		Order("scientific_name ASC").
		Find(&labels).Error
	return labels, err
}

// Delete removes a label by ID.
func (r *labelRepository) Delete(ctx context.Context, id uint) error {
	result := r.db.WithContext(ctx).Table(r.tableName()).Delete(&entities.Label{}, id)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrLabelNotFound
	}
	return nil
}

// Exists checks if a label with the given ID exists.
func (r *labelRepository) Exists(ctx context.Context, id uint) (bool, error) {
	var count int64
	err := r.db.WithContext(ctx).Table(r.tableName()).
		Where("id = ?", id).
		Count(&count).Error
	return count > 0, err
}
