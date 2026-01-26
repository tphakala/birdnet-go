package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"gorm.io/gorm"
)

// Sort field constants for Search queries.
const (
	sortFieldDetectedAt = "detected_at"
	sortFieldConfidence = "confidence"
)

// defaultDBBatchSize is the batch size for bulk database operations.
const defaultDBBatchSize = 100

// detectionRepository implements DetectionRepository.
type detectionRepository struct {
	db          *gorm.DB
	useV2Prefix bool
}

// NewDetectionRepository creates a new DetectionRepository.
// Set useV2Prefix to true for MySQL to use v2_ table prefix.
func NewDetectionRepository(db *gorm.DB, useV2Prefix bool) DetectionRepository {
	return &detectionRepository{
		db:          db,
		useV2Prefix: useV2Prefix,
	}
}

// Table name helpers
func (r *detectionRepository) tableName() string {
	if r.useV2Prefix {
		return tableV2Detections
	}
	return tableDetections
}

func (r *detectionRepository) predictionsTable() string {
	if r.useV2Prefix {
		return tableV2DetectionPredictions
	}
	return tableDetectionPredictions
}

func (r *detectionRepository) reviewsTable() string {
	if r.useV2Prefix {
		return tableV2DetectionReviews
	}
	return tableDetectionReviews
}

func (r *detectionRepository) commentsTable() string {
	if r.useV2Prefix {
		return tableV2DetectionComments
	}
	return tableDetectionComments
}

func (r *detectionRepository) locksTable() string {
	if r.useV2Prefix {
		return tableV2DetectionLocks
	}
	return tableDetectionLocks
}

func (r *detectionRepository) labelsTable() string {
	if r.useV2Prefix {
		return tableV2Labels
	}
	return tableLabels
}

func (r *detectionRepository) modelsTable() string {
	if r.useV2Prefix {
		return tableV2AIModels
	}
	return tableAIModels
}

func (r *detectionRepository) sourcesTable() string {
	if r.useV2Prefix {
		return tableV2AudioSources
	}
	return tableAudioSources
}

// dateFromUnixExpr returns a SQL expression to extract DATE from a Unix timestamp.
// SQLite: DATE(datetime(column, 'unixepoch'))
// MySQL:  DATE(FROM_UNIXTIME(column))
func (r *detectionRepository) dateFromUnixExpr(column string) string {
	if r.useV2Prefix {
		// MySQL
		return fmt.Sprintf("DATE(FROM_UNIXTIME(%s))", column)
	}
	// SQLite
	return fmt.Sprintf("DATE(datetime(%s, 'unixepoch'))", column)
}

// ============================================================================
// CRUD Operations
// ============================================================================

// Save persists a new detection.
func (r *detectionRepository) Save(ctx context.Context, det *entities.Detection) error {
	return r.db.WithContext(ctx).Table(r.tableName()).Create(det).Error
}

// SaveWithID persists a detection with a specific ID (for migration).
// GORM's Create() respects pre-set IDs, so no special handling is needed.
func (r *detectionRepository) SaveWithID(ctx context.Context, det *entities.Detection) error {
	return r.db.WithContext(ctx).Table(r.tableName()).Create(det).Error
}

// Get retrieves a detection by ID.
func (r *detectionRepository) Get(ctx context.Context, id uint) (*entities.Detection, error) {
	var det entities.Detection
	err := r.db.WithContext(ctx).Table(r.tableName()).First(&det, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrDetectionNotFound
	}
	if err != nil {
		return nil, err
	}
	return &det, nil
}

// GetWithRelations retrieves a detection with preloaded Label, Model, and Source.
// Note: Due to v2_ prefix concerns, we manually join rather than using Preload.
func (r *detectionRepository) GetWithRelations(ctx context.Context, id uint) (*entities.Detection, error) {
	var det entities.Detection
	err := r.db.WithContext(ctx).Table(r.tableName()).First(&det, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrDetectionNotFound
	}
	if err != nil {
		return nil, err
	}

	// Manually load relations - errors other than not-found are returned
	var label entities.Label
	if err := r.db.WithContext(ctx).Table(r.labelsTable()).First(&label, det.LabelID).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("failed to load label: %w", err)
		}
	} else {
		det.Label = &label
	}

	var model entities.AIModel
	if err := r.db.WithContext(ctx).Table(r.modelsTable()).First(&model, det.ModelID).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("failed to load model: %w", err)
		}
	} else {
		det.Model = &model
	}

	if det.SourceID != nil {
		var source entities.AudioSource
		if err := r.db.WithContext(ctx).Table(r.sourcesTable()).First(&source, *det.SourceID).Error; err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, fmt.Errorf("failed to load source: %w", err)
			}
		} else {
			det.Source = &source
		}
	}

	return &det, nil
}

