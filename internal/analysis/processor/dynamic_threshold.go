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

// getAdjustedConfidenceThreshold returns the current dynamic threshold for a species.
// This function does NOT trigger learning from detections - learning happens separately
// in LearnFromApprovedDetection() when a detection is approved.
// Note: This function may reset expired thresholds as a side effect.
// If isCustomThreshold is true (species has a user-configured threshold), the function returns it unchanged.
func (p *Processor) getAdjustedConfidenceThreshold(speciesLowercase string, baseThreshold float32, isCustomThreshold bool) float32 {
	// If this is a custom user-configured threshold, respect it and don't apply dynamic adjustments.
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

	now := time.Now()

	// Check for expired thresholds and reset if needed
	// Guard ensures we only reset if not already at base state (prevents redundant work)
	if now.After(dt.Timer) && (dt.Level > 0 || dt.HighConfCount > 0) {
		// Track previous state for event recording
		previousLevel := dt.Level
		previousValue := dt.CurrentValue

		dt.Level = 0
		dt.CurrentValue = float64(baseThreshold)
		dt.HighConfCount = 0
		dt.LastLearnedAt = time.Time{}

		if previousLevel != 0 {
			p.recordThresholdEvent(speciesLowercase, dt.ScientificName, previousLevel, 0, previousValue, dt.CurrentValue, changeReasonExpiry, 0)
		}
	}

	// Apply minimum threshold enforcement
	if dt.CurrentValue < p.Settings.Realtime.DynamicThreshold.Min {
		dt.CurrentValue = p.Settings.Realtime.DynamicThreshold.Min
	}

	return float32(dt.CurrentValue)
}

// recordThresholdEvent saves a threshold change event to the database (BG-59)
// scientificName is required for v2only datastore to correctly resolve the Label FK.
// See issue #1907 for context on why both names are needed.
func (p *Processor) recordThresholdEvent(speciesName, scientificName string, previousLevel, newLevel int, previousValue, newValue float64, changeReason string, confidence float64) {
	if p.Ds == nil {
		return
	}

	event := &datastore.ThresholdEvent{
		SpeciesName:    speciesName,
		ScientificName: scientificName, // Used by v2only for correct label resolution (#1907)
		PreviousLevel:  previousLevel,
		NewLevel:       newLevel,
		PreviousValue:  previousValue,
		NewValue:       newValue,
		ChangeReason:   changeReason,
		Confidence:     confidence,
		CreatedAt:      time.Now(),
	}

	// Save asynchronously to avoid blocking the detection pipeline
	go func() {
		if err := p.Ds.SaveThresholdEvent(event); err != nil {
			log := GetLogger()
			log.Error("Failed to save threshold event", logger.String("species", speciesName), logger.Error(err))
		}
	}()
}

// LearnFromApprovedDetection updates the dynamic threshold for a species based on an
// approved high-confidence detection. This should only be called when a detection has
// been confirmed (approved), not when first detected. This ensures that false positives
// (discarded detections) do not trigger threshold learning.
func (p *Processor) LearnFromApprovedDetection(speciesLowercase, scientificName string, confidence float32) {
	if !p.Settings.Realtime.DynamicThreshold.Enabled {
		return
	}

	// Only learn from detections that exceed the trigger threshold
	if confidence <= float32(p.Settings.Realtime.DynamicThreshold.Trigger) {
		return
	}

	// Check if this species has a custom threshold - don't learn for custom thresholds
	config, exists := lookupSpeciesConfig(p.Settings.Realtime.Species.Config, speciesLowercase, scientificName)
	if exists && config.Threshold > 0 {
		return
	}

	// Use global threshold as base (species has no custom threshold)
	baseThreshold := float32(p.Settings.BirdNET.Threshold)

	// Calculate learning cooldown based on detection window duration
	// This prevents multiple threshold learnings within a single detection event
	captureLength := time.Duration(p.Settings.Realtime.Audio.Export.Length) * time.Second
	preCaptureLength := time.Duration(p.Settings.Realtime.Audio.Export.PreCapture) * time.Second
	learningCooldown := captureLength - preCaptureLength
	const minCooldown = 5 * time.Second
	if learningCooldown < minCooldown {
		learningCooldown = minCooldown
	}

	// Ensure species exists in threshold map (reuses existing initialization logic)
	p.addSpeciesToDynamicThresholds(speciesLowercase, scientificName, baseThreshold)

	p.thresholdsMutex.Lock()
	defer p.thresholdsMutex.Unlock()

	dt := p.DynamicThresholds[speciesLowercase]
	if dt == nil {
		// Species was removed concurrently (e.g., via ResetDynamicThreshold)
		// Skip learning for this edge case
		return
	}

	now := time.Now()
	previousLevel := dt.Level
	previousValue := dt.CurrentValue

	// Always extend the timer when we see an approved high-confidence detection
	dt.Timer = now.Add(time.Duration(dt.ValidHours) * time.Hour)

	// Only learn if enough time has passed since last learning
	// This ensures learnings happen across different detection events, not within a single window
	if !dt.LastLearnedAt.IsZero() && now.Sub(dt.LastLearnedAt) < learningCooldown {
		return
	}

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

	// Apply minimum threshold clamp
	if dt.CurrentValue < p.Settings.Realtime.DynamicThreshold.Min {
		dt.CurrentValue = p.Settings.Realtime.DynamicThreshold.Min
	}

	// Record event if level changed
	if dt.Level != previousLevel {
		p.recordThresholdEvent(speciesLowercase, dt.ScientificName, previousLevel, dt.Level,
			previousValue, dt.CurrentValue, changeReasonHighConfidence, float64(confidence))
	}

	if p.Settings.Realtime.DynamicThreshold.Debug {
		log := GetLogger()
		log.Debug("Learned from approved detection",
			logger.String("species", speciesLowercase),
			logger.Float32("confidence", confidence),
			logger.Int("level", dt.Level),
			logger.Float64("threshold", dt.CurrentValue))
	}
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
