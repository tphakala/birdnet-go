package analysis

import (
	"fmt"

	"github.com/tphakala/birdnet-go/internal/birdnet"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/myaudio"
	"github.com/tphakala/birdnet-go/internal/observability/metrics"
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
		
		// Initialize float32 pool for audio conversion
		if err := myaudio.InitFloat32Pool(); err != nil {
			return fmt.Errorf("failed to initialize float32 pool: %w", err)
		}
	}
	return nil
}

// UpdateBirdNETModelLoadedMetric updates the model loaded metric status
// This should be called after metrics are initialized to report model status
func UpdateBirdNETModelLoadedMetric(birdnetMetrics *metrics.BirdNETMetrics) {
	if birdnetMetrics != nil && bn != nil {
		// Model is loaded successfully
		birdnetMetrics.RecordModelLoad("birdnet", nil)
	}
}
