//go:build metrics || debug

// metrics_collector.go - Production metrics collection for tuning thresholds
// This file is only compiled when the 'metrics' or 'debug' build tag is specified.
// To enable metrics collection, build with: go build -tags metrics
package diskmanager

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"time"

	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// ProductionMetrics collects runtime metrics for threshold tuning
type ProductionMetrics struct {
	// Timestamp of collection
	Timestamp time.Time `json:"timestamp"`

	// Directory metrics
	DirectoryPath  string `json:"directory_path"`
	TotalFiles     int    `json:"total_files"`
	AudioFiles     int    `json:"audio_files"`
	ParseErrors    int    `json:"parse_errors"`
	DirectoryDepth int    `json:"directory_depth"`
	UniqueSpecies  int    `json:"unique_species"`

	// Pool metrics
	PoolMetrics PoolMetrics `json:"pool_metrics"`

	// Memory metrics
	MemoryStats runtime.MemStats `json:"memory_stats"`

	// Performance metrics
	ProcessingTime time.Duration `json:"processing_time_ms"`
	FilesPerSecond float64       `json:"files_per_second"`

	// Size distribution
	SliceSizeP50 int `json:"slice_size_p50"`
	SliceSizeP95 int `json:"slice_size_p95"`
	SliceSizeP99 int `json:"slice_size_p99"`
	SliceSizeMax int `json:"slice_size_max"`
}

// CollectProductionMetrics gathers metrics during GetAudioFiles execution
// This should be called periodically in production to understand usage patterns
func CollectProductionMetrics(baseDir string, allowedExts []string, db Interface) (*ProductionMetrics, error) {
	metrics := &ProductionMetrics{
		Timestamp:     time.Now(),
		DirectoryPath: baseDir,
	}

	// Reset pool metrics before collection
	ResetPoolMetrics()

	// Collect initial memory stats
	var memStatsBefore runtime.MemStats
	runtime.ReadMemStats(&memStatsBefore)

	// Time the operation
	startTime := time.Now()

	// Run GetAudioFiles with debug enabled
	files, err := GetAudioFiles(baseDir, allowedExts, db, true)

	processingTime := time.Since(startTime)
	metrics.ProcessingTime = processingTime

	// Collect final memory stats
	var memStatsAfter runtime.MemStats
	runtime.ReadMemStats(&memStatsAfter)
	metrics.MemoryStats = memStatsAfter

	// Collect pool metrics
	metrics.PoolMetrics = GetPoolMetrics()

	// Calculate basic metrics
	if err == nil {
		metrics.AudioFiles = len(files)
		metrics.FilesPerSecond = float64(len(files)) / processingTime.Seconds()

		// Count unique species
		speciesMap := make(map[string]bool)
		for _, file := range files {
			speciesMap[file.Species] = true
		}
		metrics.UniqueSpecies = len(speciesMap)
	}

	// Count total files in directory (including non-audio)
	totalFiles := 0
	maxDepth := 0
	err = filepath.Walk(baseDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err // Continue walking
		}
		if !info.IsDir() {
			totalFiles++
			// Calculate depth
			relPath, _ := filepath.Rel(baseDir, path)
			depth := len(filepath.SplitList(relPath))
			if depth > maxDepth {
				maxDepth = depth
			}
		}
		return nil
	})
	if err == nil {
		metrics.TotalFiles = totalFiles
		metrics.DirectoryDepth = maxDepth
	}

	// Calculate size distribution from pool metrics
	if metrics.PoolMetrics.MaxCapacityObserved > 0 {
		metrics.SliceSizeMax = int(metrics.PoolMetrics.MaxCapacityObserved)
		// Estimate percentiles (would need more sophisticated tracking in production)
		metrics.SliceSizeP50 = metrics.AudioFiles / 2
		metrics.SliceSizeP95 = int(float64(metrics.AudioFiles) * 0.95)
		metrics.SliceSizeP99 = int(float64(metrics.AudioFiles) * 0.99)
	}

	return metrics, nil
}

// SaveMetricsToFile saves metrics to a JSON file for analysis
func SaveMetricsToFile(metrics *ProductionMetrics, outputPath string) error {
	data, err := json.MarshalIndent(metrics, "", "  ")
	if err != nil {
		return errors.New(fmt.Errorf("diskmanager: failed to marshal metrics: %w", err)).
			Component("diskmanager").
			Category(errors.CategoryFileParsing).
			Build()
	}

	err = os.WriteFile(outputPath, data, 0o644)
	if err != nil {
		return errors.New(fmt.Errorf("diskmanager: failed to write metrics file: %w", err)).
			Component("diskmanager").
			Category(errors.CategoryFileIO).
			Context("output_path", outputPath).
			Build()
	}

	return nil
}

