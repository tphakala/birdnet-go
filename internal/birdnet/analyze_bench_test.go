package birdnet

import (
	"fmt"
	"testing"

	"github.com/tphakala/birdnet-go/internal/datastore"
)

// BenchmarkPairLabelsAndConfidence measures the memory allocation and performance
// of the pairLabelsAndConfidence function which shows up as a major memory consumer in pprof.
func BenchmarkPairLabelsAndConfidence(b *testing.B) {
	// Create realistic test data - BirdNET v2.4 has 6,522 bird species
	speciesCount := 6522
	labels := make([]string, speciesCount)
	confidence := make([]float32, speciesCount)
	
	// Generate realistic species names and confidence values
	for i := range speciesCount {
		labels[i] = generateSpeciesName(i)
		confidence[i] = float32(i%100) / 100.0 // 0.00 to 0.99
	}
	
	// Reset timer to exclude setup time
	b.ResetTimer()
	b.ReportAllocs()
	
	// Run benchmark
	for b.Loop() {
		results, err := pairLabelsAndConfidence(labels, confidence)
		if err != nil {
			b.Errorf("pairLabelsAndConfidence failed: %v", err)
		}
		
		// Prevent compiler optimization by using the results
		if len(results) != speciesCount {
			b.Errorf("Expected %d results, got %d", speciesCount, len(results))
		}
	}
}

// BenchmarkPairLabelsAndConfidenceMemory specifically measures memory allocations
// in the pairLabelsAndConfidence function.
func BenchmarkPairLabelsAndConfidenceMemory(b *testing.B) {
	speciesCount := 6522
	labels := make([]string, speciesCount)
	confidence := make([]float32, speciesCount)
	
	// Generate test data
	for i := range speciesCount {
		labels[i] = generateSpeciesName(i)
		confidence[i] = float32(i%100) / 100.0
	}
	
	// Reset timer and enable memory allocation tracking
	b.ResetTimer()
	b.ReportAllocs()
	
	// Benchmark with memory tracking
	for b.Loop() {
		results, err := pairLabelsAndConfidence(labels, confidence)
		if err != nil {
			b.Errorf("pairLabelsAndConfidence failed: %v", err)
		}
		
		// Prevent compiler optimization
		if results != nil {
			_ = results[0].Species
		}
	}
}

// BenchmarkExtractPredictions measures the performance of extracting predictions
// from TensorFlow Lite tensors.
// NOTE: This benchmark is disabled as it requires complex TensorFlow Lite setup
/*
func BenchmarkExtractPredictions(b *testing.B) {
	// Create a mock tensor with realistic size
	mockTensor := createMockTensor(6522)
	
	b.ResetTimer()
	b.ReportAllocs()
	
	for i := 0; i < b.N; i++ {
		predictions := extractPredictions(mockTensor)
		
		// Prevent compiler optimization
		if len(predictions) != 6522 {
			b.Errorf("Expected 6522 predictions, got %d", len(predictions))
		}
	}
}
*/

// BenchmarkApplySigmoidToPredictions measures the performance of applying sigmoid
// to prediction values.
func BenchmarkApplySigmoidToPredictions(b *testing.B) {
	// Create test predictions array
	predictions := make([]float32, 6522)
	for i := range predictions {
		predictions[i] = float32(i%200-100) / 100.0 // -1.0 to 1.0
	}
	
	sensitivity := 1.0
	
	b.ResetTimer()
	b.ReportAllocs()
	
	for b.Loop() {
		confidence := applySigmoidToPredictions(predictions, sensitivity)
		
		// Prevent compiler optimization
		if len(confidence) != 6522 {
			b.Errorf("Expected 6522 confidence values, got %d", len(confidence))
		}
	}
}

// BenchmarkSortResults measures the performance of sorting results by confidence.
func BenchmarkSortResults(b *testing.B) {
	// Create test results with realistic data
	results := make([]datastore.Results, 6522)
	for i := range results {
		results[i] = datastore.Results{
			Species:    generateSpeciesName(i),
			Confidence: float32(i%100) / 100.0,
		}
	}
	
	b.ResetTimer()
	b.ReportAllocs()
	
	for b.Loop() {
		// Make a copy to sort since sorting modifies the slice
		testResults := make([]datastore.Results, len(results))
		copy(testResults, results)
		
		sortResults(testResults)
		
		// Verify sorting worked (highest confidence first)
		if len(testResults) > 1 && testResults[0].Confidence < testResults[1].Confidence {
			b.Errorf("Results not sorted correctly")
		}
	}
}

