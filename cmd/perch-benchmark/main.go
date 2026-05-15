// Standalone benchmark tool for comparing Perch v2 ONNX model variants.
// Measures inference latency across different thread configurations and
// optionally verifies output equivalence between models.
//
// Usage:
//
//	go build -o perch-benchmark ./cmd/perch-benchmark
//	./perch-benchmark -labels labels.txt -models model_a.onnx,model_b.onnx
package main

import (
	"flag"
	"fmt"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/tphakala/birdnet-go/internal/inference"
	"github.com/tphakala/birdnet-go/internal/inference/onnx"
	ortlib "github.com/yalue/onnxruntime_go"
)

const (
	perchSampleCount = 160000 // 32kHz * 5s
	defaultWarmup    = 5
	defaultIters     = 30
)

type benchConfig struct {
	modelPaths []string
	labelPath  string
	ortLibPath string
	warmup     int
	iterations int
	threads    []int
	batches    []int
	verify     bool
	audioPath  string
	xnnpack    bool
}

type benchStats struct {
	mean   time.Duration
	median time.Duration
	min    time.Duration
	max    time.Duration
	p95    time.Duration
	stddev time.Duration
	count  int
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	cfg := parseFlags()

	if len(cfg.modelPaths) == 0 {
		return fmt.Errorf("at least one model path required (-models)")
	}
	if cfg.labelPath == "" {
		return fmt.Errorf("label file required (-labels)")
	}

	for _, p := range cfg.modelPaths {
		if _, err := os.Stat(p); err != nil {
			return fmt.Errorf("model file not found: %s", p)
		}
	}
	if _, err := os.Stat(cfg.labelPath); err != nil {
		return fmt.Errorf("label file not found: %s", cfg.labelPath)
	}

	if err := inference.InitONNXRuntime(cfg.ortLibPath); err != nil {
		return fmt.Errorf("failed to init ONNX Runtime: %w", err)
	}
	defer func() { _ = inference.DestroyONNXRuntime() }()

	labels, err := loadLabels(cfg.labelPath)
	if err != nil {
		return fmt.Errorf("failed to load labels: %w", err)
	}
	fmt.Printf("Loaded %d labels from %s\n", len(labels), filepath.Base(cfg.labelPath))

	// Prepare audio input
	audio := prepareAudio(cfg.audioPath)

	// Print system info
	printSystemInfo()
	fmt.Printf("Warmup: %d iterations, Benchmark: %d iterations\n", cfg.warmup, cfg.iterations)
	fmt.Printf("Thread configs: %v\n", cfg.threads)
	fmt.Printf("Batch sizes: %v\n", cfg.batches)
	fmt.Printf("XNNPACK EP: %v\n\n", cfg.xnnpack)

	var allResults []resultEntry

	// Run benchmarks
	for _, modelPath := range cfg.modelPaths {
		modelName := filepath.Base(modelPath)
		fmt.Printf("========================================\n")
		fmt.Printf("Model: %s\n", modelName)
		fmt.Printf("========================================\n")

		for _, threads := range cfg.threads {
			for _, batchSize := range cfg.batches {
				fmt.Printf("\n  Threads: %d, Batch: %d\n", threads, batchSize)

				var stats benchStats
				var err error
				if batchSize <= 1 {
					stats, err = runBenchmark(modelPath, labels, audio, threads, cfg.xnnpack, cfg.warmup, cfg.iterations)
				} else {
					stats, err = runBatchBenchmark(modelPath, labels, audio, threads, batchSize, cfg.xnnpack, cfg.warmup, cfg.iterations)
				}
				if err != nil {
					fmt.Printf("  ERROR: %v\n", err)
					continue
				}

				printBatchStats(stats, batchSize)
				allResults = append(allResults, resultEntry{
					model:   modelName,
					threads: threads,
					batch:   batchSize,
					stats:   stats,
				})
			}
		}
		fmt.Println()
	}

	// Output equivalence verification
	if cfg.verify && len(cfg.modelPaths) >= 2 {
		fmt.Printf("========================================\n")
		fmt.Printf("Output Equivalence Check\n")
		fmt.Printf("========================================\n")
		verifyEquivalence(cfg.modelPaths, labels, audio)
	}

	// Summary comparison table
	if len(allResults) > 1 {
		fmt.Printf("\n========================================\n")
		fmt.Printf("Summary\n")
		fmt.Printf("========================================\n")
		printSummary(allResults)
	}

	return nil
}

