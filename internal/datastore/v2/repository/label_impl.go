package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"

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

// modelLabelsTable returns the appropriate model_labels table name.
func (r *labelRepository) modelLabelsTable() string {
	if r.useV2Prefix {
		return tableV2ModelLabels
	}
	return tableModelLabels
}

// GetRawLabelForLabel retrieves the raw_label from model_labels for a given model/label pair.
func (r *labelRepository) GetRawLabelForLabel(ctx context.Context, modelID, labelID uint) (string, error) {
	var rawLabel string
	err := r.db.WithContext(ctx).Table(r.modelLabelsTable()).
		Select("raw_label").
		Where("model_id = ? AND label_id = ?", modelID, labelID).
		Limit(1).
		Scan(&rawLabel).Error
	if err != nil {
		return "", err
	}
	return rawLabel, nil
}

// rawLabelBatchSize limits the number of pairs per query to stay under SQL parameter limits.
// Each pair uses 2 parameters, so 400 pairs = 800 parameters (safely under SQLite's 999 limit).
const rawLabelBatchSize = 400

// GetRawLabelsForLabels batch retrieves raw_labels from model_labels for multiple model/label pairs.
// Returns a map keyed by "modelID:labelID" string for efficient lookup.
// Automatically chunks large requests to avoid SQL parameter limits.
func (r *labelRepository) GetRawLabelsForLabels(ctx context.Context, pairs []ModelLabelPair) (map[string]string, error) {
	result := make(map[string]string)
	if len(pairs) == 0 {
		return result, nil
	}

	// Process in chunks to avoid SQL parameter limits
	for start := 0; start < len(pairs); start += rawLabelBatchSize {
		end := min(start+rawLabelBatchSize, len(pairs))
		chunk := pairs[start:end]

		if err := r.fetchRawLabelsChunk(ctx, chunk, result); err != nil {
			return nil, err
		}
	}

	return result, nil
}

// fetchRawLabelsChunk fetches raw_labels for a single chunk of pairs.
func (r *labelRepository) fetchRawLabelsChunk(ctx context.Context, pairs []ModelLabelPair, result map[string]string) error {
	type modelLabelRaw struct {
		ModelID  uint
		LabelID  uint
		RawLabel string
	}
	var rows []modelLabelRaw

	// Build WHERE clause: (model_id = ? AND label_id = ?) OR ...
	args := make([]any, 0, len(pairs)*2)
	var whereBuilder strings.Builder
	for i, pair := range pairs {
		if i > 0 {
			whereBuilder.WriteString(" OR ")
		}
		whereBuilder.WriteString("(model_id = ? AND label_id = ?)")
		args = append(args, pair.ModelID, pair.LabelID)
	}

	err := r.db.WithContext(ctx).Table(r.modelLabelsTable()).
		Select("model_id, label_id, raw_label").
		Where(whereBuilder.String(), args...).
		Scan(&rows).Error
	if err != nil {
		return err
	}

	// Add to result map
	for _, row := range rows {
		key := fmt.Sprintf("%d:%d", row.ModelID, row.LabelID)
		result[key] = row.RawLabel
	}

	return nil
}
