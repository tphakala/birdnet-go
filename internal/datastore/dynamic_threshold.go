// dynamic_threshold.go: Database operations for persisting dynamic thresholds
package datastore

import (
	"fmt"
	"time"

	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// SaveDynamicThreshold saves or updates a dynamic threshold in the database
// This uses an upsert operation to either create a new threshold or update an existing one
func (ds *DataStore) SaveDynamicThreshold(threshold *DynamicThreshold) error {
	if threshold == nil {
		return validationError("threshold cannot be nil", "threshold", nil)
	}
	if threshold.SpeciesName == "" {
		return validationError("species name cannot be empty", "species_name", "")
	}

	// Timestamps
	now := time.Now()
	threshold.UpdatedAt = now

	// Upsert: set FirstCreated only on INSERT; always update other fields
	result := ds.DB.Where("species_name = ?", threshold.SpeciesName).
		Attrs(DynamicThreshold{
			FirstCreated: now, // Only set on INSERT
		}).
		Assign(*threshold).
		FirstOrCreate(threshold)

	if result.Error != nil {
		return dbError(result.Error, "save_dynamic_threshold", errors.PriorityMedium,
			"species", threshold.SpeciesName,
			"table", "dynamic_thresholds",
			"action", "persist_learned_threshold")
	}

	return nil
}

// GetDynamicThreshold retrieves a dynamic threshold for a specific species
func (ds *DataStore) GetDynamicThreshold(speciesName string) (*DynamicThreshold, error) {
	if speciesName == "" {
		return nil, validationError("species name cannot be empty", "species_name", "")
	}

	var threshold DynamicThreshold
	err := ds.DB.Where("species_name = ?", speciesName).First(&threshold).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.Newf("dynamic threshold not found").
				Component("datastore").
				Category(errors.CategoryNotFound).
				Context("operation", "get_dynamic_threshold").
				Context("species", speciesName).
				Build()
		}
		return nil, dbError(err, "get_dynamic_threshold", errors.PriorityMedium,
			"species", speciesName,
			"action", "retrieve_learned_threshold")
	}

	return &threshold, nil
}

// GetAllDynamicThresholds retrieves all dynamic thresholds from the database
// This is typically called at application startup to restore learned thresholds
//
// Memory usage: Expected max ~300 species (BirdNET dataset size), resulting in ~65KB of memory
// for the returned slice. The optional limit parameter is provided for API flexibility but is
// not necessary for typical usage given the small dataset size. Use limit=0 or omit for no limit.
func (ds *DataStore) GetAllDynamicThresholds(limit ...int) ([]DynamicThreshold, error) {
	var thresholds []DynamicThreshold
	query := ds.DB.Order("species_name ASC")

	// Apply limit if provided and greater than 0
	if len(limit) > 0 && limit[0] > 0 {
		query = query.Limit(limit[0])
	}

	err := query.Find(&thresholds).Error
	if err != nil {
		return nil, dbError(err, "get_all_dynamic_thresholds", errors.PriorityMedium,
			"table", "dynamic_thresholds",
			"action", "restore_learned_thresholds")
	}

	return thresholds, nil
}

// DeleteDynamicThreshold removes a dynamic threshold for a specific species
func (ds *DataStore) DeleteDynamicThreshold(speciesName string) error {
	if speciesName == "" {
		return validationError("species name cannot be empty", "species_name", "")
	}

	result := ds.DB.Where("species_name = ?", speciesName).Delete(&DynamicThreshold{})
	if result.Error != nil {
		return dbError(result.Error, "delete_dynamic_threshold", errors.PriorityMedium,
			"species", speciesName,
			"action", "remove_learned_threshold")
	}

	return nil
}

// DeleteExpiredDynamicThresholds removes all dynamic thresholds that have expired
// Returns the count of deleted thresholds
// This is typically called periodically by a cleanup job
func (ds *DataStore) DeleteExpiredDynamicThresholds(before time.Time) (int64, error) {
	result := ds.DB.Where("expires_at < ?", before).Delete(&DynamicThreshold{})
	if result.Error != nil {
		return 0, dbError(result.Error, "delete_expired_dynamic_thresholds", errors.PriorityLow,
			"before", before.Format(time.RFC3339),
			"action", "cleanup_expired_thresholds")
	}

	if result.RowsAffected > 0 {
		log.Info("Cleaned up expired dynamic thresholds",
			logger.Int64("count", result.RowsAffected),
			logger.String("before", before.Format(time.RFC3339)))
	}

	return result.RowsAffected, nil
}

