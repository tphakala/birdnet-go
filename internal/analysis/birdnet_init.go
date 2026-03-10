package analysis

import (
	"github.com/tphakala/birdnet-go/internal/birdnet"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/myaudio"
	"github.com/tphakala/birdnet-go/internal/observability/metrics"
)

var bn *birdnet.BirdNET // BirdNET interpreter

// modelNameBirdNET is the model name used for metrics tracking
const modelNameBirdNET = "birdnet"

// initializeBirdNET initializes the BirdNET interpreter and included species list if not already initialized.
func initializeBirdNET(settings *conf.Settings) error {
	// Initialize the BirdNET interpreter only if not already initialized
	if bn == nil {
		var err error
		bn, err = birdnet.NewBirdNET(settings)
		if err != nil {
			return errors.New(err).
				Component("analysis").
				Category(errors.CategoryModelInit).
				Context("operation", "initialize_birdnet").
				Build()
		}

		// Initialize included species list
		if err := birdnet.BuildRangeFilter(bn); err != nil {
			return errors.New(err).
				Component("analysis").
				Category(errors.CategoryModelInit).
				Context("operation", "build_range_filter").
				Build()
		}

		// Initialize float32 pool for audio conversion
		if err := myaudio.InitFloat32Pool(); err != nil {
			return errors.New(err).
				Component("analysis").
				Category(errors.CategoryAudio).
				Context("operation", "initialize_float32_pool").
				Build()
		}
	}
	return nil
}

// UpdateBirdNETModelLoadedMetric updates the model loaded metric status.
// This should be called after metrics are initialized to report model status.
//
// IMPORTANT: This function relies on the package-global 'bn' variable being
// initialized first via initializeBirdNET(). Calling this function before
// BirdNET initialization will result in no metric update.
//
// Note: This is only used in realtime mode as metrics are not used for
// on-demand file/directory analysis operations.
func UpdateBirdNETModelLoadedMetric(birdnetMetrics *metrics.BirdNETMetrics) {
	if birdnetMetrics != nil && bn != nil {
		// Model is loaded successfully
		birdnetMetrics.RecordModelLoad(modelNameBirdNET, nil)
	}
}
