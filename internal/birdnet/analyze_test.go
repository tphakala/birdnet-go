package birdnet

import (
	"fmt"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/datastore"
)

// TestPairLabelsAndConfidence tests the pairLabelsAndConfidence function
func TestPairLabelsAndConfidence(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		labels      []string
		confidence  []float32
		wantErr     bool
		errContains string
		validate    func(t *testing.T, results []datastore.Results)
	}{
		{
			name:       "Valid pairing",
			labels:     []string{"Robin", "Sparrow", "Eagle"},
			confidence: []float32{0.9, 0.7, 0.5},
			wantErr:    false,
			validate: func(t *testing.T, results []datastore.Results) {
				t.Helper()
				assert.Len(t, results, 3)
				expected := []struct {
					species    string
					confidence float32
				}{
					{"Robin", 0.9},
					{"Sparrow", 0.7},
					{"Eagle", 0.5},
				}
				for i, exp := range expected {
					assert.Equal(t, exp.species, results[i].Species, "Result %d species mismatch", i)
					assert.InDelta(t, exp.confidence, results[i].Confidence, 0.0001, "Result %d confidence mismatch", i)
				}
			},
		},
		{
			name:        "Mismatched lengths - more labels",
			labels:      []string{"Robin", "Sparrow", "Eagle"},
			confidence:  []float32{0.9, 0.7},
			wantErr:     true,
			errContains: "mismatched labels and predictions lengths: 3 vs 2",
		},
		{
			name:        "Mismatched lengths - more confidence",
			labels:      []string{"Robin", "Sparrow"},
			confidence:  []float32{0.9, 0.7, 0.5},
			wantErr:     true,
			errContains: "mismatched labels and predictions lengths: 2 vs 3",
		},
		{
			name:       "Empty slices",
			labels:     []string{},
			confidence: []float32{},
			wantErr:    false,
			validate: func(t *testing.T, results []datastore.Results) {
				t.Helper()
				assert.Empty(t, results)
			},
		},
		{
			name:       "Single element",
			labels:     []string{"Robin"},
			confidence: []float32{0.95},
			wantErr:    false,
			validate: func(t *testing.T, results []datastore.Results) {
				t.Helper()
				require.Len(t, results, 1)
				assert.Equal(t, "Robin", results[0].Species)
				assert.InDelta(t, float32(0.95), results[0].Confidence, 0.0001)
			},
		},
		{
			name:       "Large dataset (BirdNET size)",
			labels:     generateTestLabels(6522),
			confidence: generateTestConfidence(6522),
			wantErr:    false,
			validate: func(t *testing.T, results []datastore.Results) {
				t.Helper()
				require.Len(t, results, 6522)
				// Verify a few samples
				assert.Equal(t, "Species_000001", results[0].Species, "First species incorrect")
				assert.Equal(t, "Species_006522", results[6521].Species, "Last species incorrect")
			},
		},
		{
			name:       "Confidence edge values",
			labels:     []string{"A", "B", "C", "D"},
			confidence: []float32{0.0, 1.0, 0.5, 0.999},
			wantErr:    false,
			validate: func(t *testing.T, results []datastore.Results) {
				t.Helper()
				assert.InDelta(t, float32(0.0), results[0].Confidence, 0.0001)
				assert.InDelta(t, float32(1.0), results[1].Confidence, 0.0001)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			results, err := pairLabelsAndConfidence(tt.labels, tt.confidence)

			if tt.wantErr {
				require.Error(t, err)
				if tt.errContains != "" {
					assert.Equal(t, tt.errContains, err.Error())
				}
			} else {
				require.NoError(t, err)
				if tt.validate != nil {
					tt.validate(t, results)
				}
			}
		})
	}
}

