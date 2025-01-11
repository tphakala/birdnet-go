package analysis

import (
	"fmt"

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
		if err := birdnet.BuildRangeFilter(bn); err != nil {
			return fmt.Errorf("failed to initialize BirdNET: %w", err)
		}
	}
	return nil
}