// UpdateDynamicThresholdExpiry updates the expiry time for a specific species threshold
// This is useful when extending the validity of a threshold due to new detections
func (ds *DataStore) UpdateDynamicThresholdExpiry(speciesName string, expiresAt time.Time) error {
	if speciesName == "" {
		return validationError("species name cannot be empty", "species_name", "")
	}

	result := ds.DB.Model(&DynamicThreshold{}).
		Where("species_name = ?", speciesName).
		Updates(map[string]any{
			"expires_at": expiresAt,
			"updated_at": time.Now(),
		})

	if result.Error != nil {
		return dbError(result.Error, "update_dynamic_threshold_expiry", errors.PriorityMedium,
			"species", speciesName,
			"expires_at", expiresAt.Format(time.RFC3339),
			"action", "extend_threshold_validity")
	}

	if result.RowsAffected == 0 {
		return errors.Newf("dynamic threshold not found").
			Component("datastore").
			Category(errors.CategoryNotFound).
			Context("operation", "update_dynamic_threshold_expiry").
			Context("species", speciesName).
			Build()
	}

	return nil
}

// GetDynamicThresholdStats returns statistics about dynamic thresholds
// Returns totalCount, activeCount, atMinimumCount (level 3), and level distribution
func (ds *DataStore) GetDynamicThresholdStats() (totalCount, activeCount, atMinimumCount int64, levelDistribution map[int]int64, err error) {
	now := time.Now()

	// Total count
	if err = ds.DB.Model(&DynamicThreshold{}).Count(&totalCount).Error; err != nil {
		err = dbError(err, "get_dynamic_threshold_stats_count", errors.PriorityLow,
			"stat", "total_count",
			"action", "retrieve_threshold_statistics")
		return
	}

	// Active count (not expired)
	if err = ds.DB.Model(&DynamicThreshold{}).Where("expires_at >= ?", now).Count(&activeCount).Error; err != nil {
		err = dbError(err, "get_dynamic_threshold_stats_active", errors.PriorityLow,
			"stat", "active_count",
			"action", "retrieve_threshold_statistics")
		return
	}

	// At minimum count (level 3, which is the minimum threshold)
	if err = ds.DB.Model(&DynamicThreshold{}).Where("expires_at >= ? AND level = ?", now, 3).Count(&atMinimumCount).Error; err != nil {
		err = dbError(err, "get_dynamic_threshold_stats_at_minimum", errors.PriorityLow,
			"stat", "at_minimum_count",
			"action", "retrieve_threshold_statistics")
		return
	}

	// Level distribution
	type LevelCount struct {
		Level int
		Count int64
	}
	var levelCounts []LevelCount
	if err = ds.DB.Model(&DynamicThreshold{}).
		Select("level, COUNT(*) as count").
		Where("expires_at >= ?", now).
		Group("level").
		Order("level ASC").
		Find(&levelCounts).Error; err != nil {
		err = dbError(err, "get_dynamic_threshold_stats_levels", errors.PriorityLow,
			"stat", "level_distribution",
			"action", "retrieve_threshold_statistics")
		return
	}

	levelDistribution = make(map[int]int64)
	for _, lc := range levelCounts {
		levelDistribution[lc.Level] = lc.Count
	}

	return totalCount, activeCount, atMinimumCount, levelDistribution, nil
}

// DeleteAllDynamicThresholds removes all dynamic thresholds from the database
// Returns the count of deleted thresholds
// BG-59: Used for "Reset All" functionality
func (ds *DataStore) DeleteAllDynamicThresholds() (int64, error) {
	result := ds.DB.Where("1 = 1").Delete(&DynamicThreshold{})
	if result.Error != nil {
		return 0, dbError(result.Error, "delete_all_dynamic_thresholds", errors.PriorityMedium,
			"action", "reset_all_learned_thresholds")
	}

	if result.RowsAffected > 0 {
		log.Info("Reset all dynamic thresholds",
			logger.Int64("count", result.RowsAffected))
	}

	return result.RowsAffected, nil
}

// SaveThresholdEvent saves a threshold change event to the database
// BG-59: Records threshold changes for history/audit purposes
func (ds *DataStore) SaveThresholdEvent(event *ThresholdEvent) error {
	if event == nil {
		return validationError("event cannot be nil", "event", nil)
	}
	if event.SpeciesName == "" {
		return validationError("species name cannot be empty", "species_name", "")
	}

	// Set timestamp if not already set
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now()
	}

	if err := ds.DB.Create(event).Error; err != nil {
		return dbError(err, "save_threshold_event", errors.PriorityMedium,
			"species", event.SpeciesName,
			"table", "threshold_events",
			"action", "record_threshold_change")
	}

	return nil
}

