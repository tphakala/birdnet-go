package birdnet

// Batch size thresholds based on overlap setting.
// Higher overlap means more chunks per second, benefiting from batching.
const (
	OverlapThresholdLow  = 2.0 // Below this, batching disabled
	OverlapThresholdHigh = 2.5 // At or above this, use max batch size
	BatchSizeLow         = 1   // Disabled - chunks arrive slowly
	BatchSizeMedium      = 4   // Moderate batching
	BatchSizeHigh        = 8   // Maximum batching for high chunk rate
)

// CalculateBatchSize determines the optimal batch size from the overlap setting.
// Higher overlap produces more audio chunks per second, making batching beneficial.
//
// Returns:
//   - 1 (disabled) when overlap < 2.0: chunks arrive slowly, no batching benefit
//   - 4 when overlap is 2.0-2.5: moderate chunk rate
//   - 8 when overlap >= 2.5: high chunk rate, maximize throughput
func CalculateBatchSize(overlap float64) int {
	switch {
	case overlap < OverlapThresholdLow:
		return BatchSizeLow
	case overlap < OverlapThresholdHigh:
		return BatchSizeMedium
	default:
		return BatchSizeHigh
	}
}
