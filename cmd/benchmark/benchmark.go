package benchmark

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"
	"github.com/tphakala/birdnet-go/internal/birdnet"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// batchSize holds the batch size flag value
var batchSize int

func Command(settings *conf.Settings) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "benchmark",
		Short: "Run BirdNET inference benchmark",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Validate batch size
			if batchSize < 1 || batchSize > 512 {
				return fmt.Errorf("batch size must be between 1 and 512, got %d", batchSize)
			}
			return runBenchmark(settings, batchSize)
		},
	}

	cmd.Flags().IntVarP(&batchSize, "batch", "b", 1, "batch size for inference (1-512)")

	return cmd
}

func runBenchmark(settings *conf.Settings, batch int) error {
	var xnnpackResults, standardResults benchmarkResults

	if batch > 1 {
		fmt.Printf("📦 Batch size: %d samples per inference\n\n", batch)
	}

	// First run with XNNPACK
	fmt.Println("🚀 Testing with XNNPACK delegate:")
	settings.BirdNET.UseXNNPACK = true
	if err := runInferenceBenchmark(settings, &xnnpackResults, batch); err != nil {
		fmt.Printf("❌ XNNPACK benchmark failed: %v\n", err)
	}

	// Then run without XNNPACK
	fmt.Println("\n🐌 Testing standard CPU inference:")
	settings.BirdNET.UseXNNPACK = false
	if err := runInferenceBenchmark(settings, &standardResults, batch); err != nil {
		return fmt.Errorf("❌ standard CPU inference benchmark failed: %w", err)
	}

	// Show detailed performance comparison
	fmt.Printf("\nResults:\n")
	if batch > 1 {
		fmt.Printf("Method         Batch Time    Per-Sample    Throughput\n")
		fmt.Printf("─────────────  ────────────  ────────────  ──────────────────────\n")
	} else {
		fmt.Printf("Method         Inference Time   Throughput\n")
		fmt.Printf("─────────────  ───────────────  ──────────────────────\n")
	}

	// Show Standard results if available
	if standardResults.totalInferences > 0 {
		if batch > 1 {
			fmt.Printf("Standard       %6.1f ms      %6.2f ms      %6.2f samples/sec\n",
				float64(standardResults.avgBatchTime.Milliseconds()),
				standardResults.avgTimePerSample,
				standardResults.samplesPerSecond)
		} else {
			fmt.Printf("Standard       %6.1f ms         %6.2f inferences/sec\n",
				float64(standardResults.avgBatchTime.Milliseconds()),
				standardResults.samplesPerSecond)
		}
	} else {
		fmt.Printf("Standard       ❌ Failed\n")
	}

	// Show XNNPACK results if available
	if xnnpackResults.totalInferences > 0 {
		if batch > 1 {
			fmt.Printf("XNNPACK        %6.1f ms      %6.2f ms      %6.2f samples/sec\n",
				float64(xnnpackResults.avgBatchTime.Milliseconds()),
				xnnpackResults.avgTimePerSample,
				xnnpackResults.samplesPerSecond)
		} else {
			fmt.Printf("XNNPACK        %6.1f ms         %6.2f inferences/sec\n",
				float64(xnnpackResults.avgBatchTime.Milliseconds()),
				xnnpackResults.samplesPerSecond)
		}
	} else {
		fmt.Printf("XNNPACK        ❌ Failed\n")
	}

	if batch > 1 {
		fmt.Printf("─────────────  ────────────  ────────────  ──────────────────────\n")
	} else {
		fmt.Printf("─────────────  ───────────────  ──────────────────────\n")
	}

	// Only show comparison if both tests succeeded
	if xnnpackResults.totalInferences > 0 && standardResults.totalInferences > 0 {
		speedImprovement := (float64(standardResults.avgBatchTime.Milliseconds()) -
			float64(xnnpackResults.avgBatchTime.Milliseconds())) /
			float64(standardResults.avgBatchTime.Milliseconds()) * 100

		fmt.Printf("\n🚀 Speed improvement with XNNPACK: %.1f%%\n", speedImprovement)

		// Add performance assessment based on XNNPACK results (use per-sample time for rating)
		ratingTime := xnnpackResults.avgTimePerSample
		if batch == 1 {
			ratingTime = float64(xnnpackResults.avgBatchTime.Milliseconds())
		}
		rating, description := getPerformanceRating(ratingTime)
		fmt.Printf("System Rating: %s, %s\n", rating, description)
	}

	return nil
}