// GetThresholdEvents retrieves threshold events for a specific species
// Returns events ordered by created_at DESC (most recent first)
// BG-59: Used for displaying threshold change history per species
func (ds *DataStore) GetThresholdEvents(speciesName string, limit int) ([]ThresholdEvent, error) {
	if speciesName == "" {
		return nil, validationError("species name cannot be empty", "species_name", "")
	}

	var events []ThresholdEvent
	query := ds.DB.Where("species_name = ?", speciesName).Order("created_at DESC")

	if limit > 0 {
		query = query.Limit(limit)
	}

	if err := query.Find(&events).Error; err != nil {
		return nil, dbError(err, "get_threshold_events", errors.PriorityMedium,
			"species", speciesName,
			"action", "retrieve_threshold_history")
	}

	return events, nil
}

// GetRecentThresholdEvents retrieves recent threshold events across all species
// Returns events ordered by created_at DESC (most recent first)
// BG-59: Used for displaying recent activity
func (ds *DataStore) GetRecentThresholdEvents(limit int) ([]ThresholdEvent, error) {
	var events []ThresholdEvent
	query := ds.DB.Order("created_at DESC")

	if limit > 0 {
		query = query.Limit(limit)
	}

	if err := query.Find(&events).Error; err != nil {
		return nil, dbError(err, "get_recent_threshold_events", errors.PriorityMedium,
			"action", "retrieve_recent_threshold_events")
	}

	return events, nil
}

// DeleteThresholdEvents removes all threshold events for a specific species
// BG-59: Used when resetting a single species threshold
func (ds *DataStore) DeleteThresholdEvents(speciesName string) error {
	if speciesName == "" {
		return validationError("species name cannot be empty", "species_name", "")
	}

	result := ds.DB.Where("species_name = ?", speciesName).Delete(&ThresholdEvent{})
	if result.Error != nil {
		return dbError(result.Error, "delete_threshold_events", errors.PriorityMedium,
			"species", speciesName,
			"action", "remove_threshold_history")
	}

	return nil
}

// DeleteAllThresholdEvents removes all threshold events from the database
// Returns the count of deleted events
// BG-59: Used for "Reset All" functionality
func (ds *DataStore) DeleteAllThresholdEvents() (int64, error) {
	result := ds.DB.Where("1 = 1").Delete(&ThresholdEvent{})
	if result.Error != nil {
		return 0, dbError(result.Error, "delete_all_threshold_events", errors.PriorityMedium,
			"action", "reset_all_threshold_history")
	}

	if result.RowsAffected > 0 {
		log.Info("Deleted all threshold events",
			logger.Int64("count", result.RowsAffected))
	}

	return result.RowsAffected, nil
}

// BatchSaveDynamicThresholds saves multiple dynamic thresholds in a single transaction
// This is more efficient than saving them one by one
// Optimized to use single INSERT...ON CONFLICT statement to minimize lock contention
func (ds *DataStore) BatchSaveDynamicThresholds(thresholds []DynamicThreshold) error {
	if len(thresholds) == 0 {
		return nil // Nothing to save
	}

	// Validate all entries before starting transaction
	for i := range thresholds {
		if thresholds[i].SpeciesName == "" {
			return validationError("species name cannot be empty", "index", i)
		}
	}

	return ds.DB.Transaction(func(tx *gorm.DB) error {
		now := time.Now()

		// Prepare batch data - set FirstCreated for all entries
		// (will only be used on INSERT, not UPDATE due to ON CONFLICT clause)
		for i := range thresholds {
			thresholds[i].FirstCreated = now
			thresholds[i].UpdatedAt = now
		}

		// Use single INSERT...ON CONFLICT to minimize lock time
		// This is much more efficient than multiple FirstOrCreate calls
		// GORM's OnConflict clause handles SQLite's INSERT...ON CONFLICT DO UPDATE
		result := tx.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "species_name"}},
			DoUpdates: clause.AssignmentColumns([]string{
				"level",
				"current_value",
				"base_threshold",
				"high_conf_count",
				"valid_hours",
				"expires_at",
				"last_triggered",
				"updated_at",
				"trigger_count",
			}),
		}).Create(&thresholds)

		if result.Error != nil {
			return dbError(result.Error, "batch_save_dynamic_thresholds", errors.PriorityMedium,
				"threshold_count", fmt.Sprintf("%d", len(thresholds)),
				"action", "batch_persist_thresholds")
		}

		return nil
	})
}
