package birdnet

import (
	"fmt"
	"math"
	"testing"

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
				if len(results) != 3 {
					t.Errorf("Expected 3 results, got %d", len(results))
				}
				expected := []struct {
					species    string
					confidence float32
				}{
					{"Robin", 0.9},
					{"Sparrow", 0.7},
					{"Eagle", 0.5},
				}
				for i, exp := range expected {
					if results[i].Species != exp.species {
						t.Errorf("Result %d: expected species %s, got %s", i, exp.species, results[i].Species)
					}
					if results[i].Confidence != exp.confidence {
						t.Errorf("Result %d: expected confidence %f, got %f", i, exp.confidence, results[i].Confidence)
					}
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
				if len(results) != 0 {
					t.Errorf("Expected 0 results, got %d", len(results))
				}
			},
		},
		{
			name:       "Single element",
			labels:     []string{"Robin"},
			confidence: []float32{0.95},
			wantErr:    false,
			validate: func(t *testing.T, results []datastore.Results) {
				if len(results) != 1 {
					t.Errorf("Expected 1 result, got %d", len(results))
				}
				if results[0].Species != "Robin" {
					t.Errorf("Expected species Robin, got %s", results[0].Species)
				}
				if results[0].Confidence != 0.95 {
					t.Errorf("Expected confidence 0.95, got %f", results[0].Confidence)
				}
			},
		},
		{
			name:       "Large dataset (BirdNET size)",
			labels:     generateTestLabels(6522),
			confidence: generateTestConfidence(6522),
			wantErr:    false,
			validate: func(t *testing.T, results []datastore.Results) {
				if len(results) != 6522 {
					t.Errorf("Expected 6522 results, got %d", len(results))
				}
				// Verify a few samples
				if results[0].Species != "Species_000001" {
					t.Errorf("First species incorrect: %s", results[0].Species)
				}
				if results[6521].Species != "Species_006522" {
					t.Errorf("Last species incorrect: %s", results[6521].Species)
				}
			},
		},
		{
			name:       "Confidence edge values",
			labels:     []string{"A", "B", "C", "D"},
			confidence: []float32{0.0, 1.0, 0.5, 0.999},
			wantErr:    false,
			validate: func(t *testing.T, results []datastore.Results) {
				if results[0].Confidence != 0.0 {
					t.Errorf("Expected confidence 0.0, got %f", results[0].Confidence)
				}
				if results[1].Confidence != 1.0 {
					t.Errorf("Expected confidence 1.0, got %f", results[1].Confidence)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			results, err := pairLabelsAndConfidence(tt.labels, tt.confidence)
			
			if tt.wantErr {
				if err == nil {
					t.Errorf("Expected error containing '%s', got nil", tt.errContains)
				} else if tt.errContains != "" && err.Error() != tt.errContains {
					t.Errorf("Expected error '%s', got '%s'", tt.errContains, err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error: %v", err)
				}
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
				// Results should point to the same buffer
				if &results[0] != &buffer[0] {
					t.Error("Results should reference the same buffer")
				}
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
					if results[i].Species != exp.species {
						t.Errorf("Result %d: expected species %s, got %s", i, exp.species, results[i].Species)
					}
					if results[i].Confidence != exp.confidence {
						t.Errorf("Result %d: expected confidence %f, got %f", i, exp.confidence, results[i].Confidence)
					}
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
				if len(results) != 0 {
					t.Errorf("Expected 0 results, got %d", len(results))
				}
			},
		},
		{
			name:       "Reuse buffer multiple times",
			labels:     []string{"Robin", "Sparrow", "Eagle"},
			confidence: []float32{0.9, 0.7, 0.5},
			bufferSize: 3,
			wantErr:    false,
			validate: func(t *testing.T, buffer []datastore.Results, results []datastore.Results) {
				// First use
				if results[0].Species != "Robin" || results[0].Confidence != 0.9 {
					t.Errorf("First use failed")
				}
				
				// Reuse with different values
				newLabels := []string{"Hawk", "Owl", "Crow"}
				newConfidence := []float32{0.8, 0.6, 0.4}
				results2, err := pairLabelsAndConfidenceReuse(newLabels, newConfidence, buffer)
				if err != nil {
					t.Errorf("Second use failed: %v", err)
				}
				
				// Check that buffer was updated
				if results2[0].Species != "Hawk" || results2[0].Confidence != 0.8 {
					t.Errorf("Buffer reuse failed: got %s/%f", results2[0].Species, results2[0].Confidence)
				}
				
				// Original results slice should also be updated (same underlying array)
				if results[0].Species != "Hawk" || results[0].Confidence != 0.8 {
					t.Errorf("Buffer update not reflected in original slice")
				}
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
				if err == nil {
					t.Errorf("Expected error containing '%s', got nil", tt.errContains)
				} else if tt.errContains != "" && err.Error() != tt.errContains {
					t.Errorf("Expected error '%s', got '%s'", tt.errContains, err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error: %v", err)
				}
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
	if err != nil {
		t.Fatalf("First call failed: %v", err)
	}
	
	// Save first results
	saved := make([]datastore.Results, len(results1))
	for i, r := range results1 {
		saved[i] = r.Copy()
	}
	
	// Second call
	results2, err := pairLabelsAndConfidence(labels, confidence2)
	if err != nil {
		t.Fatalf("Second call failed: %v", err)
	}
	
	// Verify first results haven't changed
	for i, r := range saved {
		if results1[i].Species != r.Species || results1[i].Confidence != r.Confidence {
			t.Errorf("First results were modified at index %d", i)
		}
	}
	
	// Verify second results are correct
	expected2 := []float32{0.3, 0.6, 0.8}
	for i, exp := range expected2 {
		if results2[i].Confidence != exp {
			t.Errorf("Second result %d: expected confidence %f, got %f", i, exp, results2[i].Confidence)
		}
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
			
			if len(results) != len(tt.expected) {
				t.Fatalf("Length mismatch: expected %d, got %d", len(tt.expected), len(results))
			}
			
			// For equal confidence values, we just check that they're sorted by confidence
			for i := 0; i < len(results); i++ {
				if i > 0 && results[i].Confidence > results[i-1].Confidence {
					t.Errorf("Results not sorted correctly at index %d: %f > %f", 
						i, results[i].Confidence, results[i-1].Confidence)
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
			
			if len(result) != tt.wantCount {
				t.Errorf("Expected %d results, got %d", tt.wantCount, len(result))
			}
			
			// Verify it returns the first N elements
			for i := 0; i < len(result); i++ {
				if result[i].Species != tt.input[i].Species {
					t.Errorf("Result %d mismatch: expected %s, got %s", 
						i, tt.input[i].Species, result[i].Species)
				}
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
				for i, r := range results {
					if math.Abs(float64(r)-0.5) > 0.0001 {
						t.Errorf("Index %d: expected 0.5, got %f", i, r)
					}
				}
			},
		},
		{
			name:        "Positive and negative",
			predictions: []float32{-2, -1, 0, 1, 2},
			sensitivity: 1.0,
			validate: func(t *testing.T, results []float32) {
				// Sigmoid should be symmetric around 0.5
				if math.Abs(float64(results[0]+results[4])-1.0) > 0.0001 {
					t.Errorf("Sigmoid not symmetric: %f + %f != 1.0", results[0], results[4])
				}
				if math.Abs(float64(results[1]+results[3])-1.0) > 0.0001 {
					t.Errorf("Sigmoid not symmetric: %f + %f != 1.0", results[1], results[3])
				}
				if math.Abs(float64(results[2])-0.5) > 0.0001 {
					t.Errorf("Sigmoid(0) should be 0.5, got %f", results[2])
				}
			},
		},
		{
			name:        "Sensitivity effect",
			predictions: []float32{1.0},
			sensitivity: 2.0,
			validate: func(t *testing.T, results []float32) {
				// Higher sensitivity should give higher confidence
				sigmoid1 := 1.0 / (1.0 + math.Exp(-1.0))
				sigmoid2 := 1.0 / (1.0 + math.Exp(-2.0))
				if results[0] <= float32(sigmoid1) {
					t.Errorf("Higher sensitivity should increase confidence: %f <= %f", results[0], sigmoid1)
				}
				if math.Abs(float64(results[0])-sigmoid2) > 0.0001 {
					t.Errorf("Expected %f, got %f", sigmoid2, results[0])
				}
			},
		},
		{
			name:        "Empty input",
			predictions: []float32{},
			sensitivity: 1.0,
			validate: func(t *testing.T, results []float32) {
				if len(results) != 0 {
					t.Errorf("Expected empty result, got %d elements", len(results))
				}
			},
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			results := applySigmoidToPredictions(tt.predictions, tt.sensitivity)
			
			if len(results) != len(tt.predictions) {
				t.Fatalf("Length mismatch: expected %d, got %d", len(tt.predictions), len(results))
			}
			
			if tt.validate != nil {
				tt.validate(t, results)
			}
		})
	}
}

// Helper functions for testing
func generateTestLabels(count int) []string {
	labels := make([]string, count)
	for i := 0; i < count; i++ {
		labels[i] = fmt.Sprintf("Species_%06d", i+1)
	}
	return labels
}

func generateTestConfidence(count int) []float32 {
	confidence := make([]float32, count)
	for i := 0; i < count; i++ {
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
				if len(original) != len(reuse) {
					t.Errorf("Length mismatch: %d vs %d", len(original), len(reuse))
				}
				// Results should be identical
				for i := range original {
					if math.Abs(float64(original[i]-reuse[i])) > 0.0001 {
						t.Errorf("Index %d: original=%f, reuse=%f", i, original[i], reuse[i])
					}
				}
				// Reuse should return the same buffer
				if &reuse[0] != &buffer[0] {
					t.Error("Buffer reuse should return the same buffer")
				}
			},
		},
		{
			name:        "Buffer size mismatch fallback",
			predictions: []float32{-1, 0, 1},
			sensitivity: 1.0,
			bufferSize:  2, // Smaller than predictions
			validate: func(t *testing.T, original []float32, reuse []float32, buffer []float32) {
				if len(original) != len(reuse) {
					t.Errorf("Length mismatch: %d vs %d", len(original), len(reuse))
				}
				// Results should be identical even with fallback
				for i := range original {
					if math.Abs(float64(original[i]-reuse[i])) > 0.0001 {
						t.Errorf("Index %d: original=%f, reuse=%f", i, original[i], reuse[i])
					}
				}
				// Should not use the buffer due to size mismatch
				if len(reuse) == len(buffer) && &reuse[0] == &buffer[0] {
					t.Error("Should have fallen back to allocation, not used buffer")
				}
			},
		},
		{
			name:        "Empty inputs",
			predictions: []float32{},
			sensitivity: 1.0,
			bufferSize:  0,
			validate: func(t *testing.T, original []float32, reuse []float32, buffer []float32) {
				if len(original) != 0 || len(reuse) != 0 {
					t.Error("Empty inputs should produce empty outputs")
				}
			},
		},
		{
			name:        "Large buffer",
			predictions: []float32{1.0, 2.0, 3.0},
			sensitivity: 2.0,
			bufferSize:  5, // Larger than predictions
			validate: func(t *testing.T, original []float32, reuse []float32, buffer []float32) {
				// Should fallback due to size mismatch
				for i := range original {
					if math.Abs(float64(original[i]-reuse[i])) > 0.0001 {
						t.Errorf("Index %d: original=%f, reuse=%f", i, original[i], reuse[i])
					}
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
				if len(results) != k {
					t.Errorf("Expected %d results, got %d", k, len(results))
				}
				// Should be sorted in descending order
				for i := 1; i < len(results); i++ {
					if results[i].Confidence > results[i-1].Confidence {
						t.Errorf("Results not sorted: %f > %f at index %d", 
							results[i].Confidence, results[i-1].Confidence, i)
					}
				}
				// Check that we got the highest confidence values
				expectedOrder := []string{"A", "C", "B"} // 0.9, 0.8, 0.7
				for i, expected := range expectedOrder {
					if results[i].Species != expected {
						t.Errorf("Index %d: expected %s, got %s", i, expected, results[i].Species)
					}
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
				if len(results) != 3 {
					t.Errorf("Expected 3 results, got %d", len(results))
				}
				// Should be sorted
				if results[0].Confidence != 0.9 || results[1].Confidence != 0.7 || results[2].Confidence != 0.5 {
					t.Error("Full array not sorted correctly")
				}
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
				if len(results) != 2 {
					t.Errorf("Expected 2 results (input length), got %d", len(results))
				}
				// Should be sorted
				if results[0].Confidence != 0.9 || results[1].Confidence != 0.8 {
					t.Error("Results not sorted correctly")
				}
			},
		},
		{
			name:  "Empty input",
			input: []datastore.Results{},
			k:     5,
			validate: func(t *testing.T, results []datastore.Results, k int) {
				if len(results) != 0 {
					t.Errorf("Expected 0 results for empty input, got %d", len(results))
				}
			},
		},
		{
			name: "k is zero",
			input: []datastore.Results{
				{Species: "A", Confidence: 0.9},
			},
			k: 0,
			validate: func(t *testing.T, results []datastore.Results, k int) {
				if len(results) != 0 {
					t.Errorf("Expected 0 results for k=0, got %d", len(results))
				}
			},
		},
		{
			name: "k is negative",
			input: []datastore.Results{
				{Species: "A", Confidence: 0.9},
			},
			k: -1,
			validate: func(t *testing.T, results []datastore.Results, k int) {
				if len(results) != 0 {
					t.Errorf("Expected 0 results for negative k, got %d", len(results))
				}
			},
		},
		{
			name: "Large dataset (realistic BirdNET size)",
			input: generateLargeTestResults(6522),
			k: 10,
			validate: func(t *testing.T, results []datastore.Results, k int) {
				if len(results) != 10 {
					t.Errorf("Expected 10 results, got %d", len(results))
				}
				// Verify sorted order
				for i := 1; i < len(results); i++ {
					if results[i].Confidence > results[i-1].Confidence {
						t.Errorf("Large dataset not sorted correctly at index %d", i)
					}
				}
				// First result should have the highest confidence
				// In our generated data, confidence decreases from 0.99 down
				if results[0].Confidence < 0.99 {
					t.Errorf("Expected highest confidence ~0.99, got %f", results[0].Confidence)
				}
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
	for i := 0; i < count; i++ {
		results[i] = datastore.Results{
			Species:    fmt.Sprintf("Species_%06d", i+1),
			Confidence: float32(100-i%100) / 100.0, // Decreasing confidence pattern
		}
	}
	return results
}