func parseFlags() benchConfig {
	var modelsStr, threadsStr, batchStr string

	cfg := benchConfig{}
	flag.StringVar(&modelsStr, "models", "", "comma-separated ONNX model paths")
	flag.StringVar(&cfg.labelPath, "labels", "", "path to labels.txt")
	flag.StringVar(&cfg.ortLibPath, "ort-lib", "", "path to libonnxruntime.so (auto-detect if empty)")
	flag.IntVar(&cfg.warmup, "warmup", defaultWarmup, "warmup iterations per config")
	flag.IntVar(&cfg.iterations, "iters", defaultIters, "benchmark iterations per config")
	flag.StringVar(&threadsStr, "threads", "1,2,4", "comma-separated thread counts to test")
	flag.StringVar(&batchStr, "batch", "1", "comma-separated batch sizes to test")
	flag.BoolVar(&cfg.xnnpack, "xnnpack", false, "use XNNPACK execution provider")
	flag.BoolVar(&cfg.verify, "verify", true, "verify output equivalence between models")
	flag.StringVar(&cfg.audioPath, "audio", "", "path to raw float32 PCM file (random if empty)")
	flag.Parse()

	if modelsStr != "" {
		cfg.modelPaths = strings.Split(modelsStr, ",")
	}
	for _, str := range []struct {
		raw string
		dst *[]int
	}{
		{threadsStr, &cfg.threads},
		{batchStr, &cfg.batches},
	} {
		if str.raw != "" {
			for s := range strings.SplitSeq(str.raw, ",") {
				s = strings.TrimSpace(s)
				var n int
				if _, err := fmt.Sscanf(s, "%d", &n); err == nil && n >= 0 {
					*str.dst = append(*str.dst, n)
				}
			}
		}
	}
	if len(cfg.threads) == 0 {
		cfg.threads = []int{1, 2, 4}
	}
	if len(cfg.batches) == 0 {
		cfg.batches = []int{1}
	}

	return cfg
}

func loadLabels(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var labels []string
	for line := range strings.Lines(string(data)) {
		line = strings.TrimSpace(line)
		if line != "" {
			labels = append(labels, line)
		}
	}
	if len(labels) == 0 {
		return nil, fmt.Errorf("no labels found in %s", path)
	}
	return labels, nil
}

func prepareAudio(audioPath string) []float32 {
	if audioPath != "" {
		data, err := os.ReadFile(audioPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not read audio file %s: %v, using random\n", audioPath, err)
		} else {
			// Interpret as raw float32 PCM
			samples := len(data) / 4
			if samples >= perchSampleCount {
				audio := make([]float32, perchSampleCount)
				for i := range perchSampleCount {
					offset := i * 4
					bits := uint32(data[offset]) | uint32(data[offset+1])<<8 | uint32(data[offset+2])<<16 | uint32(data[offset+3])<<24
					audio[i] = math.Float32frombits(bits)
				}
				fmt.Printf("Using audio from: %s\n", audioPath)
				return audio
			}
			fmt.Fprintf(os.Stderr, "warning: audio file too short (%d samples, need %d), using random\n", samples, perchSampleCount)
		}
	}

	// Generate random audio in [-1.0, 1.0] range
	audio := make([]float32, perchSampleCount)
	for i := range audio {
		audio[i] = rand.Float32()*2 - 1
	}
	fmt.Println("Using random audio input")
	return audio
}

func printSystemInfo() {
	fmt.Printf("\nSystem: %s/%s, %d CPUs\n", runtime.GOOS, runtime.GOARCH, runtime.NumCPU())
}

func runBenchmark(modelPath string, labels []string, audio []float32, threads int, useXNNPACK bool, warmup, iterations int) (benchStats, error) {
	classifier, err := createClassifier(modelPath, labels, threads, useXNNPACK)
	if err != nil {
		return benchStats{}, fmt.Errorf("create classifier: %w", err)
	}
	defer func() { _ = classifier.Close() }()

	// Warmup
	for range warmup {
		if _, err := classifier.PredictRaw(audio); err != nil {
			return benchStats{}, fmt.Errorf("warmup inference failed: %w", err)
		}
	}

	// Timed runs
	durations := make([]time.Duration, iterations)
	for i := range iterations {
		start := time.Now()
		if _, err := classifier.PredictRaw(audio); err != nil {
			return benchStats{}, fmt.Errorf("inference %d failed: %w", i, err)
		}
		durations[i] = time.Since(start)
	}

	return computeStats(durations), nil
}

