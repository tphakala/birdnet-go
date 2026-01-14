package birdnet

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCalculateBatchSize(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		overlap  float64
		expected int
	}{
		// Below low threshold - batching disabled
		{"zero overlap", 0.0, BatchSizeLow},
		{"low overlap", 1.0, BatchSizeLow},
		{"just below threshold", 1.99, BatchSizeLow},

		// At/above low threshold, below high - medium batching
		{"at low threshold", 2.0, BatchSizeMedium},
		{"between thresholds", 2.25, BatchSizeMedium},
		{"just below high threshold", 2.49, BatchSizeMedium},

		// At/above high threshold - max batching
		{"at high threshold", 2.5, BatchSizeHigh},
		{"above high threshold", 2.7, BatchSizeHigh},
		{"max valid overlap", 2.9, BatchSizeHigh},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := CalculateBatchSize(tt.overlap)
			assert.Equal(t, tt.expected, result, "overlap=%.2f", tt.overlap)
		})
	}
}

func TestCalculateBatchSize_Constants(t *testing.T) {
	t.Parallel()

	// Verify constants have expected values
	assert.InDelta(t, 2.0, OverlapThresholdLow, 0.001)
	assert.InDelta(t, 2.5, OverlapThresholdHigh, 0.001)
	assert.Equal(t, 1, BatchSizeLow)
	assert.Equal(t, 4, BatchSizeMedium)
	assert.Equal(t, 8, BatchSizeHigh)
}

func TestCalculateBatchSize_Transitions(t *testing.T) {
	t.Parallel()

	// Test that batch size changes at threshold boundaries
	tests := []struct {
		name        string
		fromOverlap float64
		toOverlap   float64
		expectFrom  int
		expectTo    int
	}{
		{"disabled to medium", 1.5, 2.0, BatchSizeLow, BatchSizeMedium},
		{"medium to high", 2.0, 2.5, BatchSizeMedium, BatchSizeHigh},
		{"high to medium", 2.7, 2.3, BatchSizeHigh, BatchSizeMedium},
		{"medium to disabled", 2.3, 1.8, BatchSizeMedium, BatchSizeLow},
		{"no change within low", 1.0, 1.5, BatchSizeLow, BatchSizeLow},
		{"no change within medium", 2.1, 2.4, BatchSizeMedium, BatchSizeMedium},
		{"no change within high", 2.6, 2.9, BatchSizeHigh, BatchSizeHigh},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			fromSize := CalculateBatchSize(tt.fromOverlap)
			toSize := CalculateBatchSize(tt.toOverlap)
			assert.Equal(t, tt.expectFrom, fromSize, "from overlap=%.2f", tt.fromOverlap)
			assert.Equal(t, tt.expectTo, toSize, "to overlap=%.2f", tt.toOverlap)
		})
	}
}