// Update modifies specific fields of a detection.
// Uses atomic operation to prevent TOCTOU race with lock checks.
func (r *detectionRepository) Update(ctx context.Context, id uint, updates map[string]any) error {
	// Atomic update: only update if not locked
	// Use WHERE NOT EXISTS to combine lock check with update
	result := r.db.WithContext(ctx).Table(r.tableName()).
		Where("id = ?", id).
		Where(fmt.Sprintf("NOT EXISTS (SELECT 1 FROM %s WHERE %s.detection_id = ?)",
			r.locksTable(), r.locksTable()), id).
		Updates(updates)

	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		// Could be: not found OR locked. Check which.
		exists, err := r.Exists(ctx, id)
		if err != nil {
			return err
		}
		if !exists {
			return ErrDetectionNotFound
		}
		return ErrDetectionLocked
	}
	return nil
}

// Delete removes a detection by ID.
// Uses atomic operation to prevent TOCTOU race with lock checks.
func (r *detectionRepository) Delete(ctx context.Context, id uint) error {
	// Atomic delete: only delete if not locked
	// Use raw SQL with NOT EXISTS to combine lock check with delete
	result := r.db.WithContext(ctx).Exec(
		fmt.Sprintf("DELETE FROM %s WHERE id = ? AND NOT EXISTS (SELECT 1 FROM %s WHERE %s.detection_id = ?)",
			r.tableName(), r.locksTable(), r.locksTable()),
		id, id)

	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		// Could be: not found OR locked. Check which.
		exists, err := r.Exists(ctx, id)
		if err != nil {
			return err
		}
		if !exists {
			return ErrDetectionNotFound
		}
		return ErrDetectionLocked
	}
	return nil
}

// ============================================================================
// Batch Operations
// ============================================================================

// SaveBatch persists multiple detections in a single transaction.
func (r *detectionRepository) SaveBatch(ctx context.Context, dets []*entities.Detection) error {
	if len(dets) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Table(r.tableName()).CreateInBatches(dets, defaultDBBatchSize).Error
}

// DeleteBatch removes multiple detections by ID.
func (r *detectionRepository) DeleteBatch(ctx context.Context, ids []uint) error {
	if len(ids) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Table(r.tableName()).Delete(&entities.Detection{}, ids).Error
}

// ============================================================================
// Query Methods
// ============================================================================

// GetRecent retrieves the most recent detections with relations.
func (r *detectionRepository) GetRecent(ctx context.Context, limit int) ([]*entities.Detection, error) {
	var dets []*entities.Detection
	err := r.db.WithContext(ctx).Table(r.tableName()).
		Order("detected_at DESC").
		Limit(limit).
		Find(&dets).Error
	if err != nil {
		return nil, err
	}

	// Load relations for each detection
	r.loadRelationsForDetections(ctx, dets)
	return dets, nil
}

// loadRelationsForDetections loads Label, Model, Source for multiple detections.
func (r *detectionRepository) loadRelationsForDetections(ctx context.Context, dets []*entities.Detection) {
	if len(dets) == 0 {
		return
	}

	// Collect unique IDs
	labelIDs := make(map[uint]bool)
	modelIDs := make(map[uint]bool)
	sourceIDs := make(map[uint]bool)
	for _, d := range dets {
		labelIDs[d.LabelID] = true
		modelIDs[d.ModelID] = true
		if d.SourceID != nil {
			sourceIDs[*d.SourceID] = true
		}
	}

	// Load labels
	labels := make(map[uint]*entities.Label)
	if len(labelIDs) > 0 {
		ids := mapKeysToSlice(labelIDs)
		var labelList []*entities.Label
		r.db.WithContext(ctx).Table(r.labelsTable()).Where("id IN ?", ids).Find(&labelList)
		for _, l := range labelList {
			labels[l.ID] = l
		}
	}

	// Load models
	models := make(map[uint]*entities.AIModel)
	if len(modelIDs) > 0 {
		ids := mapKeysToSlice(modelIDs)
		var modelList []*entities.AIModel
		r.db.WithContext(ctx).Table(r.modelsTable()).Where("id IN ?", ids).Find(&modelList)
		for _, m := range modelList {
			models[m.ID] = m
		}
	}

	// Load sources
	sources := make(map[uint]*entities.AudioSource)
	if len(sourceIDs) > 0 {
		ids := mapKeysToSlice(sourceIDs)
		var sourceList []*entities.AudioSource
		r.db.WithContext(ctx).Table(r.sourcesTable()).Where("id IN ?", ids).Find(&sourceList)
		for _, s := range sourceList {
			sources[s.ID] = s
		}
	}

	// Assign relations
	for _, d := range dets {
		d.Label = labels[d.LabelID]
		d.Model = models[d.ModelID]
		if d.SourceID != nil {
			d.Source = sources[*d.SourceID]
		}
	}
}

func mapKeysToSlice(m map[uint]bool) []uint {
	result := make([]uint, 0, len(m))
	for k := range m {
		result = append(result, k)
	}
	return result
}

// GetByLabel retrieves detections for a specific label.
func (r *detectionRepository) GetByLabel(ctx context.Context, labelID uint, limit, offset int) ([]*entities.Detection, int64, error) {
	var dets []*entities.Detection
	var total int64

	query := r.db.WithContext(ctx).Table(r.tableName()).Where("label_id = ?", labelID)

	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	err := query.Order("detected_at DESC").Limit(limit).Offset(offset).Find(&dets).Error
	return dets, total, err
}