func runBatchBenchmark(modelPath string, labels []string, audio []float32, threads, batchSize int, useXNNPACK bool, warmup, iterations int) (benchStats, error) {
	classifier, err := createClassifier(modelPath, labels, threads, useXNNPACK)
	if err != nil {
		return benchStats{}, fmt.Errorf("create classifier: %w", err)
	}
	defer func() { _ = classifier.Close() }()

	segments := make([][]float32, batchSize)
	for i := range batchSize {
		segments[i] = make([]float32, len(audio))
		copy(segments[i], audio)
	}

	// Warmup
	for range warmup {
		if _, err := classifier.PredictBatch(segments); err != nil {
			return benchStats{}, fmt.Errorf("batch warmup failed: %w", err)
		}
	}

	// Timed runs
	durations := make([]time.Duration, iterations)
	for i := range iterations {
		start := time.Now()
		if _, err := classifier.PredictBatch(segments); err != nil {
			return benchStats{}, fmt.Errorf("batch inference %d failed: %w", i, err)
		}
		durations[i] = time.Since(start)
	}

	return computeStats(durations), nil
}

func createClassifier(modelPath string, labels []string, threads int, useXNNPACK bool) (*onnx.Classifier, error) {
	opts := make([]onnx.ClassifierOption, 0, 5)
	opts = append(opts,
		onnx.WithLabels(labels),
		onnx.WithTopK(0),
		onnx.WithMinConfidence(0),
		onnx.WithSkipLabelValidation(),
	)
	var configErr error
	opts = append(opts, onnx.WithSessionOptions(func(so *ortlib.SessionOptions) {
		if threads > 0 {
			if err := so.SetIntraOpNumThreads(threads); err != nil && configErr == nil {
				configErr = fmt.Errorf("failed to set IntraOpNumThreads to %d: %w", threads, err)
			}
			if err := so.SetInterOpNumThreads(threads); err != nil && configErr == nil {
				configErr = fmt.Errorf("failed to set InterOpNumThreads to %d: %w", threads, err)
			}
		}
		if useXNNPACK {
			if err := so.AppendExecutionProvider("XNNPACK", map[string]string{}); err != nil && configErr == nil {
				configErr = fmt.Errorf("failed to append XNNPACK EP: %w", err)
			}
		}
	}))
	if configErr != nil {
		return nil, configErr
	}
	return onnx.NewClassifier(modelPath, opts...)
}

func computeStats(durations []time.Duration) benchStats {
	if len(durations) == 0 {
		return benchStats{}
	}

	sorted := make([]time.Duration, len(durations))
	copy(sorted, durations)
	slices.Sort(sorted)

	var sum float64
	for _, d := range durations {
		sum += float64(d)
	}
	mean := sum / float64(len(durations))

	var varianceSum float64
	for _, d := range durations {
		diff := float64(d) - mean
		varianceSum += diff * diff
	}
	stddev := math.Sqrt(varianceSum / float64(len(durations)))

	n := len(sorted)
	p95idx := int(float64(n) * 0.95)
	if p95idx >= n {
		p95idx = n - 1
	}

	return benchStats{
		mean:   time.Duration(mean),
		median: sorted[n/2],
		min:    sorted[0],
		max:    sorted[n-1],
		p95:    sorted[p95idx],
		stddev: time.Duration(stddev),
		count:  n,
	}
}

func printBatchStats(s benchStats, batchSize int) {
	fmt.Printf("  Mean:   %v\n", s.mean.Round(time.Millisecond))
	fmt.Printf("  Median: %v\n", s.median.Round(time.Millisecond))
	fmt.Printf("  Min:    %v\n", s.min.Round(time.Millisecond))
	fmt.Printf("  Max:    %v\n", s.max.Round(time.Millisecond))
	fmt.Printf("  P95:    %v\n", s.p95.Round(time.Millisecond))
	fmt.Printf("  Stddev: %v\n", s.stddev.Round(time.Millisecond))
	batchThroughput := float64(batchSize) / s.mean.Seconds()
	perSegment := s.mean / time.Duration(batchSize)
	fmt.Printf("  Throughput: %.2f segments/sec (%.0fms per segment)\n", batchThroughput, float64(perSegment.Milliseconds()))
}

