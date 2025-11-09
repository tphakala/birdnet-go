package processor

import (
	"time"

	"github.com/tphakala/birdnet-go/internal/datastore"
)

// addSpeciesToDynamicThresholds adds a species to the dynamic thresholds map if it doesn't already exist.
func (p *Processor) addSpeciesToDynamicThresholds(speciesLowercase string, baseThreshold float32) {
	// Lock the mutex to ensure thread-safe access to the DynamicThresholds map
	p.thresholdsMutex.Lock()
	defer p.thresholdsMutex.Unlock()

	// Check if the species already has a dynamic threshold
	_, exists := p.DynamicThresholds[speciesLowercase]

	// If it doesn't exist, initialize it
	if !exists {
		if p.Settings.Realtime.DynamicThreshold.Debug {
			logger := GetLogger()
			logger.Debug("Initializing dynamic threshold", "species", speciesLowercase)
		}
		p.DynamicThresholds[speciesLowercase] = &DynamicThreshold{
			Level:         0,
			CurrentValue:  float64(baseThreshold),
			Timer:         time.Now(),
			HighConfCount: 0,
			ValidHours:    p.Settings.Realtime.DynamicThreshold.ValidHours,
		}
	}
}

// getAdjustedConfidenceThreshold applies dynamic threshold logic to adjust the confidence threshold based on recent detections.
// If isCustomThreshold is true (species has a user-configured threshold), the function respects it as a floor
// and does not apply dynamic adjustments, ensuring user intent is never overridden.
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

	// If the detection confidence exceeds the trigger threshold
	if result.Confidence > float32(p.Settings.Realtime.DynamicThreshold.Trigger) {
		dt.HighConfCount++
		dt.Timer = time.Now().Add(time.Duration(dt.ValidHours) * time.Hour)

		// Adjust the dynamic threshold based on the number of high-confidence detections
		switch dt.HighConfCount {
		case 1:
			dt.Level = 1
			dt.CurrentValue = float64(baseThreshold * 0.75)
		case 2:
			dt.Level = 2
			dt.CurrentValue = float64(baseThreshold * 0.5)
		case 3:
			dt.Level = 3
			dt.CurrentValue = float64(baseThreshold * 0.25)
		}
	} else if time.Now().After(dt.Timer) {
		// Reset the dynamic threshold if the timer has expired
		dt.Level = 0
		dt.CurrentValue = float64(baseThreshold)
		dt.HighConfCount = 0
	}

	// Ensure the dynamic threshold doesn't fall below the minimum threshold
	if dt.CurrentValue < p.Settings.Realtime.DynamicThreshold.Min {
		dt.CurrentValue = p.Settings.Realtime.DynamicThreshold.Min
	}

	return float32(dt.CurrentValue)
}

// updateDynamicThreshold updates the dynamic threshold for a given species if enabled.
func (p *Processor) updateDynamicThreshold(commonName string, confidence float64) {
	if p.Settings.Realtime.DynamicThreshold.Enabled {
		// Lock the mutex to ensure thread-safe access to the DynamicThresholds map
		p.thresholdsMutex.Lock()
		defer p.thresholdsMutex.Unlock()

		// Check if the species already has a dynamic threshold
		if dt, exists := p.DynamicThresholds[commonName]; exists && confidence > float64(p.getBaseConfidenceThreshold(commonName)) {
			// Update the timer to extend the threshold's validity
			dt.Timer = time.Now().Add(time.Duration(dt.ValidHours) * time.Hour)
			// Since we're modifying a struct in the map, we need to reassign it
			p.DynamicThresholds[commonName] = dt
		}
	}
}

// cleanUpDynamicThresholds removes stale dynamic thresholds for species that haven't been detected for a long time.
func (p *Processor) cleanUpDynamicThresholds() {
	// Calculate the duration after which a dynamic threshold is considered stale
	staleDuration := time.Duration(p.Settings.Realtime.DynamicThreshold.ValidHours) * time.Hour

	// Get the current time
	now := time.Now()

	// Lock the mutex to ensure thread-safe access to the DynamicThresholds map
	p.thresholdsMutex.Lock()
	defer p.thresholdsMutex.Unlock()

	// Iterate through all species in the DynamicThresholds map
	for species, dt := range p.DynamicThresholds {
		// Check if the threshold for this species is stale
		if now.Sub(dt.Timer) > staleDuration {
			// If debug mode is enabled, log the removal of the stale threshold
			if p.Settings.Realtime.DynamicThreshold.Debug {
				logger := GetLogger()
				logger.Debug("Removing stale dynamic threshold", "species", species)
			}
			// Remove the stale threshold from the map
			delete(p.DynamicThresholds, species)
		}
	}
}
