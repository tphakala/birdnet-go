package analysis

import (
	"fmt"

	"github.com/tphakala/birdnet-go/internal/birdnet"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/logger"
)

var bn *birdnet.BirdNET // BirdNET interpreter

// initializeBirdNET initializes the BirdNET interpreter and included species list if not already initialized.
func initializeBirdNET(settings *conf.Settings) error {
	// Initialize the BirdNET interpreter only if not already initialized
	if bn == nil {
		var err error
		// Get the analyzer logger from the global logger
		analyzerLogger := logger.Named("birdnet-go").Named("analyzer")
		bn, err = birdnet.NewBirdNET(settings, analyzerLogger)
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