func verifyEquivalence(modelPaths, labels []string, audio []float32) {
	type modelOutput struct {
		path   string
		logits []float32
	}
	outputs := make([]modelOutput, 0, len(modelPaths))

	for _, path := range modelPaths {
		classifier, err := createClassifier(path, labels, 1, false)
		if err != nil {
			fmt.Printf("  ERROR creating classifier for %s: %v\n", filepath.Base(path), err)
			return
		}
		logits, err := classifier.PredictRaw(audio)
		_ = classifier.Close()
		if err != nil {
			fmt.Printf("  ERROR running inference on %s: %v\n", filepath.Base(path), err)
			return
		}
		outputs = append(outputs, modelOutput{path: path, logits: logits})
	}

	// Compare each pair
	for i := range len(outputs) - 1 {
		for j := i + 1; j < len(outputs); j++ {
			a, b := outputs[i], outputs[j]
			nameA, nameB := filepath.Base(a.path), filepath.Base(b.path)

			if len(a.logits) != len(b.logits) {
				fmt.Printf("\n  %s vs %s: DIFFERENT output sizes (%d vs %d)\n", nameA, nameB, len(a.logits), len(b.logits))
				continue
			}

			var maxDiff, sumDiff float64
			maxDiffIdx := 0
			for k := range a.logits {
				diff := math.Abs(float64(a.logits[k] - b.logits[k]))
				sumDiff += diff
				if diff > maxDiff {
					maxDiff = diff
					maxDiffIdx = k
				}
			}
			meanDiff := sumDiff / float64(len(a.logits))

			fmt.Printf("\n  %s vs %s:\n", nameA, nameB)
			fmt.Printf("    Output size:    %d\n", len(a.logits))
			fmt.Printf("    Max difference: %.7f (at index %d)\n", maxDiff, maxDiffIdx)
			fmt.Printf("    Mean difference: %.7f\n", meanDiff)

			switch {
			case maxDiff < 1e-3:
				fmt.Printf("    Verdict: EQUIVALENT (within 1e-3 tolerance)\n")
			case maxDiff < 1e-2:
				fmt.Printf("    Verdict: CLOSE (within 1e-2 tolerance)\n")
			default:
				fmt.Printf("    Verdict: DIVERGENT (max diff exceeds 1e-2)\n")
			}
		}
	}
}

type resultEntry struct {
	model   string
	threads int
	batch   int
	stats   benchStats
}

func printSummary(results []resultEntry) {
	fmt.Printf("\n  %-25s  Threads  Batch  Mean         Median       P95          Seg/s    ms/seg\n", "Model")
	fmt.Printf("  %-25s  -------  -----  -----------  -----------  -----------  -------  ------\n", strings.Repeat("-", 25))

	for _, r := range results {
		segPerSec := float64(r.batch) / r.stats.mean.Seconds()
		msPerSeg := float64(r.stats.mean.Milliseconds()) / float64(r.batch)
		fmt.Printf("  %-25s  %5d    %3d    %10v   %10v   %10v   %5.2f    %5.0f\n",
			r.model, r.threads, r.batch,
			r.stats.mean.Round(time.Millisecond),
			r.stats.median.Round(time.Millisecond),
			r.stats.p95.Round(time.Millisecond),
			segPerSec, msPerSeg,
		)
	}

	// Find best throughput (segments per second)
	if len(results) > 0 {
		best := results[0]
		bestThroughput := float64(best.batch) / best.stats.mean.Seconds()
		for _, r := range results[1:] {
			t := float64(r.batch) / r.stats.mean.Seconds()
			if t > bestThroughput {
				best = r
				bestThroughput = t
			}
		}
		fmt.Printf("\n  Best throughput: %s @ %d threads, batch %d (%.2f seg/s, %.0fms/seg)\n",
			best.model, best.threads, best.batch, bestThroughput,
			float64(best.stats.mean.Milliseconds())/float64(best.batch))
	}
}
