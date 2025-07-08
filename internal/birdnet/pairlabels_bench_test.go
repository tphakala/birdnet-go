package birdnet

import (
	"testing"
	"unsafe"

	"github.com/tphakala/birdnet-go/internal/datastore"
)

// BenchmarkPairLabelsAndConfidenceAlloc tests different allocation strategies
func BenchmarkPairLabelsAndConfidenceAlloc(b *testing.B) {
	speciesCount := 6522
	labels := make([]string, speciesCount)
	confidence := make([]float32, speciesCount)
	
	for i := 0; i < speciesCount; i++ {
		labels[i] = generateSpeciesName(i)
		confidence[i] = float32(i%100) / 100.0
	}
	
	b.Run("Current", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			results, _ := pairLabelsAndConfidence(labels, confidence)
			_ = results
		}
	})
	
	b.Run("PreAllocExact", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			results := make([]datastore.Results, len(labels))
			for j, label := range labels {
				results[j] = datastore.Results{Species: label, Confidence: confidence[j]}
			}
			_ = results
		}
	})
	
	b.Run("ReuseSlice", func(b *testing.B) {
		b.ReportAllocs()
		results := make([]datastore.Results, len(labels))
		for i := 0; i < b.N; i++ {
			for j, label := range labels {
				results[j] = datastore.Results{Species: label, Confidence: confidence[j]}
			}
			_ = results
		}
	})
}

// BenchmarkResultsStructSize measures the size of the Results struct
func BenchmarkResultsStructSize(b *testing.B) {
	b.Logf("Size of datastore.Results struct: %d bytes", unsafe.Sizeof(datastore.Results{}))
	b.Logf("Size of slice header: %d bytes", unsafe.Sizeof([]datastore.Results{}))
	
	// Calculate expected memory for 6522 results
	count := 6522
	structSize := unsafe.Sizeof(datastore.Results{})
	sliceHeaderSize := unsafe.Sizeof([]datastore.Results{})
	totalExpected := int(structSize)*count + int(sliceHeaderSize)
	b.Logf("Expected memory for %d results: %d bytes", count, totalExpected)
	
	// Measure actual allocation
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		results := make([]datastore.Results, count)
		_ = results
	}
}

// BenchmarkPairLabelsVariations tests different implementation approaches
func BenchmarkPairLabelsVariations(b *testing.B) {
	speciesCount := 6522
	labels := make([]string, speciesCount)
	confidence := make([]float32, speciesCount)
	
	for i := 0; i < speciesCount; i++ {
		labels[i] = generateSpeciesName(i)
		confidence[i] = float32(i%100) / 100.0
	}
	
	b.Run("WithPointers", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			results := make([]*datastore.Results, 0, len(labels))
			for j, label := range labels {
				results = append(results, &datastore.Results{
					Species:    label,
					Confidence: confidence[j],
				})
			}
			_ = results
		}
	})
	
	b.Run("IndexAssignment", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			results := make([]datastore.Results, len(labels))
			for j := range labels {
				results[j].Species = labels[j]
				results[j].Confidence = confidence[j]
			}
			_ = results
		}
	})
}