// TestPairLabelsAndConfidenceReuseBuffer tests the buffer reuse function
func TestPairLabelsAndConfidenceReuseBuffer(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		labels      []string
		confidence  []float32
		bufferSize  int
		wantErr     bool
		errContains string
		validate    func(t *testing.T, buffer []datastore.Results, results []datastore.Results)
	}{
		{
			name:       "Valid buffer reuse",
			labels:     []string{"Robin", "Sparrow", "Eagle"},
			confidence: []float32{0.9, 0.7, 0.5},
			bufferSize: 3,
			wantErr:    false,
			validate: func(t *testing.T, buffer []datastore.Results, results []datastore.Results) {
				t.Helper()
				// Results should point to the same buffer
				assert.Same(t, &results[0], &buffer[0], "Results should reference the same buffer")
				// Check values
				expected := []struct {
					species    string
					confidence float32
				}{
					{"Robin", 0.9},
					{"Sparrow", 0.7},
					{"Eagle", 0.5},
				}
				for i, exp := range expected {
					assert.Equal(t, exp.species, results[i].Species, "Result %d species mismatch", i)
					assert.InDelta(t, exp.confidence, results[i].Confidence, 0.0001, "Result %d confidence mismatch", i)
				}
			},
		},
		{
			name:        "Buffer too small",
			labels:      []string{"Robin", "Sparrow", "Eagle"},
			confidence:  []float32{0.9, 0.7, 0.5},
			bufferSize:  2,
			wantErr:     true,
			errContains: "buffer size mismatch: 2 vs 3",
		},
		{
			name:        "Buffer too large",
			labels:      []string{"Robin", "Sparrow"},
			confidence:  []float32{0.9, 0.7},
			bufferSize:  3,
			wantErr:     true,
			errContains: "buffer size mismatch: 3 vs 2",
		},
		{
			name:        "Mismatched labels and confidence",
			labels:      []string{"Robin", "Sparrow", "Eagle"},
			confidence:  []float32{0.9, 0.7},
			bufferSize:  3,
			wantErr:     true,
			errContains: "mismatched labels and predictions lengths: 3 vs 2",
		},
		{
			name:       "Empty data with empty buffer",
			labels:     []string{},
			confidence: []float32{},
			bufferSize: 0,
			wantErr:    false,
			validate: func(t *testing.T, buffer []datastore.Results, results []datastore.Results) {
				t.Helper()
				assert.Empty(t, results)
			},
		},
		{
			name:       "Reuse buffer multiple times",
			labels:     []string{"Robin", "Sparrow", "Eagle"},
			confidence: []float32{0.9, 0.7, 0.5},
			bufferSize: 3,
			wantErr:    false,
			validate: func(t *testing.T, buffer []datastore.Results, results []datastore.Results) {
				t.Helper()
				// First use
				assert.Equal(t, "Robin", results[0].Species)
				assert.InDelta(t, float32(0.9), results[0].Confidence, 0.0001)

				// Reuse with different values
				newLabels := []string{"Hawk", "Owl", "Crow"}
				newConfidence := []float32{0.8, 0.6, 0.4}
				results2, err := pairLabelsAndConfidenceReuse(newLabels, newConfidence, buffer)
				require.NoError(t, err, "Second use failed")

				// Check that buffer was updated
				assert.Equal(t, "Hawk", results2[0].Species)
				assert.InDelta(t, float32(0.8), results2[0].Confidence, 0.0001)

				// Original results slice should also be updated (same underlying array)
				assert.Equal(t, "Hawk", results[0].Species, "Buffer update not reflected in original slice")
				assert.InDelta(t, float32(0.8), results[0].Confidence, 0.0001, "Buffer update not reflected in original slice")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Create buffer
			buffer := make([]datastore.Results, tt.bufferSize)

			// Call function
			results, err := pairLabelsAndConfidenceReuse(tt.labels, tt.confidence, buffer)

			if tt.wantErr {
				require.Error(t, err)
				if tt.errContains != "" {
					assert.Equal(t, tt.errContains, err.Error())
				}
			} else {
				require.NoError(t, err)
				if tt.validate != nil {
					tt.validate(t, buffer, results)
				}
			}
		})
	}
}