// GetByModel retrieves detections for a specific AI model.
func (r *detectionRepository) GetByModel(ctx context.Context, modelID uint, limit, offset int) ([]*entities.Detection, int64, error) {
	var dets []*entities.Detection
	var total int64

	query := r.db.WithContext(ctx).Table(r.tableName()).Where("model_id = ?", modelID)

	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	err := query.Order("detected_at DESC").Limit(limit).Offset(offset).Find(&dets).Error
	return dets, total, err
}

// GetByDateRange retrieves detections within a Unix timestamp range.
func (r *detectionRepository) GetByDateRange(ctx context.Context, start, end int64, limit, offset int) ([]*entities.Detection, int64, error) {
	var dets []*entities.Detection
	var total int64

	query := r.db.WithContext(ctx).Table(r.tableName()).
		Where("detected_at >= ? AND detected_at <= ?", start, end)

	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	err := query.Order("detected_at DESC").Limit(limit).Offset(offset).Find(&dets).Error
	return dets, total, err
}

// GetByHour retrieves detections starting at a specific Unix timestamp hour.
func (r *detectionRepository) GetByHour(ctx context.Context, hourStart int64, limit, offset int) ([]*entities.Detection, int64, error) {
	hourEnd := hourStart + 3600 // 1 hour in seconds
	return r.GetByDateRange(ctx, hourStart, hourEnd, limit, offset)
}

// GetByAudioSource retrieves detections for a specific audio source.
func (r *detectionRepository) GetByAudioSource(ctx context.Context, sourceID uint, limit, offset int) ([]*entities.Detection, int64, error) {
	var dets []*entities.Detection
	var total int64

	query := r.db.WithContext(ctx).Table(r.tableName()).Where("source_id = ?", sourceID)

	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	err := query.Order("detected_at DESC").Limit(limit).Offset(offset).Find(&dets).Error
	return dets, total, err
}

// ============================================================================
// Search
// ============================================================================

// Search finds detections matching the given filters.
func (r *detectionRepository) Search(ctx context.Context, filters *SearchFilters) ([]*entities.Detection, int64, error) {
	var dets []*entities.Detection
	var total int64

	query := r.db.WithContext(ctx).Table(r.tableName())

	// Apply filters
	if len(filters.LabelIDs) > 0 {
		query = query.Where("label_id IN ?", filters.LabelIDs)
	}
	if filters.ModelID != nil {
		query = query.Where("model_id = ?", *filters.ModelID)
	}
	if filters.AudioSourceID != nil {
		query = query.Where("source_id = ?", *filters.AudioSourceID)
	}
	if filters.StartTime != nil {
		query = query.Where("detected_at >= ?", *filters.StartTime)
	}
	if filters.EndTime != nil {
		query = query.Where("detected_at <= ?", *filters.EndTime)
	}
	if filters.MinConfidence != nil {
		query = query.Where("confidence >= ?", *filters.MinConfidence)
	}
	if filters.MaxConfidence != nil {
		query = query.Where("confidence <= ?", *filters.MaxConfidence)
	}

	// Text search on scientific name (requires join)
	if filters.Query != "" {
		query = query.Joins(fmt.Sprintf("JOIN %s ON %s.id = %s.label_id",
			r.labelsTable(), r.labelsTable(), r.tableName())).
			Where(fmt.Sprintf("%s.scientific_name LIKE ?", r.labelsTable()), "%"+filters.Query+"%")
	}

	// Verified filter (requires join with reviews)
	if filters.Verified != nil {
		query = query.Joins(fmt.Sprintf("JOIN %s ON %s.detection_id = %s.id",
			r.reviewsTable(), r.reviewsTable(), r.tableName())).
			Where(fmt.Sprintf("%s.verified = ?", r.reviewsTable()), string(*filters.Verified))
	}

	// Locked filter (requires join with locks)
	if filters.IsLocked != nil {
		if *filters.IsLocked {
			query = query.Joins(fmt.Sprintf("JOIN %s ON %s.detection_id = %s.id",
				r.locksTable(), r.locksTable(), r.tableName()))
		} else {
			query = query.Where(fmt.Sprintf("NOT EXISTS (SELECT 1 FROM %s WHERE %s.detection_id = %s.id)",
				r.locksTable(), r.locksTable(), r.tableName()))
		}
	}

	// Count total
	countQuery := query.Session(&gorm.Session{})
	if err := countQuery.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	// Sorting
	sortField := sortFieldDetectedAt
	if filters.SortBy != "" {
		switch filters.SortBy {
		case sortFieldConfidence:
			sortField = sortFieldConfidence
		case sortFieldDetectedAt:
			sortField = sortFieldDetectedAt
		}
	}
	order := sortField
	if filters.SortDesc {
		order += " DESC"
	} else {
		order += " ASC"
	}
	query = query.Order(order)

	// Pagination
	if filters.Limit > 0 {
		query = query.Limit(filters.Limit)
	}
	if filters.Offset > 0 {
		query = query.Offset(filters.Offset)
	}

	err := query.Find(&dets).Error
	return dets, total, err
}

