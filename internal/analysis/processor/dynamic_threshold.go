package processor

import (
	"log"
	"time"

	"github.com/tphakala/birdnet-go/internal/datastore"
)

// addSpeciesToDynamicThresholds adds a species to the dynamic thresholds map if it doesn't already exist.
func (p *Processor) addSpeciesToDynamicThresholds(speciesLowercase string, baseThreshold float32) {
	_, exists := p.DynamicThresholds[speciesLowercase]
	if !exists {
		log.Printf("Initializing dynamic threshold for %s\n", speciesLowercase)
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
func (p *Processor) getAdjustedConfidenceThreshold(speciesLowercase string, result datastore.Results, baseThreshold float32) float32 {
	dt, exists := p.DynamicThresholds[speciesLowercase]
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

// cleanUpDynamicThresholds removes stale dynamic thresholds for species that haven't been detected for a long time.
func (p *Processor) cleanUpDynamicThresholds() {
	staleDuration := 24 * time.Hour // Duration after which a dynamic threshold is considered stale
	now := time.Now()

	for species, dt := range p.DynamicThresholds {
		if now.Sub(dt.Timer) > staleDuration {
			log.Printf("Removing stale dynamic threshold for %s", species)
			delete(p.DynamicThresholds, species)
		}
	}
}
