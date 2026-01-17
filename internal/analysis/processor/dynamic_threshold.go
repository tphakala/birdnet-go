package processor

import (
	"strings"
	"time"

	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// Threshold level multipliers define how much the base threshold is reduced at each level.
// Level 1: 25% reduction, Level 2: 50% reduction, Level 3: 75% reduction (maximum)
const (
	thresholdLevel1Multiplier = 0.75 // First high-confidence detection: 75% of base
	thresholdLevel2Multiplier = 0.50 // Second high-confidence detection: 50% of base
	thresholdLevel3Multiplier = 0.25 // Third+ high-confidence detection: 25% of base (minimum)
)

// Threshold change reason constants for event recording
const (
	changeReasonHighConfidence = "high_confidence" // Threshold lowered due to high-confidence detection
	changeReasonExpiry         = "expiry"          // Threshold reset due to timer expiration
)

// DynamicThreshold represents the dynamic threshold configuration for a species.
type DynamicThreshold struct {
	Level          int
	CurrentValue   float64
	Timer          time.Time
	HighConfCount  int
	ValidHours     int
	ScientificName string
	LastLearnedAt  time.Time // Tracks when the last threshold learning event occurred to prevent multiple learnings within a single detection window
}

// addSpeciesToDynamicThresholds adds a species to the dynamic thresholds map if it doesn't already exist.
func (p *Processor) addSpeciesToDynamicThresholds(speciesLowercase, scientificName string, baseThreshold float32) {
	// Lock the mutex to ensure thread-safe access to the DynamicThresholds map
	p.thresholdsMutex.Lock()
	defer p.thresholdsMutex.Unlock()

	// Check if the species already has a dynamic threshold
	existing, exists := p.DynamicThresholds[speciesLowercase]

	// If it doesn't exist, initialize it
	if !exists {
		if p.Settings.Realtime.DynamicThreshold.Debug {
			log := GetLogger()
			log.Debug("Initializing dynamic threshold", logger.String("species", speciesLowercase))
		}
		p.DynamicThresholds[speciesLowercase] = &DynamicThreshold{
			Level:          0,
			CurrentValue:   float64(baseThreshold),
			Timer:          time.Now(),
			HighConfCount:  0,
			ValidHours:     p.Settings.Realtime.DynamicThreshold.ValidHours,
			ScientificName: scientificName,
		}
	} else if existing.ScientificName == "" && scientificName != "" {
		// Update scientific name if it was missing
		existing.ScientificName = scientificName
	}
}

// getAdjustedConfidenceThreshold applies dynamic threshold logic to adjust the confidence threshold based on recent detections.
// If isCustomThreshold is true (species has a user-configured threshold), the function returns it unchanged,
// ensuring user intent is never overridden by automatic dynamic adjustments.
func (p *Processor) getAdjustedConfidenceThreshold(speciesLowercase string, result datastore.Results, baseThreshold float32, isCustomThreshold bool) float32 {
	// If this is a custom user-configured threshold, respect it and don't apply dynamic adjustments.
	// Dynamic threshold is meant to learn from detections for species using the global threshold,
	// not to override explicit user configuration.
	if isCustomThreshold {
		return baseThreshold
	}

	// Lock the mutex to ensure thread-safe access to the DynamicThresholds map
	p.thresholdsMutex.Lock()
	defer p.thresholdsMutex.Unlock()

	// Get the dynamic threshold for the species
	dt, exists := p.DynamicThresholds[speciesLowercase]

	// If it doesn't exist, return the base threshold
	if !exists {
		return baseThreshold
	}

	// Track previous state for event recording
	previousLevel := dt.Level
	previousValue := dt.CurrentValue

	// Calculate the learning cooldown based on detection window duration
	// This prevents multiple threshold learnings within a single detection event
	captureLength := time.Duration(p.Settings.Realtime.Audio.Export.Length) * time.Second
	preCaptureLength := time.Duration(p.Settings.Realtime.Audio.Export.PreCapture) * time.Second
	learningCooldown := captureLength - preCaptureLength
	// Enforce minimum 5 seconds to prevent issues with misconfigured short windows
	const minCooldown = 5 * time.Second
	if learningCooldown < minCooldown {
		learningCooldown = minCooldown
	}

	now := time.Now()

	// If the detection confidence exceeds the trigger threshold
	if result.Confidence > float32(p.Settings.Realtime.DynamicThreshold.Trigger) {
		// Always extend the timer to keep threshold active while species is being detected
		dt.Timer = now.Add(time.Duration(dt.ValidHours) * time.Hour)

		// Only learn from this detection if enough time has passed since last learning
		// This ensures learnings happen across different detection events, not within a single window
		if dt.LastLearnedAt.IsZero() || now.Sub(dt.LastLearnedAt) >= learningCooldown {
			dt.HighConfCount++
			dt.LastLearnedAt = now

			// Adjust the dynamic threshold based on the number of high-confidence detections
			switch dt.HighConfCount {
			case 1:
				dt.Level = 1
				dt.CurrentValue = float64(baseThreshold * thresholdLevel1Multiplier)
			case 2:
				dt.Level = 2
				dt.CurrentValue = float64(baseThreshold * thresholdLevel2Multiplier)
			default:
				// Level 3 is the maximum reduction; any count >= 3 stays at this level
				dt.Level = 3
				dt.CurrentValue = float64(baseThreshold * thresholdLevel3Multiplier)
			}

			// Apply minimum threshold clamp BEFORE recording event so event shows actual value
			if dt.CurrentValue < p.Settings.Realtime.DynamicThreshold.Min {
				dt.CurrentValue = p.Settings.Realtime.DynamicThreshold.Min
			}

			// Record event if level changed (BG-59)
			if dt.Level != previousLevel {
				p.recordThresholdEvent(speciesLowercase, previousLevel, dt.Level, previousValue, dt.CurrentValue, changeReasonHighConfidence, float64(result.Confidence))
			}
		}
	} else if now.After(dt.Timer) {
		// Reset the dynamic threshold if the timer has expired
		dt.Level = 0
		dt.CurrentValue = float64(baseThreshold)
		dt.HighConfCount = 0
		dt.LastLearnedAt = time.Time{} // Reset so next high-confidence detection triggers learning immediately

		// Record expiry event if level was not already 0 (BG-59)
		if previousLevel != 0 {
			p.recordThresholdEvent(speciesLowercase, previousLevel, 0, previousValue, float64(baseThreshold), changeReasonExpiry, 0)
		}
	}

	// Final minimum threshold enforcement - this is intentionally separate from the clamp inside
	// the learning block (lines 113-116). That clamp ensures event recording shows the actual
	// clamped value. This clamp handles edge cases: expiry resets, and any code paths that
	// might bypass the learning block but still need minimum enforcement.
	if dt.CurrentValue < p.Settings.Realtime.DynamicThreshold.Min {
		dt.CurrentValue = p.Settings.Realtime.DynamicThreshold.Min
	}

	return float32(dt.CurrentValue)
}

// recordThresholdEvent saves a threshold change event to the database (BG-59)
func (p *Processor) recordThresholdEvent(speciesName string, previousLevel, newLevel int, previousValue, newValue float64, changeReason string, confidence float64) {
	if p.Ds == nil {
		return
	}

	event := &datastore.ThresholdEvent{
		SpeciesName:   speciesName,
		PreviousLevel: previousLevel,
		NewLevel:      newLevel,
		PreviousValue: previousValue,
		NewValue:      newValue,
		ChangeReason:  changeReason,
		Confidence:    confidence,
		CreatedAt:     time.Now(),
	}

	// Save asynchronously to avoid blocking the detection pipeline
	go func() {
		if err := p.Ds.SaveThresholdEvent(event); err != nil {
			log := GetLogger()
			log.Error("Failed to save threshold event", logger.String("species", speciesName), logger.Error(err))
		}
	}()
}

// updateDynamicThreshold updates the dynamic threshold for a given species if enabled.
func (p *Processor) updateDynamicThreshold(commonName string, confidence float64) {
	if p.Settings.Realtime.DynamicThreshold.Enabled {
		// Lock the mutex to ensure thread-safe access to the DynamicThresholds map
		p.thresholdsMutex.Lock()
		defer p.thresholdsMutex.Unlock()

		// Check if the species already has a dynamic threshold
		// Note: scientific name not available in this context, but common name lookup is sufficient
		if dt, exists := p.DynamicThresholds[commonName]; exists && confidence > float64(p.getBaseConfidenceThreshold(commonName, "")) {
			// Update the timer to extend the threshold's validity
			// Note: dt is a pointer, so this directly mutates the struct in the map
			dt.Timer = time.Now().Add(time.Duration(dt.ValidHours) * time.Hour)
		}
	}
}

// cleanUpDynamicThresholds removes stale dynamic thresholds for species that haven't been detected for a long time.
// This cleans up both the in-memory map and the database.
func (p *Processor) cleanUpDynamicThresholds() {
	log := GetLogger()
	// Calculate the duration after which a dynamic threshold is considered stale
	staleDuration := time.Duration(p.Settings.Realtime.DynamicThreshold.ValidHours) * time.Hour

	// Get the current time
	now := time.Now()

	// Lock the mutex to ensure thread-safe access to the DynamicThresholds map
	p.thresholdsMutex.Lock()

	// Count for logging
	var removedCount int

	// Iterate through all species in the DynamicThresholds map
	for species, dt := range p.DynamicThresholds {
		// Check if the threshold for this species is stale
		if now.Sub(dt.Timer) > staleDuration {
			// If debug mode is enabled, log the removal of the stale threshold
			if p.Settings.Realtime.DynamicThreshold.Debug {
				log.Debug("removing stale dynamic threshold from memory", logger.String("species", species))
			}
			// Remove the stale threshold from the map
			delete(p.DynamicThresholds, species)
			removedCount++
		}
	}
	p.thresholdsMutex.Unlock()

	// Log memory cleanup if any were removed
	if removedCount > 0 {
		log.Debug("cleaned up stale dynamic thresholds from memory", logger.Int("count", removedCount))
	}

	// Also clean up expired thresholds from the database
	if p.Ds != nil {
		dbCount, err := p.Ds.DeleteExpiredDynamicThresholds(now)
		if err != nil {
			log.Warn("failed to clean up expired dynamic thresholds from database", logger.Error(err))
		} else if dbCount > 0 {
			log.Info("cleaned up expired dynamic thresholds from database", logger.Int64("count", dbCount))
		}
	}
}

// ResetDynamicThreshold resets a single species threshold and clears its history (BG-59)
// This removes both the in-memory threshold and the database records.
// The error return is always nil as database errors are logged internally
// and the operation is best-effort for database cleanup.
func (p *Processor) ResetDynamicThreshold(speciesName string) error {
	log := GetLogger()
	// Normalize to lowercase to match the casing used by addSpeciesToDynamicThresholds
	speciesName = strings.ToLower(speciesName)

	// Lock the mutex to ensure thread-safe access to the DynamicThresholds map
	p.thresholdsMutex.Lock()

	// Remove from in-memory map
	delete(p.DynamicThresholds, speciesName)
	p.thresholdsMutex.Unlock()

	// Delete from database
	if p.Ds != nil {
		// Delete the threshold record
		if err := p.Ds.DeleteDynamicThreshold(speciesName); err != nil {
			log.Warn("failed to delete dynamic threshold from database", logger.String("species", speciesName), logger.Error(err))
			// Don't return error - the in-memory reset was successful
		}

		// Delete event history for this species (no need to record reset event since history is cleared)
		if err := p.Ds.DeleteThresholdEvents(speciesName); err != nil {
			log.Warn("failed to delete threshold events from database", logger.String("species", speciesName), logger.Error(err))
		}
	}

	log.Info("reset dynamic threshold", logger.String("species", speciesName))
	return nil
}

// ResetAllDynamicThresholds resets all thresholds and clears all history (BG-59)
// Returns the count of reset thresholds. The error return is always nil as database
// errors are logged internally and the operation is best-effort for database cleanup;
// in-memory reset is always successful.
func (p *Processor) ResetAllDynamicThresholds() (int64, error) {
	log := GetLogger()
	// Lock the mutex to ensure thread-safe access to the DynamicThresholds map
	p.thresholdsMutex.Lock()

	// Count in-memory thresholds
	count := int64(len(p.DynamicThresholds))

	// Clear all in-memory thresholds (no need to record reset events since history is cleared)
	p.DynamicThresholds = make(map[string]*DynamicThreshold)
	p.thresholdsMutex.Unlock()

	// Delete all from database
	if p.Ds != nil {
		dbCount, err := p.Ds.DeleteAllDynamicThresholds()
		if err != nil {
			log.Warn("failed to delete all dynamic thresholds from database", logger.Error(err))
			// Don't return error - the in-memory reset was successful
		}

		// Use the higher count (in case database had more records)
		if dbCount > count {
			count = dbCount
		}

		// Delete all event history
		if _, err := p.Ds.DeleteAllThresholdEvents(); err != nil {
			log.Warn("failed to delete all threshold events from database", logger.Error(err))
		}
	}

	log.Info("reset all dynamic thresholds", logger.Int64("count", count))
	return count, nil
}

// GetDynamicThresholdData returns a copy of the current dynamic thresholds for API access (BG-59)
// This provides a safe read-only view of the thresholds without exposing the internal map
func (p *Processor) GetDynamicThresholdData() []DynamicThresholdData {
	p.thresholdsMutex.RLock()
	defer p.thresholdsMutex.RUnlock()

	data := make([]DynamicThresholdData, 0, len(p.DynamicThresholds))
	now := time.Now()

	for speciesName, dt := range p.DynamicThresholds {
		data = append(data, DynamicThresholdData{
			SpeciesName:    speciesName,
			ScientificName: dt.ScientificName,
			Level:          dt.Level,
			CurrentValue:   dt.CurrentValue,
			HighConfCount:  dt.HighConfCount,
			ExpiresAt:      dt.Timer,
			IsActive:       dt.Timer.After(now),
		})
	}

	return data
}

// DynamicThresholdData represents threshold data for API responses (BG-59)
type DynamicThresholdData struct {
	SpeciesName    string    `json:"speciesName"`
	ScientificName string    `json:"scientificName"`
	Level          int       `json:"level"`
	CurrentValue   float64   `json:"currentValue"`
	HighConfCount  int       `json:"highConfCount"`
	ExpiresAt      time.Time `json:"expiresAt"`
	IsActive       bool      `json:"isActive"`
}