// ============================================================================
// Counts
// ============================================================================

// CountAll returns the total number of detections.
func (r *detectionRepository) CountAll(ctx context.Context) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).Table(r.tableName()).Count(&count).Error
	return count, err
}

// CountByLabel returns the count of detections for a specific label.
func (r *detectionRepository) CountByLabel(ctx context.Context, labelID uint) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).Table(r.tableName()).
		Where("label_id = ?", labelID).
		Count(&count).Error
	return count, err
}

// CountByModel returns the count of detections for a specific model.
func (r *detectionRepository) CountByModel(ctx context.Context, modelID uint) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).Table(r.tableName()).
		Where("model_id = ?", modelID).
		Count(&count).Error
	return count, err
}

// CountByDateRange returns the count of detections in a Unix timestamp range.
func (r *detectionRepository) CountByDateRange(ctx context.Context, start, end int64) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).Table(r.tableName()).
		Where("detected_at >= ? AND detected_at <= ?", start, end).
		Count(&count).Error
	return count, err
}

// CountByHour returns the count of detections in a specific hour.
func (r *detectionRepository) CountByHour(ctx context.Context, hourStart int64) (int64, error) {
	return r.CountByDateRange(ctx, hourStart, hourStart+3600)
}

// ============================================================================
// Aggregations
// ============================================================================

// GetTopSpecies returns the most frequently detected species in a time range.
func (r *detectionRepository) GetTopSpecies(ctx context.Context, start, end int64, minConfidence float64, modelID *uint, limit int) ([]SpeciesCount, error) {
	var results []SpeciesCount

	query := r.db.WithContext(ctx).Table(r.tableName()).
		Select(fmt.Sprintf("%s.label_id, %s.scientific_name, COUNT(*) as count",
			r.tableName(), r.labelsTable())).
		Joins(fmt.Sprintf("JOIN %s ON %s.id = %s.label_id",
			r.labelsTable(), r.labelsTable(), r.tableName())).
		Where(fmt.Sprintf("%s.detected_at >= ? AND %s.detected_at <= ?", r.tableName(), r.tableName()), start, end).
		Where(fmt.Sprintf("%s.confidence >= ?", r.tableName()), minConfidence)

	if modelID != nil {
		query = query.Where(fmt.Sprintf("%s.model_id = ?", r.tableName()), *modelID)
	}

	err := query.Group("label_id").
		Order("count DESC").
		Limit(limit).
		Scan(&results).Error

	return results, err
}

// GetHourlyOccurrences returns detection counts by hour (0-23) for a label.
func (r *detectionRepository) GetHourlyOccurrences(ctx context.Context, labelID uint, start, end int64) ([24]int, error) {
	var counts [24]int

	type hourCount struct {
		Hour  int
		Count int
	}
	var results []hourCount

	// Extract hour from Unix timestamp
	err := r.db.WithContext(ctx).Table(r.tableName()).
		Select("(detected_at / 3600) % 24 as hour, COUNT(*) as count").
		Where("label_id = ? AND detected_at >= ? AND detected_at <= ?", labelID, start, end).
		Group("hour").
		Scan(&results).Error

	if err != nil {
		return counts, err
	}

	for _, r := range results {
		if r.Hour >= 0 && r.Hour < 24 {
			counts[r.Hour] = r.Count
		}
	}

	return counts, nil
}

// GetDailyOccurrences returns daily detection counts for a label.
func (r *detectionRepository) GetDailyOccurrences(ctx context.Context, labelID uint, start, end int64) ([]DailyCount, error) {
	var results []DailyCount

	// Group by date using dialect-appropriate date conversion
	dateExpr := r.dateFromUnixExpr("detected_at")
	err := r.db.WithContext(ctx).Table(r.tableName()).
		Select(fmt.Sprintf("%s as date, COUNT(*) as count", dateExpr)).
		Where("label_id = ? AND detected_at >= ? AND detected_at <= ?", labelID, start, end).
		Group("date").
		Order("date ASC").
		Scan(&results).Error

	return results, err
}

// GetSpeciesFirstDetection returns the first-ever detection of a species.
func (r *detectionRepository) GetSpeciesFirstDetection(ctx context.Context, labelID uint) (*entities.Detection, error) {
	var det entities.Detection
	err := r.db.WithContext(ctx).Table(r.tableName()).
		Where("label_id = ?", labelID).
		Order("detected_at ASC").
		First(&det).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrDetectionNotFound
	}
	return &det, err
}

// GetAllDetectedLabels returns IDs of all labels that have at least one detection.
func (r *detectionRepository) GetAllDetectedLabels(ctx context.Context) ([]uint, error) {
	var labelIDs []uint
	err := r.db.WithContext(ctx).Table(r.tableName()).
		Distinct("label_id").
		Pluck("label_id", &labelIDs).Error
	return labelIDs, err
}