// TestPairLabelsAndConfidenceReuse tests that the function works correctly with pre-allocated slices
func TestPairLabelsAndConfidenceReuse(t *testing.T) {
	t.Parallel()

	labels := []string{"Robin", "Sparrow", "Eagle"}
	confidence1 := []float32{0.9, 0.7, 0.5}
	confidence2 := []float32{0.3, 0.6, 0.8}

	// First call
	results1, err := pairLabelsAndConfidence(labels, confidence1)
	require.NoError(t, err, "First call failed")

	// Save first results
	saved := make([]datastore.Results, len(results1))
	for i, r := range results1 {
		saved[i] = r.Copy()
	}

	// Second call
	results2, err := pairLabelsAndConfidence(labels, confidence2)
	require.NoError(t, err, "Second call failed")

	// Verify first results haven't changed
	for i, r := range saved {
		assert.Equal(t, r.Species, results1[i].Species, "First results species modified at index %d", i)
		assert.InDelta(t, r.Confidence, results1[i].Confidence, 0.0001, "First results confidence modified at index %d", i)
	}

	// Verify second results are correct
	expected2 := []float32{0.3, 0.6, 0.8}
	for i, exp := range expected2 {
		assert.InDelta(t, exp, results2[i].Confidence, 0.0001, "Second result %d confidence mismatch", i)
	}
}

// TestSortResults tests the sortResults function
func TestSortResults(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    []datastore.Results
		expected []datastore.Results
	}{
		{
			name: "Already sorted",
			input: []datastore.Results{
				{Species: "A", Confidence: 0.9},
				{Species: "B", Confidence: 0.7},
				{Species: "C", Confidence: 0.5},
			},
			expected: []datastore.Results{
				{Species: "A", Confidence: 0.9},
				{Species: "B", Confidence: 0.7},
				{Species: "C", Confidence: 0.5},
			},
		},
		{
			name: "Reverse order",
			input: []datastore.Results{
				{Species: "C", Confidence: 0.5},
				{Species: "B", Confidence: 0.7},
				{Species: "A", Confidence: 0.9},
			},
			expected: []datastore.Results{
				{Species: "A", Confidence: 0.9},
				{Species: "B", Confidence: 0.7},
				{Species: "C", Confidence: 0.5},
			},
		},
		{
			name: "Random order",
			input: []datastore.Results{
				{Species: "B", Confidence: 0.7},
				{Species: "A", Confidence: 0.9},
				{Species: "D", Confidence: 0.3},
				{Species: "C", Confidence: 0.5},
			},
			expected: []datastore.Results{
				{Species: "A", Confidence: 0.9},
				{Species: "B", Confidence: 0.7},
				{Species: "C", Confidence: 0.5},
				{Species: "D", Confidence: 0.3},
			},
		},
		{
			name:     "Empty slice",
			input:    []datastore.Results{},
			expected: []datastore.Results{},
		},
		{
			name: "Single element",
			input: []datastore.Results{
				{Species: "A", Confidence: 0.9},
			},
			expected: []datastore.Results{
				{Species: "A", Confidence: 0.9},
			},
		},
		{
			name: "Equal confidence values",
			input: []datastore.Results{
				{Species: "A", Confidence: 0.5},
				{Species: "B", Confidence: 0.5},
				{Species: "C", Confidence: 0.9},
				{Species: "D", Confidence: 0.5},
			},
			expected: []datastore.Results{
				{Species: "C", Confidence: 0.9},
				{Species: "A", Confidence: 0.5}, // Order among equal values may vary
				{Species: "B", Confidence: 0.5},
				{Species: "D", Confidence: 0.5},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Make a copy to avoid modifying test data
			results := make([]datastore.Results, len(tt.input))
			copy(results, tt.input)

			sortResults(results)

			assert.Len(t, results, len(tt.expected))

			// For equal confidence values, we just check that they're sorted by confidence
			for i := range results {
				if i > 0 {
					assert.LessOrEqual(t, results[i].Confidence, results[i-1].Confidence,
						"Results not sorted correctly at index %d", i)
				}
			}
		})
	}
}