// AnalyzeThresholds analyzes collected metrics to suggest optimal thresholds
func AnalyzeThresholds(metricsFiles []string) (*PoolConfig, error) {
	allMetrics := make([]ProductionMetrics, 0, len(metricsFiles))

	// Load all metrics files
	for _, file := range metricsFiles {
		data, err := os.ReadFile(file)
		if err != nil {
			return nil, errors.New(fmt.Errorf("diskmanager: failed to read metrics file %s: %w", file, err)).
				Component("diskmanager").
				Category(errors.CategoryFileIO).
				Build()
		}

		var metrics ProductionMetrics
		if err := json.Unmarshal(data, &metrics); err != nil {
			return nil, errors.New(fmt.Errorf("diskmanager: failed to parse metrics file %s: %w", file, err)).
				Component("diskmanager").
				Category(errors.CategoryFileParsing).
				Build()
		}

		allMetrics = append(allMetrics, metrics)
	}

	if len(allMetrics) == 0 {
		return nil, errors.New(fmt.Errorf("diskmanager: no metrics to analyze")).
			Component("diskmanager").
			Category(errors.CategoryValidation).
			Build()
	}

	// Calculate suggested thresholds
	config := &PoolConfig{}

	// Calculate average and max values
	var totalAudioFiles, maxAudioFiles int
	var totalParseErrors, maxParseErrors int
	var maxCapacity uint64

	for i := range allMetrics {
		m := &allMetrics[i]
		totalAudioFiles += m.AudioFiles
		if m.AudioFiles > maxAudioFiles {
			maxAudioFiles = m.AudioFiles
		}

		totalParseErrors += m.ParseErrors
		if m.ParseErrors > maxParseErrors {
			maxParseErrors = m.ParseErrors
		}

		if m.PoolMetrics.MaxCapacityObserved > maxCapacity {
			maxCapacity = m.PoolMetrics.MaxCapacityObserved
		}
	}

	avgAudioFiles := totalAudioFiles / len(allMetrics)

	// Set InitialCapacity to average with some headroom
	config.InitialCapacity = int(float64(avgAudioFiles) * 1.2)
	if config.InitialCapacity < 100 {
		config.InitialCapacity = 100
	}

	// Set MaxPoolCapacity to 2x the maximum observed
	config.MaxPoolCapacity = int(maxCapacity * 2)
	if config.MaxPoolCapacity < 1000 {
		config.MaxPoolCapacity = 1000
	}

	// Set MaxParseErrors based on observed maximum
	config.MaxParseErrors = maxParseErrors * 2
	if config.MaxParseErrors < 100 {
		config.MaxParseErrors = 100
	}

	log := GetLogger()
	log.Info("Threshold analysis completed",
		logger.Int("samples_analyzed", len(allMetrics)),
		logger.Int("avg_audio_files", avgAudioFiles),
		logger.Int("max_audio_files", maxAudioFiles),
		logger.Uint64("max_capacity_observed", maxCapacity),
		logger.Int("suggested_initial_capacity", config.InitialCapacity),
		logger.Int("suggested_max_pool_capacity", config.MaxPoolCapacity),
		logger.Int("suggested_max_parse_errors", config.MaxParseErrors))

	return config, nil
}

// EnableHeapProfiling starts heap profiling for detailed memory analysis
func EnableHeapProfiling(outputPath string) error {
	f, err := os.Create(outputPath)
	if err != nil {
		return errors.New(fmt.Errorf("diskmanager: failed to create heap profile: %w", err)).
			Component("diskmanager").
			Category(errors.CategoryFileIO).
			Context("output_path", outputPath).
			Build()
	}
	defer func() {
		_ = f.Close()
	}()

	runtime.GC() // Force a garbage collection before profiling
	if err := pprof.WriteHeapProfile(f); err != nil {
		return errors.New(fmt.Errorf("diskmanager: failed to write heap profile: %w", err)).
			Component("diskmanager").
			Category(errors.CategoryFileIO).
			Build()
	}

	GetLogger().Info("Heap profile written",
		logger.String("path", outputPath),
		logger.String("analyze_command", "go tool pprof "+outputPath))
	return nil
}

// CollectAndSaveMetrics is a convenience function to collect and save metrics
// Note: You must provide an actual database interface implementation in production
func CollectAndSaveMetrics(baseDir string, db Interface) error {
	// Use default configuration
	allowedExts := allowedFileTypes

	// Collect metrics
	metrics, err := CollectProductionMetrics(baseDir, allowedExts, db)
	if err != nil {
		return err
	}

	// Generate filename with timestamp
	filename := fmt.Sprintf("diskmanager_metrics_%s.json",
		time.Now().Format("20060102_150405"))

	// Save to file
	if err := SaveMetricsToFile(metrics, filename); err != nil {
		return err
	}

	log := GetLogger()
	log.Info("Metrics saved",
		logger.String("filename", filename),
		logger.String("directory", metrics.DirectoryPath),
		logger.Int("total_files", metrics.TotalFiles),
		logger.Int("audio_files", metrics.AudioFiles),
		logger.Duration("processing_time", metrics.ProcessingTime),
		logger.Float64("files_per_second", metrics.FilesPerSecond),
		logger.Uint64("pool_gets", metrics.PoolMetrics.GetCount),
		logger.Uint64("pool_puts", metrics.PoolMetrics.PutCount),
		logger.Uint64("pool_skips", metrics.PoolMetrics.SkipCount),
		logger.Uint64("max_capacity", metrics.PoolMetrics.MaxCapacityObserved))

	return nil
}
