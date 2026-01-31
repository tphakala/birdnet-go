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
	isMySQL     bool // For API consistency; currently unused here (used by detection_impl.go for dialect-specific SQL)
}

// NewLabelRepository creates a new LabelRepository.
// Parameters:
//   - db: GORM database connection
//   - useV2Prefix: true to use v2_ table prefix (MySQL migration mode)
//   - isMySQL: true for MySQL dialect (affects date/time SQL expressions)
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
// Returns an error if scientificName is empty or whitespace-only.
// Input is normalized (trimmed) before use.
func (r *labelRepository) GetOrCreate(ctx context.Context, scientificName string, labelType entities.LabelType) (*entities.Label, error) {
	// Validate and normalize input - reject empty or whitespace-only names
	scientificName = strings.TrimSpace(scientificName)
	if scientificName == "" {
		return nil, fmt.Errorf("scientific name cannot be empty")
	}

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

	// Create new label with normalized name
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

// batchQuerySize is the maximum number of IDs to include in a single IN clause.
// SQLite has a default limit of 999 parameters; we use 500 to be safe.
const batchQuerySize = 500

// GetByIDs retrieves multiple labels by their IDs.
// Handles large ID sets by chunking to avoid SQL parameter limits.
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

// GetByScientificNames retrieves multiple labels by scientific names in a single query.
// Handles large name sets by chunking to avoid SQL parameter limits.
func (r *labelRepository) GetByScientificNames(ctx context.Context, names []string) ([]*entities.Label, error) {
	if len(names) == 0 {
		return []*entities.Label{}, nil
	}

	var result []*entities.Label
	for i := 0; i < len(names); i += batchQuerySize {
		end := min(i+batchQuerySize, len(names))
		batchNames := names[i:end]

		var labels []*entities.Label
		err := r.db.WithContext(ctx).Table(r.tableName()).
			Where("scientific_name IN ?", batchNames).
			Find(&labels).Error
		if err != nil {
			return nil, fmt.Errorf("batch load labels by scientific names: %w", err)
		}
		result = append(result, labels...)
	}
	return result, nil
}

// BatchGetOrCreate retrieves or creates multiple labels in optimized batches.
// Returns a map of scientificName -> Label for all requested names.
// Only labels matching both the scientific name AND the specified labelType are returned.
func (r *labelRepository) BatchGetOrCreate(ctx context.Context, scientificNames []string, labelType entities.LabelType) (map[string]*entities.Label, error) {
	if len(scientificNames) == 0 {
		return make(map[string]*entities.Label), nil
	}

	// Deduplicate and validate input - trim whitespace and filter empty names
	uniqueNames := make(map[string]struct{}, len(scientificNames))
	for _, name := range scientificNames {
		trimmedName := strings.TrimSpace(name)
		if trimmedName != "" {
			uniqueNames[trimmedName] = struct{}{}
		}
	}

	// Early return if no valid names after filtering
	if len(uniqueNames) == 0 {
		return make(map[string]*entities.Label), nil
	}

	nameList := make([]string, 0, len(uniqueNames))
	for name := range uniqueNames {
		nameList = append(nameList, name)
	}

	result := make(map[string]*entities.Label, len(nameList))

	// Step 1: Fetch existing labels with matching type (chunked for SQL limits)
	existing, err := r.getByScientificNamesWithType(ctx, nameList, labelType)
	if err != nil {
		return nil, fmt.Errorf("batch fetch existing labels: %w", err)
	}

	// Build map of found names
	for _, label := range existing {
		if label.ScientificName != nil {
			result[*label.ScientificName] = label
		}
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

	// Step 3: Bulk insert missing labels with ON CONFLICT DO NOTHING
	// This handles concurrent inserts gracefully
	newLabels := make([]entities.Label, len(missing))
	for i, name := range missing {
		nameCopy := name // Avoid closure issues
		newLabels[i] = entities.Label{
			ScientificName: &nameCopy,
			LabelType:      labelType,
		}
	}

	if err := r.db.WithContext(ctx).Table(r.tableName()).
		Clauses(clause.OnConflict{DoNothing: true}).
		CreateInBatches(newLabels, batchQuerySize).Error; err != nil {
		return nil, fmt.Errorf("batch create labels: %w", err)
	}

	// Step 4: Re-fetch to get IDs (with type filter)
	// This is necessary because:
	// - ON CONFLICT DO NOTHING may not return IDs for conflicting rows
	// - Some GORM drivers don't populate IDs after bulk insert with conflicts
	// - This handles race conditions where another process inserted the same labels
	created, err := r.getByScientificNamesWithType(ctx, missing, labelType)
	if err != nil {
		return nil, fmt.Errorf("fetch created labels: %w", err)
	}

	for _, label := range created {
		if label.ScientificName != nil {
			result[*label.ScientificName] = label
		}
	}

	return result, nil
}

// getByScientificNamesWithType retrieves labels by scientific names filtered by type.
// This is an internal helper for BatchGetOrCreate to ensure type-safe lookups.
func (r *labelRepository) getByScientificNamesWithType(ctx context.Context, names []string, labelType entities.LabelType) ([]*entities.Label, error) {
	if len(names) == 0 {
		return []*entities.Label{}, nil
	}

	var result []*entities.Label
	for i := 0; i < len(names); i += batchQuerySize {
		end := min(i+batchQuerySize, len(names))
		batchNames := names[i:end]

		var labels []*entities.Label
		err := r.db.WithContext(ctx).Table(r.tableName()).
			Where("scientific_name IN ? AND label_type = ?", batchNames, labelType).
			Find(&labels).Error
		if err != nil {
			return nil, fmt.Errorf("batch load labels by scientific names with type: %w", err)
		}
		result = append(result, labels...)
	}
	return result, nil
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
