package processor

import (
	"strings"
	"time"

	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/errors"
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
//
// Tracking is per species, not per model: the learned state (Level, HighConfCount,
// Timer) means "this species is confirmed present at this location", which is
// model-independent. Any model's approved high-confidence detection advances the
// shared level. The applied threshold is computed live at read time from the
// caller's model-specific base and the shared level (see getAdjustedConfidenceThreshold),
// so no absolute value is stored.
type DynamicThreshold struct {
	Level         int
	Timer         time.Time
	HighConfCount int
	ValidHours    int
	// BaseThreshold is the model-global base of the model that last learned this
	// species. It is display/persistence metadata only and is NEVER used to compute
	// the applied threshold; that always uses the caller's live per-model base.
	BaseThreshold  float64
	ScientificName string
	LastLearnedAt  time.Time // Tracks when the last threshold learning event occurred to prevent multiple learnings within a single detection window
	// FirstCreated records when this species threshold was first initialized. Persisted
	// once and never overwritten on re-flush, so it reflects true creation time (#4195).
	FirstCreated time.Time
	// LastTriggered records the last time an approved high-confidence detection reinforced
	// this threshold; initialized to the creation time. Persisted so the stored value
	// reflects a real trigger rather than the periodic flush time (#4195).
	LastTriggered time.Time
}

// effectiveDynamicThreshold computes the applied threshold from a model-specific
// base and the species' shared learned level, clamped to the configured minimum.
// At level 0 the multiplier is 1.0, so this returns base (clamped), preserving the
// no-adjustment default.
func effectiveDynamicThreshold(base float64, level int, minThreshold float64) float64 {
	value := base * levelMultiplier(level)
	if value < minThreshold {
		value = minThreshold
	}
	return value
}

// addSpeciesToDynamicThresholds adds a species to the dynamic thresholds map if it doesn't already exist.
func (p *Processor) addSpeciesToDynamicThresholds(speciesLowercase, scientificName string, baseThreshold float32) {
	// The species (lowercase common name) is the identity key, in memory and when
	// persisted (#4195). Never admit an empty key: it cannot be persisted and would
	// simulate learning for a phantom species that silently consumes memory.
	if speciesLowercase == "" {
		return
	}

	settings := p.currentSettings()

	// Lock the mutex to ensure thread-safe access to the DynamicThresholds map
	p.thresholdsMutex.Lock()
	defer p.thresholdsMutex.Unlock()

	// Check if the species already has a dynamic threshold
	existing, exists := p.DynamicThresholds[speciesLowercase]

	// If it doesn't exist, initialize it
	if !exists {
		if settings.Realtime.DynamicThreshold.Debug {
			log := GetLogger()
			log.Debug("Initializing dynamic threshold", logger.String("species", speciesLowercase))
		}
		now := time.Now()
		p.DynamicThresholds[speciesLowercase] = &DynamicThreshold{
			Level:          0,
			BaseThreshold:  float64(baseThreshold),
			Timer:          now,
			HighConfCount:  0,
			ValidHours:     settings.Realtime.DynamicThreshold.ValidHours,
			ScientificName: scientificName,
			FirstCreated:   now,
			LastTriggered:  now,
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

	settings := p.currentSettings()
	minThreshold := settings.Realtime.DynamicThreshold.Min

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
		// Track previous state for event recording. The applied value is derived, so
		// reconstruct the pre-reset value from this caller's base and the old level.
		previousLevel := dt.Level
		previousValue := effectiveDynamicThreshold(float64(baseThreshold), previousLevel, minThreshold)

		dt.Level = 0
		dt.HighConfCount = 0
		dt.LastLearnedAt = time.Time{}
		dt.BaseThreshold = float64(baseThreshold)

		if previousLevel != 0 {
			resetValue := effectiveDynamicThreshold(float64(baseThreshold), 0, minThreshold)
			p.recordThresholdEvent(speciesLowercase, dt.ScientificName, previousLevel, 0, previousValue, resetValue, changeReasonExpiry, 0)
		}
	}

	// Compute the applied threshold live from this model's base and the shared level.
	return float32(effectiveDynamicThreshold(float64(baseThreshold), dt.Level, minThreshold))
}

// recordThresholdEvent saves a threshold change event to the database (BG-59).
// Events are keyed by species (lowercase common name); scientificName is stored as
// display metadata only, not used to resolve any model/label (#4195).
func (p *Processor) recordThresholdEvent(speciesName, scientificName string, previousLevel, newLevel int, previousValue, newValue float64, changeReason string, confidence float64) {
	if p.Ds == nil {
		return
	}

	event := &datastore.ThresholdEvent{
		SpeciesName:    speciesName,
		ScientificName: scientificName, // display metadata only (#4195)
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
			_ = errors.New(err).
				Component("analysis.processor").
				Category(errors.CategoryDatabase).
				Context("operation", "save_threshold_event").
				Build()
		}
	}()
}

// LearnFromApprovedDetection updates the dynamic threshold for a species based on an
// approved high-confidence detection. This should only be called when a detection has
// been confirmed (approved), not when first detected. This ensures that false positives
// (discarded detections) do not trigger threshold learning.
// baseThreshold is the model-specific base for the model that produced this
// detection (already computed by the caller); it is recorded as display metadata
// and used to compute the recorded event value, never to key the entry.
func (p *Processor) LearnFromApprovedDetection(speciesLowercase, scientificName string, confidence, baseThreshold float32) {
	settings := p.currentSettings()
	if !settings.Realtime.DynamicThreshold.Enabled {
		return
	}

	// Only learn from detections that exceed the trigger threshold
	if confidence <= float32(settings.Realtime.DynamicThreshold.Trigger) {
		return
	}

	// Check if this species has a custom threshold - don't learn for custom thresholds
	config, exists := lookupSpeciesConfig(settings.Realtime.Species.Config, speciesLowercase, scientificName)
	if exists && config.Threshold > 0 {
		return
	}

	// Calculate learning cooldown based on detection window duration
	// This prevents multiple threshold learnings within a single detection event
	captureLength := time.Duration(settings.Realtime.Audio.Export.Length) * time.Second
	preCaptureLength := time.Duration(settings.Realtime.Audio.Export.PreCapture) * time.Second
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

	minThreshold := settings.Realtime.DynamicThreshold.Min
	now := time.Now()
	previousLevel := dt.Level
	previousValue := effectiveDynamicThreshold(dt.BaseThreshold, previousLevel, minThreshold)

	// Always extend the timer when we see an approved high-confidence detection
	dt.Timer = now.Add(time.Duration(dt.ValidHours) * time.Hour)

	// Only learn if enough time has passed since last learning.
	// This ensures learnings happen across different detection events, not within a
	// single window. It also collapses the multiple contributing models of one
	// approval (processApprovedDetection loops over them) into a single level
	// increment: the first model sets LastLearnedAt, the rest only extend the timer.
	if !dt.LastLearnedAt.IsZero() && now.Sub(dt.LastLearnedAt) < learningCooldown {
		return
	}

	// Record the model-specific base of the model that actually learned (display
	// metadata). Set inside the learn block, after the cooldown gate, so the
	// contributing models skipped by the cooldown do not overwrite it nondeterministically.
	dt.BaseThreshold = float64(baseThreshold)

	dt.HighConfCount++
	dt.LastLearnedAt = now
	dt.LastTriggered = now // real trigger time, persisted instead of the flush time (#4195)

	// Adjust the dynamic threshold based on the number of high-confidence detections
	switch dt.HighConfCount {
	case 1:
		dt.Level = 1
	case 2:
		dt.Level = 2
	default:
		// Level 3 is the maximum reduction; any count >= 3 stays at this level
		dt.Level = 3
	}

	newValue := effectiveDynamicThreshold(dt.BaseThreshold, dt.Level, minThreshold)

	// Record event if level changed
	if dt.Level != previousLevel {
		p.recordThresholdEvent(speciesLowercase, dt.ScientificName, previousLevel, dt.Level,
			previousValue, newValue, changeReasonHighConfidence, float64(confidence))
	}

	if settings.Realtime.DynamicThreshold.Debug {
		log := GetLogger()
		log.Debug("Learned from approved detection",
			logger.String("species", speciesLowercase),
			logger.Float32("confidence", confidence),
			logger.Int("level", dt.Level),
			logger.Float64("threshold", newValue))
	}
}

// updateDynamicThreshold updates the dynamic threshold for a given species if enabled.
func (p *Processor) updateDynamicThreshold(modelID, commonName string, confidence float64) {
	settings := p.currentSettings()
	if settings.Realtime.DynamicThreshold.Enabled {
		// Lock the mutex to ensure thread-safe access to the DynamicThresholds map
		p.thresholdsMutex.Lock()
		defer p.thresholdsMutex.Unlock()

		// Check if the species already has a dynamic threshold. The entry is keyed
		// per species; modelID still selects the model-specific base to compare the
		// detection confidence against before extending the timer.
		// Note: scientific name not available in this context, but common name lookup is sufficient.
		// Lowercase the key to match how entries are stored (addSpeciesToDynamicThresholds);
		// otherwise a title-cased common name misses and the timer is never extended.
		if dt, exists := p.DynamicThresholds[strings.ToLower(commonName)]; exists && confidence > float64(p.getBaseConfidenceThreshold(settings, commonName, "", modelID)) {
			// Update the timer to extend the threshold's validity
			// Note: dt is a pointer, so this directly mutates the struct in the map
			dt.Timer = time.Now().Add(time.Duration(dt.ValidHours) * time.Hour)
		}
	}
}

// cleanUpDynamicThresholds removes stale dynamic thresholds for species that haven't been detected for a long time.
// This cleans up both the in-memory map and the database.
func (p *Processor) cleanUpDynamicThresholds() {
	settings := p.currentSettings()
	log := GetLogger()
	// Calculate the duration after which a dynamic threshold is considered stale
	staleDuration := time.Duration(settings.Realtime.DynamicThreshold.ValidHours) * time.Hour

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
			if settings.Realtime.DynamicThreshold.Debug {
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

// ResetDynamicThreshold resets a single species threshold across all models and clears its history (BG-59)
// This removes both the in-memory threshold and the database records.
// The error return is always nil as database errors are logged internally
// and the operation is best-effort for database cleanup.
//
// The mutex is held for the entire operation (memory clear + DB delete) to
// prevent a race where a new detection inserts data between the memory clear
// and the DB delete, causing the DB delete to wipe the newly-inserted record.
func (p *Processor) ResetDynamicThreshold(speciesName string) error {
	log := GetLogger()
	// Normalize to lowercase to match the casing used by addSpeciesToDynamicThresholds
	speciesName = strings.ToLower(speciesName)

	// Lock the mutex for the entire operation to prevent races between
	// memory clear and DB delete. See drainPendingResets for the pattern.
	p.thresholdsMutex.Lock()

	// Remove the species entry from the in-memory map. Entries are keyed per
	// species, so a single delete clears it; DeleteDynamicThreshold likewise
	// operates on the species name.
	delete(p.DynamicThresholds, speciesName)
	if p.pendingResets != nil {
		p.pendingResets[speciesName] = struct{}{}
	}

	// Delete from database while still holding the lock to prevent a concurrent
	// detection from inserting new data that this delete would then wipe.
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
	p.thresholdsMutex.Unlock()

	log.Info("reset dynamic threshold", logger.String("species", speciesName))
	return nil
}

// ResetAllDynamicThresholds resets all thresholds and clears all history (BG-59)
// Returns the count of reset thresholds. The error return is always nil as database
// errors are logged internally and the operation is best-effort for database cleanup;
// in-memory reset is always successful.
//
// The mutex is held for the entire operation (memory clear + DB delete) to
// prevent a race where a new detection inserts data between the memory clear
// and the DB delete, causing the DB delete to wipe the newly-inserted record.
func (p *Processor) ResetAllDynamicThresholds() (int64, error) {
	log := GetLogger()
	// Lock the mutex for the entire operation to prevent races between
	// memory clear and DB delete. See drainPendingResets for the pattern.
	p.thresholdsMutex.Lock()

	// Count in-memory thresholds
	count := int64(len(p.DynamicThresholds))

	// Clear all in-memory thresholds and set pendingResetAll so the periodic
	// persistence goroutine won't re-insert a stale snapshot into the database.
	// Also clear individual pending resets since pendingResetAll supersedes them.
	p.DynamicThresholds = make(map[string]*DynamicThreshold)
	p.pendingResetAll = true
	if p.pendingResets != nil {
		p.pendingResets = make(map[string]struct{})
	}

	// Delete all from database while still holding the lock to prevent a
	// concurrent detection from inserting new data that this delete would wipe.
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
	p.thresholdsMutex.Unlock()

	log.Info("reset all dynamic thresholds", logger.Int64("count", count))
	return count, nil
}

// GetDynamicThresholdData returns a copy of the current dynamic thresholds for API access (BG-59)
// This provides a safe read-only view of the thresholds without exposing the internal map
func (p *Processor) GetDynamicThresholdData() []DynamicThresholdData {
	p.thresholdsMutex.RLock()
	defer p.thresholdsMutex.RUnlock()

	// Snapshot settings once so the derived values reflect a single consistent view.
	settings := p.currentSettings()
	minThreshold := settings.Realtime.DynamicThreshold.Min
	data := make([]DynamicThresholdData, 0, len(p.DynamicThresholds))
	now := time.Now()

	for speciesName, dt := range p.DynamicThresholds {
		data = append(data, DynamicThresholdData{
			SpeciesName:    speciesName,
			ScientificName: dt.ScientificName,
			Level:          dt.Level,
			CurrentValue:   effectiveDynamicThreshold(dt.BaseThreshold, dt.Level, minThreshold),
			BaseThreshold:  dt.BaseThreshold,
			HighConfCount:  dt.HighConfCount,
			ExpiresAt:      dt.Timer,
			FirstCreated:   dt.FirstCreated,
			LastTriggered:  dt.LastTriggered,
			TriggerCount:   dt.HighConfCount,
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
	CurrentValue   float64   `json:"currentValue"`  // derived from BaseThreshold and Level at read time
	BaseThreshold  float64   `json:"baseThreshold"` // model-global base of the model that last learned (display only)
	HighConfCount  int       `json:"highConfCount"`
	ExpiresAt      time.Time `json:"expiresAt"`
	FirstCreated   time.Time `json:"firstCreated"`  // real creation time (#4195)
	LastTriggered  time.Time `json:"lastTriggered"` // real last-trigger time (#4195)
	TriggerCount   int       `json:"triggerCount"`
	IsActive       bool      `json:"isActive"`
}

// levelMultiplier returns the threshold multiplier for a given level.
// This centralizes the level-to-multiplier mapping used by LearnFromApprovedDetection
// and effectiveDynamicThreshold to avoid duplication.
func levelMultiplier(level int) float64 {
	switch level {
	case 1:
		return thresholdLevel1Multiplier
	case 2:
		return thresholdLevel2Multiplier
	case 3:
		return thresholdLevel3Multiplier
	default:
		return 1.0 // Level 0 = no reduction
	}
}