// TestTrimResultsToMax tests the trimResultsToMax function
func TestTrimResultsToMax(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		input     []datastore.Results
		maxCount  int
		wantCount int
	}{
		{
			name: "Trim to 3 from 5",
			input: []datastore.Results{
				{Species: "A", Confidence: 0.9},
				{Species: "B", Confidence: 0.8},
				{Species: "C", Confidence: 0.7},
				{Species: "D", Confidence: 0.6},
				{Species: "E", Confidence: 0.5},
			},
			maxCount:  3,
			wantCount: 3,
		},
		{
			name: "No trimming needed",
			input: []datastore.Results{
				{Species: "A", Confidence: 0.9},
				{Species: "B", Confidence: 0.8},
			},
			maxCount:  5,
			wantCount: 2,
		},
		{
			name:      "Empty input",
			input:     []datastore.Results{},
			maxCount:  10,
			wantCount: 0,
		},
		{
			name: "Trim to 0",
			input: []datastore.Results{
				{Species: "A", Confidence: 0.9},
				{Species: "B", Confidence: 0.8},
			},
			maxCount:  0,
			wantCount: 0,
		},
		{
			name: "Exact match",
			input: []datastore.Results{
				{Species: "A", Confidence: 0.9},
				{Species: "B", Confidence: 0.8},
				{Species: "C", Confidence: 0.7},
			},
			maxCount:  3,
			wantCount: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result := trimResultsToMax(tt.input, tt.maxCount)

			assert.Len(t, result, tt.wantCount)

			// Verify it returns the first N elements
			for i := range result {
				assert.Equal(t, tt.input[i].Species, result[i].Species, "Result %d mismatch", i)
			}
		})
	}
}

// TestApplySigmoidToPredictions tests the sigmoid application
func TestApplySigmoidToPredictions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		predictions []float32
		sensitivity float64
		validate    func(t *testing.T, results []float32)
	}{
		{
			name:        "Zero predictions",
			predictions: []float32{0, 0, 0},
			sensitivity: 1.0,
			validate: func(t *testing.T, results []float32) {
				t.Helper()
				for i, r := range results {
					assert.InDelta(t, 0.5, r, 0.0001, "Index %d", i)
				}
			},
		},
		{
			name:        "Positive and negative",
			predictions: []float32{-2, -1, 0, 1, 2},
			sensitivity: 1.0,
			validate: func(t *testing.T, results []float32) {
				t.Helper()
				// Sigmoid should be symmetric around 0.5
				assert.InDelta(t, 1.0, results[0]+results[4], 0.0001, "Sigmoid not symmetric: %f + %f", results[0], results[4])
				assert.InDelta(t, 1.0, results[1]+results[3], 0.0001, "Sigmoid not symmetric: %f + %f", results[1], results[3])
				assert.InDelta(t, 0.5, results[2], 0.0001, "Sigmoid(0) should be 0.5")
			},
		},
		{
			name:        "Sensitivity effect",
			predictions: []float32{1.0},
			sensitivity: 2.0,
			validate: func(t *testing.T, results []float32) {
				t.Helper()
				// Higher sensitivity should give higher confidence
				sigmoid1 := 1.0 / (1.0 + math.Exp(-1.0))
				sigmoid2 := 1.0 / (1.0 + math.Exp(-2.0))
				assert.Greater(t, results[0], float32(sigmoid1), "Higher sensitivity should increase confidence")
				assert.InDelta(t, sigmoid2, results[0], 0.0001)
			},
		},
		{
			name:        "Empty input",
			predictions: []float32{},
			sensitivity: 1.0,
			validate: func(t *testing.T, results []float32) {
				t.Helper()
				assert.Empty(t, results)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			results := applySigmoidToPredictions(tt.predictions, tt.sensitivity)

			assert.Len(t, results, len(tt.predictions))

			if tt.validate != nil {
				tt.validate(t, results)
			}
		})
	}
}

