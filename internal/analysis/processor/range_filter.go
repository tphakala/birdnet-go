package processor

import (
	"log"
	"strings"
	"time"
)

// Add or update the updateIncludedSpecies function
func (p *Processor) updateIncludedSpecies(date time.Time) {
	speciesScores, err := p.Bn.GetProbableSpecies(date, 0.0)
	if err != nil {
		log.Printf("Failed to get probable species: %s", err)
		return
	}

	var includedSpecies []string
	for _, speciesScore := range speciesScores {
		includedSpecies = append(includedSpecies, speciesScore.Label)
	}

	p.Settings.UpdateIncludedSpecies(includedSpecies)
	p.Settings.BirdNET.RangeFilter.LastUpdated = date

	// Update dynamic thresholds if enabled
	if p.Settings.Realtime.DynamicThreshold.Enabled {
		p.updateDynamicThresholds()
	}
}

// Add a new function to update dynamic thresholds
func (p *Processor) updateDynamicThresholds() {
	newDynamicThresholds := make(map[string]*DynamicThreshold)
	for _, species := range p.Settings.BirdNET.RangeFilter.Species {
		speciesLowercase := strings.ToLower(species)
		if dt, exists := p.DynamicThresholds[speciesLowercase]; exists {
			newDynamicThresholds[speciesLowercase] = dt
		} else {
			newDynamicThresholds[speciesLowercase] = &DynamicThreshold{
				Level:         0,
				CurrentValue:  float64(p.Settings.BirdNET.Threshold),
				Timer:         time.Now(),
				HighConfCount: 0,
				ValidHours:    p.Settings.Realtime.DynamicThreshold.ValidHours,
			}
		}
	}
	p.DynamicThresholds = newDynamicThresholds
}