// ============================================================================
// Model-Specific Statistics
// ============================================================================

// GetModelStats returns aggregate statistics for a specific model.
func (r *detectionRepository) GetModelStats(ctx context.Context, modelID uint) (*ModelStats, error) {
	var stats ModelStats
	stats.ModelID = modelID

	// Total detections
	if err := r.db.WithContext(ctx).Table(r.tableName()).
		Where("model_id = ?", modelID).
		Count(&stats.TotalDetections).Error; err != nil {
		return nil, err
	}

	// Unique species
	if err := r.db.WithContext(ctx).Table(r.tableName()).
		Where("model_id = ?", modelID).
		Distinct("label_id").
		Count(&stats.UniqueSpecies).Error; err != nil {
		return nil, err
	}

	// First and last detection
	type timeRange struct {
		MinTime int64
		MaxTime int64
	}
	var tr timeRange
	err := r.db.WithContext(ctx).Table(r.tableName()).
		Select("MIN(detected_at) as min_time, MAX(detected_at) as max_time").
		Where("model_id = ?", modelID).
		Scan(&tr).Error
	if err != nil {
		return nil, err
	}
	stats.FirstDetection = tr.MinTime
	stats.LastDetection = tr.MaxTime

	// Average confidence
	var avgConf struct {
		Avg float64
	}
	err = r.db.WithContext(ctx).Table(r.tableName()).
		Select("AVG(confidence) as avg").
		Where("model_id = ?", modelID).
		Scan(&avgConf).Error
	if err != nil {
		return nil, err
	}
	stats.AvgConfidence = avgConf.Avg

	return &stats, nil
}

// GetSpeciesStatsByModel returns species statistics for a model.
func (r *detectionRepository) GetSpeciesStatsByModel(ctx context.Context, labelID, modelID uint) (*SpeciesModelStats, error) {
	var stats SpeciesModelStats
	stats.LabelID = labelID
	stats.ModelID = modelID

	// Total detections
	if err := r.db.WithContext(ctx).Table(r.tableName()).
		Where("label_id = ? AND model_id = ?", labelID, modelID).
		Count(&stats.TotalDetections).Error; err != nil {
		return nil, err
	}

	// First and last detection
	type timeRange struct {
		MinTime int64
		MaxTime int64
	}
	var tr timeRange
	err := r.db.WithContext(ctx).Table(r.tableName()).
		Select("MIN(detected_at) as min_time, MAX(detected_at) as max_time").
		Where("label_id = ? AND model_id = ?", labelID, modelID).
		Scan(&tr).Error
	if err != nil {
		return nil, err
	}
	stats.FirstDetection = tr.MinTime
	stats.LastDetection = tr.MaxTime

	// Average confidence
	var avgConf struct {
		Avg float64
	}
	err = r.db.WithContext(ctx).Table(r.tableName()).
		Select("AVG(confidence) as avg").
		Where("label_id = ? AND model_id = ?", labelID, modelID).
		Scan(&avgConf).Error
	if err != nil {
		return nil, err
	}
	stats.AvgConfidence = avgConf.Avg

	return &stats, nil
}

// GetTopSpeciesByModel returns top species for a specific model.
func (r *detectionRepository) GetTopSpeciesByModel(ctx context.Context, modelID uint, limit int) ([]SpeciesCount, error) {
	var results []SpeciesCount

	err := r.db.WithContext(ctx).Table(r.tableName()).
		Select(fmt.Sprintf("%s.label_id, %s.scientific_name, COUNT(*) as count",
			r.tableName(), r.labelsTable())).
		Joins(fmt.Sprintf("JOIN %s ON %s.id = %s.label_id",
			r.labelsTable(), r.labelsTable(), r.tableName())).
		Where(fmt.Sprintf("%s.model_id = ?", r.tableName()), modelID).
		Group("label_id").
		Order("count DESC").
		Limit(limit).
		Scan(&results).Error

	return results, err
}

// ============================================================================
// Predictions
// ============================================================================

// SavePredictions stores additional predictions for a detection.
func (r *detectionRepository) SavePredictions(ctx context.Context, detectionID uint, preds []*entities.DetectionPrediction) error {
	if len(preds) == 0 {
		return nil
	}

	// Delete existing predictions first
	if err := r.DeletePredictions(ctx, detectionID); err != nil {
		return err
	}

	// Set detection ID on all predictions
	for _, p := range preds {
		p.DetectionID = detectionID
	}

	return r.db.WithContext(ctx).Table(r.predictionsTable()).Create(&preds).Error
}

// SavePredictionsBatch stores predictions for multiple detections efficiently.
func (r *detectionRepository) SavePredictionsBatch(ctx context.Context, preds []*entities.DetectionPrediction) error {
	if len(preds) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Table(r.predictionsTable()).CreateInBatches(preds, defaultDBBatchSize).Error
}