// Helper functions for testing
func generateTestLabels(count int) []string {
	labels := make([]string, count)
	for i := range count {
		labels[i] = fmt.Sprintf("Species_%06d", i+1)
	}
	return labels
}

func generateTestConfidence(count int) []float32 {
	confidence := make([]float32, count)
	for i := range count {
		confidence[i] = float32(i%100) / 100.0
	}
	return confidence
}

// TestApplySigmoidToPredictionsReuse tests the optimized sigmoid function with buffer reuse
func TestApplySigmoidToPredictionsReuse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		predictions []float32
		sensitivity float64
		bufferSize  int
		validate    func(t *testing.T, original []float32, reuse []float32, buffer []float32)
	}{
		{
			name:        "Buffer size matches",
			predictions: []float32{-2, -1, 0, 1, 2},
			sensitivity: 1.0,
			bufferSize:  5,
			validate: func(t *testing.T, original []float32, reuse []float32, buffer []float32) {
				t.Helper()
				assert.Len(t, reuse, len(original))
				// Results should be identical
				for i := range original {
					assert.InDelta(t, original[i], reuse[i], 0.0001, "Index %d", i)
				}
				// Reuse should return the same buffer
				assert.Same(t, &buffer[0], &reuse[0], "Buffer reuse should return the same buffer")
			},
		},
		{
			name:        "Buffer size mismatch fallback",
			predictions: []float32{-1, 0, 1},
			sensitivity: 1.0,
			bufferSize:  2, // Smaller than predictions
			validate: func(t *testing.T, original []float32, reuse []float32, buffer []float32) {
				t.Helper()
				assert.Len(t, reuse, len(original))
				// Results should be identical even with fallback
				for i := range original {
					assert.InDelta(t, original[i], reuse[i], 0.0001, "Index %d", i)
				}
				// Should not use the buffer due to size mismatch
				if len(reuse) == len(buffer) {
					assert.NotSame(t, &buffer[0], &reuse[0], "Should have fallen back to allocation, not used buffer")
				}
			},
		},
		{
			name:        "Empty inputs",
			predictions: []float32{},
			sensitivity: 1.0,
			bufferSize:  0,
			validate: func(t *testing.T, original []float32, reuse []float32, buffer []float32) {
				t.Helper()
				assert.Empty(t, original)
				assert.Empty(t, reuse)
			},
		},
		{
			name:        "Large buffer",
			predictions: []float32{1.0, 2.0, 3.0},
			sensitivity: 2.0,
			bufferSize:  5, // Larger than predictions
			validate: func(t *testing.T, original []float32, reuse []float32, buffer []float32) {
				t.Helper()
				// Should fallback due to size mismatch
				for i := range original {
					assert.InDelta(t, original[i], reuse[i], 0.0001, "Index %d", i)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Get original results
			original := applySigmoidToPredictions(tt.predictions, tt.sensitivity)

			// Create buffer and test reuse function
			buffer := make([]float32, tt.bufferSize)
			reuse := applySigmoidToPredictionsReuse(tt.predictions, tt.sensitivity, buffer)

			if tt.validate != nil {
				tt.validate(t, original, reuse, buffer)
			}
		})
	}
}