// benchmarkResults stores benchmark metrics
type benchmarkResults struct {
	totalInferences    int           // number of inference calls (batches if batch > 1)
	totalSamples       int           // total samples processed (totalInferences * batchSize)
	avgBatchTime       time.Duration // average time per inference call
	avgTimePerSample   float64       // average time per sample in ms (avgBatchTime / batchSize)
	samplesPerSecond   float64       // throughput in samples per second
}

func runInferenceBenchmark(settings *conf.Settings, results *benchmarkResults, batch int) error {
	// Initialize BirdNET
	bn, err := birdnet.NewBirdNET(settings)
	if err != nil {
		return fmt.Errorf("failed to initialize BirdNET: %w", err)
	}
	defer bn.Delete()

	// Generate 3 seconds of silent audio (48000 * 3 samples)
	sampleSize := 48000 * 3
	silentChunk := make([]float32, sampleSize)

	// For batch inference, create batch of samples
	var batchSamples [][]float32
	if batch > 1 {
		batchSamples = make([][]float32, batch)
		for i := range batch {
			batchSamples[i] = silentChunk
		}
	}

	// Run for 30 seconds
	duration := 30 * time.Second
	startTime := time.Now()
	var totalInferences int
	var totalDuration time.Duration

	if batch > 1 {
		fmt.Printf("⏳ Running batch benchmark for 30 seconds (batch size: %d)...\n", batch)
	} else {
		fmt.Println("⏳ Running benchmark for 30 seconds...")
	}

	for time.Since(startTime) < duration {
		inferenceStart := time.Now()

		if batch > 1 {
			// Batch inference
			_, err := bn.PredictBatch(batchSamples)
			if err != nil {
				return fmt.Errorf("batch prediction failed: %w", err)
			}
		} else {
			// Single inference
			_, err := bn.Predict([][]float32{silentChunk})
			if err != nil {
				return fmt.Errorf("prediction failed: %w", err)
			}
		}

		inferenceTime := time.Since(inferenceStart)
		totalDuration += inferenceTime
		totalInferences++

		// Update progress display
		if totalInferences%10 == 0 {
			avgTime := totalDuration / time.Duration(totalInferences)
			if batch > 1 {
				avgPerSample := float64(avgTime.Milliseconds()) / float64(batch)
				fmt.Printf("\r🔄 Batches: \033[1;36m%d\033[0m, Batch time: \033[1;33m%dms\033[0m, Per-sample: \033[1;32m%.2fms\033[0m",
					totalInferences, avgTime.Milliseconds(), avgPerSample)
			} else {
				fmt.Printf("\r🔄 Inferences: \033[1;36m%d\033[0m, Average time: \033[1;33m%dms\033[0m",
					totalInferences, avgTime.Milliseconds())
			}
		}
	}
	fmt.Println() // Add newline after progress display

	// Calculate and store results
	results.totalInferences = totalInferences
	results.totalSamples = totalInferences * batch
	results.avgBatchTime = totalDuration / time.Duration(totalInferences)
	results.avgTimePerSample = float64(results.avgBatchTime.Milliseconds()) / float64(batch)
	results.samplesPerSecond = float64(results.totalSamples) / duration.Seconds()

	return nil
}

func getPerformanceRating(inferenceTime float64) (rating, description string) {
	switch {
	case inferenceTime > 3000:
		return "❌ Failed", "System is too slow for BirdNET-Go real-time detection"
	case inferenceTime > 2000:
		return "❌ Very Poor", "System is too slow for reliable operation"
	case inferenceTime > 1000:
		return "⚠️ Poor", "System may struggle with real-time detection"
	case inferenceTime > 500:
		return "👍 Decent", "System should handle real-time detection"
	case inferenceTime > 200:
		return "✨ Good", "System will perform well"
	case inferenceTime > 100:
		return "🌟 Very Good", "System will perform very well"
	case inferenceTime > 20:
		return "🏆 Excellent", "System will perform excellently"
	default:
		return "🚀 Superb", "System will perform exceptionally well"
	}
}
