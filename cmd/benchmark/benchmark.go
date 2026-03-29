package benchmark

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"
	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/conf"
)

func Command(settings *conf.Settings) *cobra.Command {
	return &cobra.Command{
		Use:   "benchmark",
		Short: "Run BirdNET inference benchmark",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runBenchmark(settings)
		},
	}
}

func runBenchmark(settings *conf.Settings) error {
	var xnnpackResults, standardResults benchmarkResults

	// First run with XNNPACK
	fmt.Println("🚀 Testing with XNNPACK delegate:")
	settings.BirdNET.UseXNNPACK = true
	if err := runInferenceBenchmark(settings, &xnnpackResults); err != nil {
		fmt.Printf("❌ XNNPACK benchmark failed: %v\n", err)
	}

	// Then run without XNNPACK
	fmt.Println("\n🐌 Testing standard CPU inference:")
	settings.BirdNET.UseXNNPACK = false
	if err := runInferenceBenchmark(settings, &standardResults); err != nil {
		return fmt.Errorf("❌ standard CPU inference benchmark failed: %w", err)
	}

	// Show detailed performance comparison
	fmt.Printf("Results:\n")
	fmt.Printf("Method         Inference Time   Throughput\n")
	fmt.Printf("─────────────  ───────────────  ──────────────────────\n")

	// Show Standard results if available
	if standardResults.totalInferences > 0 {
		fmt.Printf("Standard       %6.1f ms         %6.2f inferences/sec\n",
			float64(standardResults.avgTime.Milliseconds()),
			standardResults.inferencesPerSecond)
	} else {
		fmt.Printf("Standard       ❌ Failed\n")
	}

	// Show XNNPACK results if available
	if xnnpackResults.totalInferences > 0 {
		fmt.Printf("XNNPACK        %6.1f ms         %6.2f inferences/sec\n",
			float64(xnnpackResults.avgTime.Milliseconds()),
			xnnpackResults.inferencesPerSecond)
	} else {
		fmt.Printf("XNNPACK        ❌ Failed\n")
	}
	fmt.Printf("─────────────  ───────────────  ──────────────────────\n")

	// Only show comparison if both tests succeeded
	if xnnpackResults.totalInferences > 0 && standardResults.totalInferences > 0 {
		speedImprovement := (float64(standardResults.avgTime.Milliseconds()) -
			float64(xnnpackResults.avgTime.Milliseconds())) /
			float64(standardResults.avgTime.Milliseconds()) * 100

		fmt.Printf("\n🚀 Speed improvement with XNNPACK: %.1f%%\n", speedImprovement)

		// Add performance assessment based on XNNPACK results
		rating, description := getPerformanceRating(float64(xnnpackResults.avgTime.Milliseconds()))
		fmt.Printf("System Rating: %s, %s\n", rating, description)
	}

	return nil
}

// Add this struct to store benchmark results
type benchmarkResults struct {
	totalInferences     int
	avgTime             time.Duration
	inferencesPerSecond float64
}

func runInferenceBenchmark(settings *conf.Settings, results *benchmarkResults) error {
	// Initialize BirdNET
	bn, err := classifier.NewBirdNET(settings)
	if err != nil {
		return fmt.Errorf("failed to initialize BirdNET: %w", err)
	}
	defer bn.Delete()

	// Generate 3 seconds of silent audio (48000 * 3 samples)
	sampleSize := 48000 * 3
	silentChunk := make([]float32, sampleSize)

	// Run for 30 seconds
	duration := 30 * time.Second
	startTime := time.Now()
	var totalInferences int
	var totalDuration time.Duration

	fmt.Println("⏳ Running benchmark for 30 seconds...")

	for time.Since(startTime) < duration {
		inferenceStart := time.Now()
		_, err := bn.Predict([][]float32{silentChunk})
		if err != nil {
			return fmt.Errorf("prediction failed: %w", err)
		}
		inferenceTime := time.Since(inferenceStart)
		totalDuration += inferenceTime
		totalInferences++

		// Update progress display
		if totalInferences%10 == 0 {
			avgTime := totalDuration / time.Duration(totalInferences)
			fmt.Printf("\r🔄 Inferences: \033[1;36m%d\033[0m, Average time: \033[1;33m%dms\033[0m",
				totalInferences, avgTime.Milliseconds())
		}
	}
	fmt.Println() // Add newline after progress display

	// Calculate and store results
	results.totalInferences = totalInferences
	results.avgTime = totalDuration / time.Duration(totalInferences)
	results.inferencesPerSecond = float64(totalInferences) / duration.Seconds()

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
