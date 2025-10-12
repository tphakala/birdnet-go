// dynamic_threshold.go: Database operations for persisting dynamic thresholds
package datastore

import (
	"fmt"
	"time"

	"github.com/tphakala/birdnet-go/internal/errors"
	"gorm.io/gorm"
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

	// Set UpdatedAt timestamp
	threshold.UpdatedAt = time.Now()

	// Use upsert operation to either create or update the threshold
	result := ds.DB.Where("species_name = ?", threshold.SpeciesName).
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
func (ds *DataStore) GetAllDynamicThresholds() ([]DynamicThreshold, error) {
	var thresholds []DynamicThreshold
	err := ds.DB.Order("species_name ASC").Find(&thresholds).Error
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
		getLogger().Info("Cleaned up expired dynamic thresholds",
			"count", result.RowsAffected,
			"before", before.Format(time.RFC3339))
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
		Updates(map[string]interface{}{
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
// This can be useful for monitoring and debugging
func (ds *DataStore) GetDynamicThresholdStats() (map[string]interface{}, error) {
	stats := make(map[string]interface{})

	// Total count
	var totalCount int64
	if err := ds.DB.Model(&DynamicThreshold{}).Count(&totalCount).Error; err != nil {
		return nil, dbError(err, "get_dynamic_threshold_stats_count", errors.PriorityLow,
			"stat", "total_count",
			"action", "retrieve_threshold_statistics")
	}
	stats["total_count"] = totalCount

	// Expired count
	var expiredCount int64
	if err := ds.DB.Model(&DynamicThreshold{}).Where("expires_at < ?", time.Now()).Count(&expiredCount).Error; err != nil {
		return nil, dbError(err, "get_dynamic_threshold_stats_expired", errors.PriorityLow,
			"stat", "expired_count",
			"action", "retrieve_threshold_statistics")
	}
	stats["expired_count"] = expiredCount

	// Active count
	stats["active_count"] = totalCount - expiredCount

	// Level distribution
	type LevelCount struct {
		Level int
		Count int64
	}
	var levelCounts []LevelCount
	if err := ds.DB.Model(&DynamicThreshold{}).
		Select("level, COUNT(*) as count").
		Where("expires_at >= ?", time.Now()).
		Group("level").
		Order("level ASC").
		Find(&levelCounts).Error; err != nil {
		return nil, dbError(err, "get_dynamic_threshold_stats_levels", errors.PriorityLow,
			"stat", "level_distribution",
			"action", "retrieve_threshold_statistics")
	}
	stats["level_distribution"] = levelCounts

	return stats, nil
}

// BatchSaveDynamicThresholds saves multiple dynamic thresholds in a single transaction
// This is more efficient than saving them one by one
func (ds *DataStore) BatchSaveDynamicThresholds(thresholds []DynamicThreshold) error {
	if len(thresholds) == 0 {
		return nil // Nothing to save
	}

	return ds.DB.Transaction(func(tx *gorm.DB) error {
		for i := range thresholds {
			// Set UpdatedAt timestamp
			thresholds[i].UpdatedAt = time.Now()

			// Use upsert operation
			result := tx.Where("species_name = ?", thresholds[i].SpeciesName).
				Assign(thresholds[i]).
				FirstOrCreate(&thresholds[i])

			if result.Error != nil {
				return dbError(result.Error, "batch_save_dynamic_threshold", errors.PriorityMedium,
					"species", thresholds[i].SpeciesName,
					"batch_index", fmt.Sprintf("%d", i),
					"action", "batch_persist_thresholds")
			}
		}
		return nil
	})
}
