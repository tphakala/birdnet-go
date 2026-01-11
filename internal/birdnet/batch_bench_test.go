package birdnet

import (
	"sync"
	"testing"

	"github.com/tphakala/birdnet-go/internal/conf"
)

// BenchmarkPredict_Sequential measures single-sample prediction performance (baseline).
func BenchmarkPredict_Sequential(b *testing.B) {
	settings := conf.NewTestSettings().Build()
	bn, err := NewBirdNET(settings)
	if err != nil {
		b.Skipf("Skipping benchmark, BirdNET initialization failed: %v", err)
	}
	defer bn.Delete()

	sample := make([]float32, SampleSize)

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_, err := bn.Predict([][]float32{sample})
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkPredictBatch_4 measures batch inference performance with 4 samples.
func BenchmarkPredictBatch_4(b *testing.B) {
	settings := conf.NewTestSettings().Build()
	bn, err := NewBirdNET(settings)
	if err != nil {
		b.Skipf("Skipping benchmark, BirdNET initialization failed: %v", err)
	}
	defer bn.Delete()

	samples := make([][]float32, 4)
	for i := range samples {
		samples[i] = make([]float32, SampleSize)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_, err := bn.PredictBatch(samples)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkPredictBatch_8 measures batch inference performance with 8 samples.
func BenchmarkPredictBatch_8(b *testing.B) {
	settings := conf.NewTestSettings().Build()
	bn, err := NewBirdNET(settings)
	if err != nil {
		b.Skipf("Skipping benchmark, BirdNET initialization failed: %v", err)
	}
	defer bn.Delete()

	samples := make([][]float32, 8)
	for i := range samples {
		samples[i] = make([]float32, SampleSize)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_, err := bn.PredictBatch(samples)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkScheduler_Throughput measures end-to-end throughput using the batch scheduler API.
func BenchmarkScheduler_Throughput(b *testing.B) {
	settings := conf.NewTestSettings().Build()
	settings.BirdNET.Overlap = 2.0 // Enable batching (batch size = 4)

	bn, err := NewBirdNET(settings)
	if err != nil {
		b.Skipf("Skipping benchmark, BirdNET initialization failed: %v", err)
	}
	defer bn.Delete()

	sample := make([]float32, SampleSize)

	b.ReportAllocs()
	b.ResetTimer()

	var wg sync.WaitGroup
	for b.Loop() {
		wg.Add(1)
		resultChan := make(chan BatchResponse, 1)

		go func() {
			defer wg.Done()
			<-resultChan
		}()

		err := bn.SubmitBatch(BatchRequest{
			Sample:     sample,
			SourceID:   "bench",
			ResultChan: resultChan,
		})
		if err != nil {
			b.Fatal(err)
		}
	}
	wg.Wait()
}
