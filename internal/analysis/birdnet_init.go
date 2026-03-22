package analysis

import (
	"github.com/tphakala/birdnet-go/internal/birdnet"
	"github.com/tphakala/birdnet-go/internal/observability/metrics"
)

// modelNameBirdNET is the model name used for metrics tracking
const modelNameBirdNET = "birdnet"

// UpdateBirdNETModelLoadedMetric updates the model loaded metric status.
// This should be called after metrics are initialized to report model status.
//
// Note: This is only used in realtime mode as metrics are not used for
// on-demand file/directory analysis operations.
func UpdateBirdNETModelLoadedMetric(birdnetMetrics *metrics.BirdNETMetrics, bn *birdnet.BirdNET) {
	if birdnetMetrics != nil && bn != nil {
		// Model is loaded successfully
		birdnetMetrics.RecordModelLoad(modelNameBirdNET, nil)
	}
}
