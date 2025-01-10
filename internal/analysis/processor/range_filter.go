package processor

import (
	"time"
)

// Add or update the updateIncludedSpecies function
func (p *Processor) ReloadRangeFilter() error {
	today := time.Now().Truncate(24 * time.Hour)

	// Update location based species list
	speciesScores, err := p.Bn.GetProbableSpecies(today, 0.0)
	if err != nil {
		return err
	}

	// Convert the speciesScores slice to a slice of species labels
	var includedSpecies []string
	for _, speciesScore := range speciesScores {
		includedSpecies = append(includedSpecies, speciesScore.Label)
	}

	p.Settings.UpdateIncludedSpecies(includedSpecies)

	return nil
}
