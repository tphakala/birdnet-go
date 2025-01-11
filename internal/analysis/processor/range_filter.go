package processor

import (
	"fmt"
	"log"
	"os"
	"time"
)

// Add or update the updateIncludedSpecies function
func (p *Processor) BuildRangeFilter() error {
	today := time.Now().Truncate(24 * time.Hour)

	// Update location based species list
	speciesScores, err := p.Bn.GetProbableSpecies(today, 0.0)
	if err != nil {
		return err
	}

	// Convert the speciesScores slice to a slice of species labels
	var includedSpecies []string
	for _, speciesScore := range speciesScores {
		// debug species and score
		log.Printf("	Species: %s, Score: %f\n", speciesScore.Label, speciesScore.Score)
		includedSpecies = append(includedSpecies, speciesScore.Label)
	}

	if p.Settings.BirdNET.RangeFilter.Debug {
		// Debug: Write included species to file
		debugFile := "debug_included_species.txt"
		content := fmt.Sprintf("Updated at: %s\nSpecies count: %d\n\nSpecies list:\n",
			time.Now().Format("2006-01-02 15:04:05"),
			len(includedSpecies))
		for _, species := range includedSpecies {
			content += species + "\n"
		}
		if err := os.WriteFile(debugFile, []byte(content), 0644); err != nil {
			log.Printf("❌ [range_filter/rebuild] Warning: Failed to write included species file: %v\n", err)
		}
	}

	p.Settings.UpdateIncludedSpecies(includedSpecies)

	return nil
}