// GetPredictions retrieves all predictions for a detection.
func (r *detectionRepository) GetPredictions(ctx context.Context, detectionID uint) ([]*entities.DetectionPrediction, error) {
	var preds []*entities.DetectionPrediction
	err := r.db.WithContext(ctx).Table(r.predictionsTable()).
		Where("detection_id = ?", detectionID).
		Order("rank ASC").
		Find(&preds).Error
	return preds, err
}

// DeletePredictions removes all predictions for a detection.
func (r *detectionRepository) DeletePredictions(ctx context.Context, detectionID uint) error {
	return r.db.WithContext(ctx).Table(r.predictionsTable()).
		Where("detection_id = ?", detectionID).
		Delete(&entities.DetectionPrediction{}).Error
}

// ============================================================================
// Reviews
// ============================================================================

// SaveReview creates or updates a review for a detection.
func (r *detectionRepository) SaveReview(ctx context.Context, review *entities.DetectionReview) error {
	// Check if review exists
	var existing entities.DetectionReview
	err := r.db.WithContext(ctx).Table(r.reviewsTable()).
		Where("detection_id = ?", review.DetectionID).
		First(&existing).Error

	if errors.Is(err, gorm.ErrRecordNotFound) {
		// Create new review
		return r.db.WithContext(ctx).Table(r.reviewsTable()).Create(review).Error
	}
	if err != nil {
		return err
	}

	// Update existing review
	return r.db.WithContext(ctx).Table(r.reviewsTable()).
		Where("detection_id = ?", review.DetectionID).
		Updates(map[string]any{
			"verified":   review.Verified,
			"updated_at": time.Now(),
		}).Error
}

// GetReview retrieves the review for a detection.
func (r *detectionRepository) GetReview(ctx context.Context, detectionID uint) (*entities.DetectionReview, error) {
	var review entities.DetectionReview
	err := r.db.WithContext(ctx).Table(r.reviewsTable()).
		Where("detection_id = ?", detectionID).
		First(&review).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrReviewNotFound
	}
	return &review, err
}

