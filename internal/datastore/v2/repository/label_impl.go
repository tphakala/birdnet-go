package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// labelRepository implements LabelRepository.
type labelRepository struct {
	db          *gorm.DB
	useV2Prefix bool // For MySQL: use v2_ table prefix
	isMySQL     bool // For MySQL dialect
}

// NewLabelRepository creates a new LabelRepository.
func NewLabelRepository(db *gorm.DB, useV2Prefix, isMySQL bool) LabelRepository {
	return &labelRepository{
		db:          db,
		useV2Prefix: useV2Prefix,
		isMySQL:     isMySQL,
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
// Labels are unique per (scientific_name, model_id).
func (r *labelRepository) GetOrCreate(ctx context.Context, scientificName string, modelID, labelTypeID uint, taxonomicClassID *uint) (*entities.Label, error) {
	// Validate and normalize input
	scientificName = strings.TrimSpace(scientificName)
	if scientificName == "" {
		return nil, fmt.Errorf("scientific name cannot be empty")
	}

	var label entities.Label

	// Lookup by (scientific_name, model_id)
	err := r.db.WithContext(ctx).Table(r.tableName()).
		Where("scientific_name = ? AND model_id = ?", scientificName, modelID).
		First(&label).Error
	if err == nil {
		return &label, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}

	// Create new label
	label = entities.Label{
		ScientificName:   scientificName,
		ModelID:          modelID,
		LabelTypeID:      labelTypeID,
		TaxonomicClassID: taxonomicClassID,
	}

	createErr := r.db.WithContext(ctx).Table(r.tableName()).Create(&label).Error
	if createErr != nil {
		// Handle race condition - try to fetch the existing record
		findErr := r.db.WithContext(ctx).Table(r.tableName()).
			Where("scientific_name = ? AND model_id = ?", scientificName, modelID).
			First(&label).Error
		if findErr != nil {
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

// batchQuerySize is the maximum number of IDs to include in a single IN clause.
const batchQuerySize = 500

// GetByIDs retrieves multiple labels by their IDs.
func (r *labelRepository) GetByIDs(ctx context.Context, ids []uint) (map[uint]*entities.Label, error) {
	result := make(map[uint]*entities.Label)
	if len(ids) == 0 {
		return result, nil
	}

	for i := 0; i < len(ids); i += batchQuerySize {
		end := min(i+batchQuerySize, len(ids))
		batchIDs := ids[i:end]

		var labels []*entities.Label
		err := r.db.WithContext(ctx).Table(r.tableName()).
			Where("id IN ?", batchIDs).
			Find(&labels).Error
		if err != nil {
			return nil, fmt.Errorf("batch load labels: %w", err)
		}

		for _, label := range labels {
			result[label.ID] = label
		}
	}
	return result, nil
}

// GetByScientificNameAndModel retrieves a label by scientific name and model ID.
func (r *labelRepository) GetByScientificNameAndModel(ctx context.Context, name string, modelID uint) (*entities.Label, error) {
	var label entities.Label
	err := r.db.WithContext(ctx).Table(r.tableName()).
		Where("scientific_name = ? AND model_id = ?", name, modelID).
		First(&label).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrLabelNotFound
	}
	if err != nil {
		return nil, err
	}
	return &label, nil
}

// GetByScientificNamesAndModel retrieves multiple labels by scientific names for a specific model.
func (r *labelRepository) GetByScientificNamesAndModel(ctx context.Context, names []string, modelID uint) ([]*entities.Label, error) {
	if len(names) == 0 {
		return []*entities.Label{}, nil
	}

	var result []*entities.Label
	for i := 0; i < len(names); i += batchQuerySize {
		end := min(i+batchQuerySize, len(names))
		batchNames := names[i:end]

		var labels []*entities.Label
		err := r.db.WithContext(ctx).Table(r.tableName()).
			Where("scientific_name IN ? AND model_id = ?", batchNames, modelID).
			Find(&labels).Error
		if err != nil {
			return nil, fmt.Errorf("batch load labels: %w", err)
		}
		result = append(result, labels...)
	}
	return result, nil
}

// BatchGetOrCreate retrieves or creates multiple labels in optimized batches.
func (r *labelRepository) BatchGetOrCreate(ctx context.Context, scientificNames []string, modelID, labelTypeID uint, taxonomicClassID *uint) (map[string]*entities.Label, error) {
	if len(scientificNames) == 0 {
		return make(map[string]*entities.Label), nil
	}

	// Deduplicate and validate input
	uniqueNames := make(map[string]struct{}, len(scientificNames))
	for _, name := range scientificNames {
		trimmedName := strings.TrimSpace(name)
		if trimmedName != "" {
			uniqueNames[trimmedName] = struct{}{}
		}
	}

	if len(uniqueNames) == 0 {
		return make(map[string]*entities.Label), nil
	}

	nameList := make([]string, 0, len(uniqueNames))
	for name := range uniqueNames {
		nameList = append(nameList, name)
	}

	result := make(map[string]*entities.Label, len(nameList))

	// Step 1: Fetch existing labels for this model
	existing, err := r.GetByScientificNamesAndModel(ctx, nameList, modelID)
	if err != nil {
		return nil, fmt.Errorf("batch fetch existing labels: %w", err)
	}

	for _, label := range existing {
		result[label.ScientificName] = label
	}

	// Step 2: Identify missing names
	var missing []string
	for _, name := range nameList {
		if _, found := result[name]; !found {
			missing = append(missing, name)
		}
	}

	if len(missing) == 0 {
		return result, nil
	}

	// Step 3: Bulk insert missing labels
	newLabels := make([]entities.Label, len(missing))
	for i, name := range missing {
		newLabels[i] = entities.Label{
			ScientificName:   name,
			ModelID:          modelID,
			LabelTypeID:      labelTypeID,
			TaxonomicClassID: taxonomicClassID,
		}
	}

	if err := r.db.WithContext(ctx).Table(r.tableName()).
		Clauses(clause.OnConflict{DoNothing: true}).
		CreateInBatches(newLabels, batchQuerySize).Error; err != nil {
		return nil, fmt.Errorf("batch create labels: %w", err)
	}

	// Step 4: Re-fetch to get IDs
	created, err := r.GetByScientificNamesAndModel(ctx, missing, modelID)
	if err != nil {
		return nil, fmt.Errorf("fetch created labels: %w", err)
	}

	for _, label := range created {
		result[label.ScientificName] = label
	}

	return result, nil
}

// GetAllByModel retrieves all labels for a specific model.
func (r *labelRepository) GetAllByModel(ctx context.Context, modelID uint) ([]*entities.Label, error) {
	var labels []*entities.Label
	err := r.db.WithContext(ctx).Table(r.tableName()).
		Where("model_id = ?", modelID).
		Order("scientific_name ASC").
		Find(&labels).Error
	return labels, err
}

// GetAllByLabelType retrieves all labels of a specific label type.
func (r *labelRepository) GetAllByLabelType(ctx context.Context, labelTypeID uint) ([]*entities.Label, error) {
	var labels []*entities.Label
	err := r.db.WithContext(ctx).Table(r.tableName()).
		Where("label_type_id = ?", labelTypeID).
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

// CountByModel returns the count of labels for a specific model.
func (r *labelRepository) CountByModel(ctx context.Context, modelID uint) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).Table(r.tableName()).
		Where("model_id = ?", modelID).
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

// GetByScientificName retrieves all labels matching a scientific name across all models.
func (r *labelRepository) GetByScientificName(ctx context.Context, name string) ([]*entities.Label, error) {
	var labels []*entities.Label
	err := r.db.WithContext(ctx).Table(r.tableName()).
		Where("scientific_name = ?", name).
		Find(&labels).Error
	return labels, err
}

// GetLabelIDsByScientificName retrieves label IDs for a scientific name across all models.
func (r *labelRepository) GetLabelIDsByScientificName(ctx context.Context, name string) ([]uint, error) {
	var ids []uint
	err := r.db.WithContext(ctx).Table(r.tableName()).
		Select("id").
		Where("scientific_name = ?", name).
		Pluck("id", &ids).Error
	return ids, err
}

// labelTypeRepository implements LabelTypeRepository.
type labelTypeRepository struct {
	db          *gorm.DB
	useV2Prefix bool
}

// NewLabelTypeRepository creates a new LabelTypeRepository.
func NewLabelTypeRepository(db *gorm.DB, useV2Prefix bool) LabelTypeRepository {
	return &labelTypeRepository{
		db:          db,
		useV2Prefix: useV2Prefix,
	}
}

func (r *labelTypeRepository) tableName() string {
	if r.useV2Prefix {
		return tableV2LabelTypes
	}
	return tableLabelTypes
}

func (r *labelTypeRepository) GetByName(ctx context.Context, name string) (*entities.LabelType, error) {
	var lt entities.LabelType
	err := r.db.WithContext(ctx).Table(r.tableName()).
		Where("name = ?", name).
		First(&lt).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrLabelTypeNotFound
	}
	if err != nil {
		return nil, err
	}
	return &lt, nil
}

func (r *labelTypeRepository) GetByID(ctx context.Context, id uint) (*entities.LabelType, error) {
	var lt entities.LabelType
	err := r.db.WithContext(ctx).Table(r.tableName()).First(&lt, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrLabelTypeNotFound
	}
	if err != nil {
		return nil, err
	}
	return &lt, nil
}

func (r *labelTypeRepository) GetAll(ctx context.Context) ([]*entities.LabelType, error) {
	var types []*entities.LabelType
	err := r.db.WithContext(ctx).Table(r.tableName()).
		Order("name ASC").
		Find(&types).Error
	return types, err
}

func (r *labelTypeRepository) GetOrCreate(ctx context.Context, name string) (*entities.LabelType, error) {
	var lt entities.LabelType
	err := r.db.WithContext(ctx).Table(r.tableName()).
		Where("name = ?", name).
		First(&lt).Error
	if err == nil {
		return &lt, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}

	lt = entities.LabelType{Name: name}
	if err := r.db.WithContext(ctx).Table(r.tableName()).Create(&lt).Error; err != nil {
		// Race condition - try to fetch again
		if findErr := r.db.WithContext(ctx).Table(r.tableName()).Where("name = ?", name).First(&lt).Error; findErr != nil {
			return nil, err
		}
	}
	return &lt, nil
}

// taxonomicClassRepository implements TaxonomicClassRepository.
type taxonomicClassRepository struct {
	db          *gorm.DB
	useV2Prefix bool
}

// NewTaxonomicClassRepository creates a new TaxonomicClassRepository.
func NewTaxonomicClassRepository(db *gorm.DB, useV2Prefix bool) TaxonomicClassRepository {
	return &taxonomicClassRepository{
		db:          db,
		useV2Prefix: useV2Prefix,
	}
}

func (r *taxonomicClassRepository) tableName() string {
	if r.useV2Prefix {
		return tableV2TaxonomicClasses
	}
	return tableTaxonomicClasses
}

func (r *taxonomicClassRepository) GetByName(ctx context.Context, name string) (*entities.TaxonomicClass, error) {
	var tc entities.TaxonomicClass
	err := r.db.WithContext(ctx).Table(r.tableName()).
		Where("name = ?", name).
		First(&tc).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrTaxonomicClassNotFound
	}
	if err != nil {
		return nil, err
	}
	return &tc, nil
}

func (r *taxonomicClassRepository) GetByID(ctx context.Context, id uint) (*entities.TaxonomicClass, error) {
	var tc entities.TaxonomicClass
	err := r.db.WithContext(ctx).Table(r.tableName()).First(&tc, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrTaxonomicClassNotFound
	}
	if err != nil {
		return nil, err
	}
	return &tc, nil
}

func (r *taxonomicClassRepository) GetAll(ctx context.Context) ([]*entities.TaxonomicClass, error) {
	var classes []*entities.TaxonomicClass
	err := r.db.WithContext(ctx).Table(r.tableName()).
		Order("name ASC").
		Find(&classes).Error
	return classes, err
}

func (r *taxonomicClassRepository) GetOrCreate(ctx context.Context, name string) (*entities.TaxonomicClass, error) {
	var tc entities.TaxonomicClass
	err := r.db.WithContext(ctx).Table(r.tableName()).
		Where("name = ?", name).
		First(&tc).Error
	if err == nil {
		return &tc, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}

	tc = entities.TaxonomicClass{Name: name}
	if err := r.db.WithContext(ctx).Table(r.tableName()).Create(&tc).Error; err != nil {
		// Race condition - try to fetch again
		if findErr := r.db.WithContext(ctx).Table(r.tableName()).Where("name = ?", name).First(&tc).Error; findErr != nil {
			return nil, err
		}
	}
	return &tc, nil
}