// TestGetTopKResults tests the optimized top-k results function
func TestGetTopKResults(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    []datastore.Results
		k        int
		validate func(t *testing.T, results []datastore.Results, k int)
	}{
		{
			name: "Normal case - top 3 from 5",
			input: []datastore.Results{
				{Species: "A", Confidence: 0.9},
				{Species: "B", Confidence: 0.7},
				{Species: "C", Confidence: 0.8},
				{Species: "D", Confidence: 0.6},
				{Species: "E", Confidence: 0.5},
			},
			k: 3,
			validate: func(t *testing.T, results []datastore.Results, k int) {
				t.Helper()
				assert.Len(t, results, k)
				// Should be sorted in descending order
				for i := 1; i < len(results); i++ {
					assert.LessOrEqual(t, results[i].Confidence, results[i-1].Confidence,
						"Results not sorted at index %d", i)
				}
				// Check that we got the highest confidence values
				expectedOrder := []string{"A", "C", "B"} // 0.9, 0.8, 0.7
				for i, expected := range expectedOrder {
					assert.Equal(t, expected, results[i].Species, "Index %d", i)
				}
			},
		},
		{
			name: "k equals length",
			input: []datastore.Results{
				{Species: "A", Confidence: 0.5},
				{Species: "B", Confidence: 0.9},
				{Species: "C", Confidence: 0.7},
			},
			k: 3,
			validate: func(t *testing.T, results []datastore.Results, k int) {
				t.Helper()
				require.Len(t, results, 3)
				// Should be sorted
				assert.InDelta(t, float32(0.9), results[0].Confidence, 0.0001)
				assert.InDelta(t, float32(0.7), results[1].Confidence, 0.0001)
				assert.InDelta(t, float32(0.5), results[2].Confidence, 0.0001)
			},
		},
		{
			name: "k greater than length",
			input: []datastore.Results{
				{Species: "A", Confidence: 0.8},
				{Species: "B", Confidence: 0.9},
			},
			k: 5,
			validate: func(t *testing.T, results []datastore.Results, k int) {
				t.Helper()
				require.Len(t, results, 2, "Expected 2 results (input length)")
				// Should be sorted
				assert.InDelta(t, float32(0.9), results[0].Confidence, 0.0001)
				assert.InDelta(t, float32(0.8), results[1].Confidence, 0.0001)
			},
		},
		{
			name:  "Empty input",
			input: []datastore.Results{},
			k:     5,
			validate: func(t *testing.T, results []datastore.Results, k int) {
				t.Helper()
				assert.Empty(t, results, "Expected 0 results for empty input")
			},
		},
		{
			name: "k is zero",
			input: []datastore.Results{
				{Species: "A", Confidence: 0.9},
			},
			k: 0,
			validate: func(t *testing.T, results []datastore.Results, k int) {
				t.Helper()
				assert.Empty(t, results, "Expected 0 results for k=0")
			},
		},
		{
			name: "k is negative",
			input: []datastore.Results{
				{Species: "A", Confidence: 0.9},
			},
			k: -1,
			validate: func(t *testing.T, results []datastore.Results, k int) {
				t.Helper()
				assert.Empty(t, results, "Expected 0 results for negative k")
			},
		},
		{
			name:  "Large dataset (realistic BirdNET size)",
			input: generateLargeTestResults(6522),
			k:     10,
			validate: func(t *testing.T, results []datastore.Results, k int) {
				t.Helper()
				require.Len(t, results, 10)
				// Verify sorted order
				for i := 1; i < len(results); i++ {
					assert.LessOrEqual(t, results[i].Confidence, results[i-1].Confidence,
						"Large dataset not sorted correctly at index %d", i)
				}
				// First result should have the highest confidence
				// In our generated data, confidence decreases from 0.99 down
				assert.GreaterOrEqual(t, results[0].Confidence, float32(0.99),
					"Expected highest confidence ~0.99")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Make a copy to avoid modifying test data
			input := make([]datastore.Results, len(tt.input))
			copy(input, tt.input)

			results := getTopKResults(input, tt.k)

			if tt.validate != nil {
				tt.validate(t, results, tt.k)
			}
		})
	}
}

// generateLargeTestResults creates a large dataset for testing
func generateLargeTestResults(count int) []datastore.Results {
	results := make([]datastore.Results, count)
	for i := range count {
		results[i] = datastore.Results{
			Species:    fmt.Sprintf("Species_%06d", i+1),
			Confidence: float32(100-i%100) / 100.0, // Decreasing confidence pattern
		}
	}
	return results
}