// UpdateReview updates the verification status for a detection.
func (r *detectionRepository) UpdateReview(ctx context.Context, detectionID uint, verified entities.VerificationStatus) error {
	result := r.db.WithContext(ctx).Table(r.reviewsTable()).
		Where("detection_id = ?", detectionID).
		Updates(map[string]any{
			"verified":   verified,
			"updated_at": time.Now(),
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrReviewNotFound
	}
	return nil
}

// DeleteReview removes the review for a detection.
func (r *detectionRepository) DeleteReview(ctx context.Context, detectionID uint) error {
	result := r.db.WithContext(ctx).Table(r.reviewsTable()).
		Where("detection_id = ?", detectionID).
		Delete(&entities.DetectionReview{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrReviewNotFound
	}
	return nil
}

// ============================================================================
// Comments
// ============================================================================

// SaveComment adds a comment to a detection.
func (r *detectionRepository) SaveComment(ctx context.Context, comment *entities.DetectionComment) error {
	return r.db.WithContext(ctx).Table(r.commentsTable()).Create(comment).Error
}

// GetComments retrieves all comments for a detection.
func (r *detectionRepository) GetComments(ctx context.Context, detectionID uint) ([]*entities.DetectionComment, error) {
	var comments []*entities.DetectionComment
	err := r.db.WithContext(ctx).Table(r.commentsTable()).
		Where("detection_id = ?", detectionID).
		Order("created_at ASC").
		Find(&comments).Error
	return comments, err
}

// UpdateComment modifies a comment's content.
func (r *detectionRepository) UpdateComment(ctx context.Context, commentID uint, entry string) error {
	result := r.db.WithContext(ctx).Table(r.commentsTable()).
		Where("id = ?", commentID).
		Updates(map[string]any{
			"entry":      entry,
			"updated_at": time.Now(),
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrCommentNotFound
	}
	return nil
}

// DeleteComment removes a specific comment.
func (r *detectionRepository) DeleteComment(ctx context.Context, commentID uint) error {
	result := r.db.WithContext(ctx).Table(r.commentsTable()).Delete(&entities.DetectionComment{}, commentID)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrCommentNotFound
	}
	return nil
}

// ============================================================================
// Locks
// ============================================================================

// Lock prevents modification/deletion of a detection.
func (r *detectionRepository) Lock(ctx context.Context, detectionID uint) error {
	// Check if detection exists
	exists, err := r.Exists(ctx, detectionID)
	if err != nil {
		return err
	}
	if !exists {
		return ErrDetectionNotFound
	}

	// Check if already locked
	locked, _ := r.IsLocked(ctx, detectionID)
	if locked {
		return nil // Already locked, idempotent
	}

	lock := entities.DetectionLock{DetectionID: detectionID}
	return r.db.WithContext(ctx).Table(r.locksTable()).Create(&lock).Error
}

// Unlock removes the lock from a detection.
func (r *detectionRepository) Unlock(ctx context.Context, detectionID uint) error {
	result := r.db.WithContext(ctx).Table(r.locksTable()).
		Where("detection_id = ?", detectionID).
		Delete(&entities.DetectionLock{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrLockNotFound
	}
	return nil
}

// IsLocked checks if a detection is locked.
func (r *detectionRepository) IsLocked(ctx context.Context, detectionID uint) (bool, error) {
	var count int64
	err := r.db.WithContext(ctx).Table(r.locksTable()).
		Where("detection_id = ?", detectionID).
		Count(&count).Error
	return count > 0, err
}

// GetLockedClipPaths returns clip paths for all locked detections.
func (r *detectionRepository) GetLockedClipPaths(ctx context.Context) ([]string, error) {
	var paths []string
	err := r.db.WithContext(ctx).Table(r.tableName()).
		Select(fmt.Sprintf("%s.clip_name", r.tableName())).
		Joins(fmt.Sprintf("JOIN %s ON %s.detection_id = %s.id",
			r.locksTable(), r.locksTable(), r.tableName())).
		Where(fmt.Sprintf("%s.clip_name IS NOT NULL", r.tableName())).
		Pluck("clip_name", &paths).Error
	return paths, err
}

// ============================================================================
// Analytics
// ============================================================================

// GetSpeciesSummary returns summary statistics for all species.
func (r *detectionRepository) GetSpeciesSummary(ctx context.Context, start, end int64, modelID *uint) ([]SpeciesSummaryData, error) {
	var results []SpeciesSummaryData

	query := r.db.WithContext(ctx).Table(r.tableName()).
		Select(fmt.Sprintf(`
			%s.label_id,
			%s.scientific_name,
			COUNT(*) as total_detections,
			MIN(%s.detected_at) as first_detection,
			MAX(%s.detected_at) as last_detection,
			AVG(%s.confidence) as avg_confidence,
			MAX(%s.confidence) as max_confidence
		`, r.tableName(), r.labelsTable(), r.tableName(), r.tableName(), r.tableName(), r.tableName())).
		Joins(fmt.Sprintf("JOIN %s ON %s.id = %s.label_id",
			r.labelsTable(), r.labelsTable(), r.tableName())).
		Where(fmt.Sprintf("%s.detected_at >= ? AND %s.detected_at <= ?", r.tableName(), r.tableName()), start, end)

	if modelID != nil {
		query = query.Where(fmt.Sprintf("%s.model_id = ?", r.tableName()), *modelID)
	}

	err := query.Group("label_id").
		Order("total_detections DESC").
		Scan(&results).Error

	return results, err
}

// GetHourlyDistribution returns detection counts by hour.
func (r *detectionRepository) GetHourlyDistribution(ctx context.Context, start, end int64, labelID, modelID *uint) ([]HourlyDistributionData, error) {
	var results []HourlyDistributionData

	query := r.db.WithContext(ctx).Table(r.tableName()).
		Select("(detected_at / 3600) % 24 as hour, COUNT(*) as count").
		Where("detected_at >= ? AND detected_at <= ?", start, end)

	if labelID != nil {
		query = query.Where("label_id = ?", *labelID)
	}
	if modelID != nil {
		query = query.Where("model_id = ?", *modelID)
	}

	err := query.Group("hour").
		Order("hour ASC").
		Scan(&results).Error

	return results, err
}

// GetDailyAnalytics returns daily statistics.
func (r *detectionRepository) GetDailyAnalytics(ctx context.Context, start, end int64, labelID, modelID *uint) ([]DailyAnalyticsData, error) {
	var results []DailyAnalyticsData

	// Use dialect-appropriate date conversion
	dateExpr := r.dateFromUnixExpr("detected_at")
	query := r.db.WithContext(ctx).Table(r.tableName()).
		Select(fmt.Sprintf(`
			%s as date,
			COUNT(*) as total_detections,
			COUNT(DISTINCT label_id) as unique_species,
			AVG(confidence) as avg_confidence
		`, dateExpr)).
		Where("detected_at >= ? AND detected_at <= ?", start, end)

	if labelID != nil {
		query = query.Where("label_id = ?", *labelID)
	}
	if modelID != nil {
		query = query.Where("model_id = ?", *modelID)
	}

	err := query.Group("date").
		Order("date DESC").
		Scan(&results).Error

	return results, err
}

// GetDetectionTrends returns detection trends over time.
func (r *detectionRepository) GetDetectionTrends(ctx context.Context, period string, limit int, modelID *uint) ([]DailyAnalyticsData, error) {
	// Calculate start time based on period
	var startTime int64
	now := time.Now().Unix()

	switch period {
	case "week":
		startTime = now - (7 * 24 * 3600)
	case "month":
		startTime = now - (30 * 24 * 3600)
	default: // day
		startTime = now - (24 * 3600)
	}

	return r.GetDailyAnalytics(ctx, startTime, now, nil, modelID)
}

// GetNewSpecies returns species detected for the first time ever within the range.
// Uses MIN(id) as tie-breaker to avoid duplicates when multiple detections share the same timestamp.
func (r *detectionRepository) GetNewSpecies(ctx context.Context, start, end int64, limit, offset int) ([]NewSpeciesData, error) {
	var results []NewSpeciesData

	// Find species where first detection is within the range
	// Include MIN(id) to get a unique detection per label when timestamps tie
	subquery := r.db.WithContext(ctx).Table(r.tableName()).
		Select("label_id, MIN(detected_at) as first_detected").
		Group("label_id")

	// Inner subquery to get the specific detection_id using the tie-breaker
	detSubquery := r.db.WithContext(ctx).Table(r.tableName()).
		Select("label_id, MIN(id) as first_detection_id, detected_at").
		Group("label_id, detected_at")

	err := r.db.WithContext(ctx).Table("(?) as firsts", subquery).
		Select(fmt.Sprintf(`
			DISTINCT firsts.label_id,
			%s.scientific_name,
			firsts.first_detected,
			det_ids.first_detection_id as detection_id,
			%s.confidence
		`, r.labelsTable(), r.tableName())).
		Joins(fmt.Sprintf("JOIN %s ON %s.id = firsts.label_id",
			r.labelsTable(), r.labelsTable())).
		Joins("JOIN (?) as det_ids ON det_ids.label_id = firsts.label_id AND det_ids.detected_at = firsts.first_detected", detSubquery).
		Joins(fmt.Sprintf("JOIN %s ON %s.id = det_ids.first_detection_id",
			r.tableName(), r.tableName())).
		Where("firsts.first_detected >= ? AND firsts.first_detected <= ?", start, end).
		Order("firsts.first_detected DESC").
		Limit(limit).
		Offset(offset).
		Scan(&results).Error

	return results, err
}

// GetSpeciesFirstDetectionInPeriod returns the first detection of each species within a date range.
// Uses ROW_NUMBER() window function to correctly identify the detection with the earliest timestamp
// per label, with id as tie-breaker for deterministic results.
func (r *detectionRepository) GetSpeciesFirstDetectionInPeriod(ctx context.Context, start, end int64, limit, offset int) ([]SpeciesFirstSeen, error) {
	var results []SpeciesFirstSeen

	// Use window function to rank detections per label by timestamp (with id as tie-breaker)
	// This ensures we get the actual detection_id that corresponds to the first_detected time
	rawSQL := fmt.Sprintf(`
		SELECT label_id, scientific_name, first_detected, detection_id
		FROM (
			SELECT
				d.label_id,
				l.scientific_name,
				d.detected_at as first_detected,
				d.id as detection_id,
				ROW_NUMBER() OVER (PARTITION BY d.label_id ORDER BY d.detected_at ASC, d.id ASC) as rn
			FROM %s d
			JOIN %s l ON l.id = d.label_id
			WHERE d.detected_at >= ? AND d.detected_at <= ?
		) ranked
		WHERE rn = 1
		ORDER BY first_detected ASC
		LIMIT ? OFFSET ?
	`, r.tableName(), r.labelsTable())

	err := r.db.WithContext(ctx).Raw(rawSQL, start, end, limit, offset).Scan(&results).Error
	return results, err
}

// ============================================================================
// Utilities
// ============================================================================

// GetClipPath returns the clip path for a detection.
// Returns ErrDetectionNotFound if the detection doesn't exist.
// Returns ErrNoClipPath if the detection exists but has no clip.
func (r *detectionRepository) GetClipPath(ctx context.Context, id uint) (string, error) {
	// Use a struct to properly handle the query result
	var result struct {
		ClipName *string `gorm:"column:clip_name"`
	}
	err := r.db.WithContext(ctx).Table(r.tableName()).
		Select("clip_name").
		Where("id = ?", id).
		Take(&result).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", ErrDetectionNotFound
		}
		return "", err
	}
	if result.ClipName == nil {
		return "", ErrNoClipPath
	}
	return *result.ClipName, nil
}

// Exists checks if a detection with the given ID exists.
func (r *detectionRepository) Exists(ctx context.Context, id uint) (bool, error) {
	var count int64
	err := r.db.WithContext(ctx).Table(r.tableName()).
		Where("id = ?", id).
		Count(&count).Error
	return count > 0, err
}

// GetLastMigratedID returns the highest legacy_id that has been migrated.
//
// ASSUMPTION: This method assumes legacy IDs are monotonically increasing and
// that migration proceeds in ID order. It returns MAX(legacy_id) which serves
// as a cursor for the migration worker to determine where to resume.
//
// NOTE: This approach does not handle gaps in ID sequences that are filled later.
// If legacy IDs can be inserted out of order (e.g., ID 100 inserted after ID 200),
// those records will not be migrated unless a full re-scan is performed.
func (r *detectionRepository) GetLastMigratedID(ctx context.Context) (uint, error) {
	var maxID *uint
	err := r.db.WithContext(ctx).Table(r.tableName()).
		Select("MAX(legacy_id)").
		Pluck("MAX(legacy_id)", &maxID).Error
	if err != nil {
		return 0, err
	}
	if maxID == nil {
		return 0, nil
	}
	return *maxID, nil
}