// BenchmarkTrimResultsToMax measures the performance of trimming results to top N.
func BenchmarkTrimResultsToMax(b *testing.B) {
	// Create test results
	results := make([]datastore.Results, 6522)
	for i := range results {
		results[i] = datastore.Results{
			Species:    generateSpeciesName(i),
			Confidence: float32(i%100) / 100.0,
		}
	}
	
	b.ResetTimer()
	b.ReportAllocs()
	
	for b.Loop() {
		trimmed := trimResultsToMax(results, 10)
		
		// Verify trimming worked
		if len(trimmed) != 10 {
			b.Errorf("Expected 10 results, got %d", len(trimmed))
		}
	}
}

// BenchmarkFullPredictionPipeline measures the complete prediction pipeline
// excluding TensorFlow Lite inference (since we can't easily mock that).
func BenchmarkFullPredictionPipeline(b *testing.B) {
	speciesCount := 6522
	labels := make([]string, speciesCount)
	rawPredictions := make([]float32, speciesCount)
	
	// Generate realistic test data
	for i := range speciesCount {
		labels[i] = generateSpeciesName(i)
		rawPredictions[i] = float32(i%200-100) / 100.0 // -1.0 to 1.0
	}
	
	sensitivity := 1.0
	
	b.ResetTimer()
	b.ReportAllocs()
	
	for b.Loop() {
		// Step 1: Apply sigmoid to predictions
		confidence := applySigmoidToPredictions(rawPredictions, sensitivity)
		
		// Step 2: Pair labels with confidence values
		results, err := pairLabelsAndConfidence(labels, confidence)
		if err != nil {
			b.Errorf("pairLabelsAndConfidence failed: %v", err)
		}
		
		// Step 3: Sort results by confidence
		sortResults(results)
		
		// Step 4: Trim to top 10 results
		finalResults := trimResultsToMax(results, 10)
		
		// Prevent compiler optimization
		if len(finalResults) != 10 {
			b.Errorf("Expected 10 final results, got %d", len(finalResults))
		}
	}
}

// BenchmarkPairLabelsAndConfidenceOptimized tests the optimized buffer reuse version
func BenchmarkPairLabelsAndConfidenceOptimized(b *testing.B) {
	speciesCount := 6522
	labels := make([]string, speciesCount)
	confidence := make([]float32, speciesCount)
	buffer := make([]datastore.Results, speciesCount) // Pre-allocated buffer
	
	// Generate realistic data
	for i := 0; i < speciesCount; i++ {
		labels[i] = generateSpeciesName(i)
		confidence[i] = float32(i%100) / 100.0
	}
	
	b.ResetTimer()
	b.ReportAllocs()
	
	for b.Loop() {
		results, err := pairLabelsAndConfidenceReuse(labels, confidence, buffer)
		if err != nil {
			b.Errorf("pairLabelsAndConfidenceReuse failed: %v", err)
		}
		
		// Prevent compiler optimization
		if len(results) != speciesCount {
			b.Errorf("Expected %d results, got %d", speciesCount, len(results))
		}
	}
}

// BenchmarkMemoryGrowthPattern measures memory growth during repeated predictions
func BenchmarkMemoryGrowthPattern(b *testing.B) {
	speciesCount := 6522
	labels := make([]string, speciesCount)
	
	// Generate labels once
	for i := range speciesCount {
		labels[i] = generateSpeciesName(i)
	}
	
	b.ResetTimer()
	b.ReportAllocs()
	
	i := 0
	for b.Loop() {
		// Simulate varying confidence values each iteration
		confidence := make([]float32, speciesCount)
		for j := range confidence {
			confidence[j] = float32((i+j)%100) / 100.0
		}
		i++
		
		// Run the memory-intensive pipeline
		results, err := pairLabelsAndConfidence(labels, confidence)
		if err != nil {
			b.Errorf("pairLabelsAndConfidence failed: %v", err)
		}
		
		// Sort and trim as in real usage
		sortResults(results)
		trimResultsToMax(results, 10)
	}
}

// generateSpeciesName creates realistic species names for testing
func generateSpeciesName(index int) string {
	// Generate names like "Species_000001", "Species_000002", etc.
	return fmt.Sprintf("Species_%06d", index+1)
}