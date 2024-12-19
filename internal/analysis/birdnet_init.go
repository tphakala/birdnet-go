package analysis

import (
	"fmt"
	"time"

	"github.com/tphakala/birdnet-go/internal/birdnet"
	"github.com/tphakala/birdnet-go/internal/conf"
)

var bn *birdnet.BirdNET // BirdNET interpreter

// initializeBirdNET initializes the BirdNET interpreter and included species list if not already initialized.
func initializeBirdNET(settings *conf.Settings) error {
	// Initialize the BirdNET interpreter only if not already initialized
	if bn == nil {
		var err error
		bn, err = birdnet.NewBirdNET(settings)
		if err != nil {
			return fmt.Errorf("failed to initialize BirdNET: %w", err)
		}

		// Initialize included species list
		if err := initializeIncludedSpecies(settings); err != nil {
			return fmt.Errorf("failed to initialize included species: %w", err)
		}
	}
	return nil
}

// initializeIncludedSpecies initializes the included species list based on date and geographic location
func initializeIncludedSpecies(settings *conf.Settings) error {
	speciesScores, err := bn.GetProbableSpecies(time.Now(), 0.0)
	if err != nil {
		return fmt.Errorf("error getting probable species: %w", err)
	}

	// Update included species in settings
	var includedSpecies []string
	for _, speciesScore := range speciesScores {
		includedSpecies = append(includedSpecies, speciesScore.Label)
	}
	settings.UpdateIncludedSpecies(includedSpecies)

	// debug print included species in human readable format
	/*for _, species := range includedSpecies {
		fmt.Printf("Included species: %s\n", species)
	}*/

	return nil
}
