// birdnet.go BirdNET model specific code
package birdnet

import (
	"bufio"
	"bytes"
	_ "embed" // Embedding data directly into the binary.
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/cpuspec"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/telemetry"
	tflite "github.com/tphakala/go-tflite"
	"github.com/tphakala/go-tflite/delegates/xnnpack"
)

// Default model version for the embedded model
const DefaultModelVersion = "BirdNET_GLOBAL_6K_V2.4"

// Model version string, default is the embedded model version
var modelVersion = "BirdNET GLOBAL 6K V2.4 FP32"

// speciesCacheEntry holds cached species scores for a composite cache key.
// Scores are immutable once stored - callers must not mutate the returned map.
type speciesCacheEntry struct {
	key    string             // Composite cache key: date + rounded lat/lon + model id
	scores map[string]float64 // Species occurrence scores keyed by label
}

// BirdNET struct represents the BirdNET model with interpreters and configuration.
type BirdNET struct {
	AnalysisInterpreter *tflite.Interpreter
	RangeInterpreter    *tflite.Interpreter
	Settings            *conf.Settings
	ModelInfo           ModelInfo           // Information about the current model
	TaxonomyMap         TaxonomyMap         // Mapping of species codes to names and vice versa
	ScientificIndex     ScientificNameIndex // Index for fast scientific name lookups
	TaxonomyPath        string              // Path to custom taxonomy file, if used
	mu                  sync.Mutex
	resultsBuffer       []datastore.Results // Pre-allocated buffer for results to reduce allocations
	confidenceBuffer    []float32           // Pre-allocated buffer for confidence values to reduce allocations

	// Species occurrence cache to avoid repeated GetProbableSpecies calls within same day
	speciesCacheMu sync.RWMutex
	speciesCache   map[string]*speciesCacheEntry

	// Batch inference scheduler (nil if batch size <= 1)
	batchScheduler *BatchScheduler
}

// NewBirdNET initializes a new BirdNET instance with given settings.
func NewBirdNET(settings *conf.Settings) (*BirdNET, error) {
	bn := &BirdNET{
		Settings:     settings,
		TaxonomyPath: "", // Default to embedded taxonomy
		speciesCache: make(map[string]*speciesCacheEntry),
	}

	// Determine model info based on settings
	var modelIdentifier string
	if settings.BirdNET.ModelPath != "" {
		// Use custom model path
		modelIdentifier = settings.BirdNET.ModelPath
	} else {
		// Use default embedded model
		modelIdentifier = DefaultModelVersion
	}

	// Get model info
	var err error
	bn.ModelInfo, err = DetermineModelInfo(modelIdentifier)
	if err != nil {
		return nil, errors.New(fmt.Errorf("BirdNET: failed to determine model information: %w", err)).
			Component("birdnet").
			Category(errors.CategoryModelInit).
			ModelContext(settings.BirdNET.ModelPath, modelIdentifier).
			Context("model_identifier", modelIdentifier).
			Build()
	}

	// Load taxonomy data
	bn.TaxonomyMap, bn.ScientificIndex, err = LoadTaxonomyData(bn.TaxonomyPath)
	if err != nil {
		return nil, errors.New(fmt.Errorf("BirdNET: failed to load taxonomy data: %w", err)).
			Component("birdnet").
			Category(errors.CategoryModelInit).
			Context("taxonomy_path", bn.TaxonomyPath).
			Build()
	}

	if err := bn.initializeModel(); err != nil {
		return nil, errors.New(fmt.Errorf("BirdNET: failed to initialize analysis model: %w", err)).
			Component("birdnet").
			Category(errors.CategoryModelInit).
			ModelContext(settings.BirdNET.ModelPath, modelIdentifier).
			Build()
	}

	if err := bn.initializeMetaModel(); err != nil {
		return nil, errors.New(fmt.Errorf("BirdNET: failed to initialize range filter model: %w", err)).
			Component("birdnet").
			Category(errors.CategoryModelInit).
			ModelContext(settings.BirdNET.ModelPath, modelIdentifier).
			Build()
	}

	if err := bn.loadLabels(); err != nil {
		return nil, errors.New(fmt.Errorf("BirdNET: failed to load species labels: %w", err)).
			Component("birdnet").
			Category(errors.CategoryModelInit).
			ModelContext(settings.BirdNET.ModelPath, modelIdentifier).
			Context("locale", settings.BirdNET.Locale).
			Build()
	}

	// Normalize and validate locale setting.
	inputLocale := strings.ToLower(settings.BirdNET.Locale)
	normalizedLocale, err := conf.NormalizeLocale(inputLocale)
	if err != nil {
		return nil, err
	}
	settings.BirdNET.Locale = normalizedLocale

	// Check if the locale is supported by the model
	if !IsLocaleSupported(&bn.ModelInfo, normalizedLocale) {
		bn.Debug("Warning: Locale '%s' is not officially supported by model '%s'. Using default locale '%s'.",
			normalizedLocale, bn.ModelInfo.ID, bn.ModelInfo.DefaultLocale)
		settings.BirdNET.Locale = bn.ModelInfo.DefaultLocale
	}

	// Validate model and labels, which will also allocate the results buffer
	if err := bn.validateModelAndLabels(); err != nil {
		return nil, errors.New(fmt.Errorf("BirdNET: model validation failed: %w", err)).
			Component("birdnet").
			Category(errors.CategoryModelInit).
			ModelContext(settings.BirdNET.ModelPath, bn.ModelInfo.ID).
			Build()
	}

	// Initialize batch scheduler if batch size > 1
	if settings.BirdNET.BatchSize > 1 {
		bn.batchScheduler = NewBatchScheduler(bn, settings.BirdNET.BatchSize)
	}

	return bn, nil
}

// initializeModel loads and initializes the primary BirdNET model.
func (bn *BirdNET) initializeModel() error {
	start := time.Now()

	modelData, err := bn.loadModel()
	if err != nil {
		return errors.New(err).
			Category(errors.CategoryModelLoad).
			ModelContext(bn.Settings.BirdNET.ModelPath, bn.ModelInfo.ID).
			Timing("model-load", time.Since(start)).
			Build()
	}

	model := tflite.NewModel(modelData)
	if model == nil {
		return errors.New(fmt.Errorf("cannot load TensorFlow Lite model")).
			Category(errors.CategoryModelInit).
			ModelContext(bn.Settings.BirdNET.ModelPath, bn.ModelInfo.ID).
			Context("model_size_mb", len(modelData)/1024/1024).
			Context("use_xnnpack", bn.Settings.BirdNET.UseXNNPACK).
			Timing("model-init", time.Since(start)).
			Build()
	}

	// Determine the number of threads for the interpreter based on settings and system capacity.
	threads := bn.determineThreadCount(bn.Settings.BirdNET.Threads)

	// Configure interpreter options.
	options := tflite.NewInterpreterOptions()

	// Try to use XNNPACK delegate if enabled in settings
	log := GetLogger()
	if bn.Settings.BirdNET.UseXNNPACK {
		delegate := xnnpack.New(xnnpack.DelegateOptions{NumThreads: int32(max(1, threads-1))}) //nolint:gosec // G115: thread count bounded by CPU count, safe conversion
		if delegate == nil {
			log.Warn("Failed to create XNNPACK delegate, falling back to default CPU",
				logger.String("tflite_download", "https://github.com/tphakala/tflite_c/releases/tag/v2.17.1"))
			options.SetNumThread(threads)
		} else {
			options.AddDelegate(delegate)
			options.SetNumThread(1)
		}
	} else {
		options.SetNumThread(threads)
	}

	options.SetErrorReporter(func(msg string, user_data any) {
		GetLogger().Error("TFLite error", logger.String("message", msg))
	}, nil)

	// Create and allocate the TensorFlow Lite interpreter.
	bn.AnalysisInterpreter = tflite.NewInterpreter(model, options)
	if bn.AnalysisInterpreter == nil {
		return fmt.Errorf("cannot create interpreter")
	}
	if status := bn.AnalysisInterpreter.AllocateTensors(); status != tflite.OK {
		return fmt.Errorf("tensor allocation failed")
	}

	// Force garbage collection to reclaim memory from model loading
	// The model data is no longer needed as TFLite has created its own internal copy
	runtime.GC()

	// Update model version based on custom model path if provided
	if bn.Settings.BirdNET.ModelPath != "" {
		// Extract model version from the file name if possible
		fileName := filepath.Base(bn.Settings.BirdNET.ModelPath)
		if strings.HasPrefix(fileName, "BirdNET_") && strings.Contains(fileName, "_Model_") {
			parts := strings.Split(fileName, "_Model_")
			bn.ModelInfo.ID = parts[0]
		} else {
			bn.ModelInfo.ID = "Custom"
		}
		modelVersion = bn.Settings.BirdNET.ModelPath
	}

	// Log model initialization details
	if bn.Settings.BirdNET.Threads == 0 {
		spec := cpuspec.GetCPUSpec()
		if spec.PerformanceCores > 0 {
			log.Info("BirdNET model initialized",
				logger.String("model", modelVersion),
				logger.Int("threads", threads),
				logger.Int("performance_cores", spec.PerformanceCores),
				logger.Int("total_cpus", runtime.NumCPU()))
		} else {
			log.Info("BirdNET model initialized",
				logger.String("model", modelVersion),
				logger.Int("threads", threads),
				logger.Int("total_cpus", runtime.NumCPU()))
		}
	} else {
		log.Info("BirdNET model initialized",
			logger.String("model", modelVersion),
			logger.Int("threads", threads),
			logger.Int("total_cpus", runtime.NumCPU()),
			logger.Bool("threads_configured", true))
	}
	return nil
}

// getMetaModelData returns the appropriate meta model data based on the settings.
func (bn *BirdNET) getMetaModelData() ([]byte, error) {
	// Check if external model path is specified
	if bn.Settings.BirdNET.RangeFilter.ModelPath != "" {
		modelPath := bn.Settings.BirdNET.RangeFilter.ModelPath

		// Expand environment variables first
		modelPath = os.ExpandEnv(modelPath)

		// Then expand ~ to home directory if needed
		if strings.HasPrefix(modelPath, "~/") {
			homeDir, err := os.UserHomeDir()
			if err != nil {
				return nil, errors.New(err).
					Category(errors.CategoryFileIO).
					Context("path", modelPath).
					Build()
			}
			modelPath = filepath.Join(homeDir, modelPath[2:])
		}

		// Load model from external file
		data, err := os.ReadFile(modelPath) //nolint:gosec // G304: modelPath is from application settings
		if err != nil {
			return nil, errors.New(err).
				Category(errors.CategoryFileIO).
				Context("path", modelPath).
				Context("range_filter_model", bn.Settings.BirdNET.RangeFilter.Model).
				Build()
		}

		GetLogger().Info("Loaded range filter model", logger.String("path", modelPath))
		return data, nil
	}

	// No model path specified, try standard paths first (for noembed builds)
	if !hasEmbeddedModels {
		// Determine which model file to look for based on the model version
		modelFileName := DefaultRangeFilterV2ModelName
		if bn.Settings.BirdNET.RangeFilter.Model == "legacy" {
			modelFileName = DefaultRangeFilterV1ModelName
			GetLogger().Warn("Looking for legacy range filter model")
		}

		data, path, err := tryLoadModelFromStandardPaths(modelFileName, "range filter")
		if err != nil {
			// Add extra context to the error
			return nil, errors.Wrap(err).
				Context("range_filter_model", bn.Settings.BirdNET.RangeFilter.Model).
				Build()
		}
		GetLogger().Info("Loaded range filter model from standard path", logger.String("path", path))
		bn.Debug("Loaded range filter model from standard path: %s", path)
		return data, nil
	}

	// Fall back to embedded models
	var data []byte
	if bn.Settings.BirdNET.RangeFilter.Model == "legacy" {
		GetLogger().Warn("Using legacy range filter model")
		data = metaModelDataV1
	} else {
		data = metaModelDataV2
	}

	if data == nil {
		return nil, errors.Newf("range filter model not available: embedded model is nil").
			Category(errors.CategoryModelLoad).
			Context("embedded_models", hasEmbeddedModels).
			Context("range_filter_model", bn.Settings.BirdNET.RangeFilter.Model).
			Build()
	}

	return data, nil
}

// initializeMetaModel loads and initializes the meta model used for range filtering.
func (bn *BirdNET) initializeMetaModel() error {
	start := time.Now()

	metaModelData, err := bn.getMetaModelData()
	if err != nil {
		return err
	}

	model := tflite.NewModel(metaModelData)
	if model == nil {
		return errors.New(fmt.Errorf("cannot load meta model from embedded data")).
			Category(errors.CategoryModelLoad).
			Context("model_type", "range_filter").
			Context("range_filter_model", bn.Settings.BirdNET.RangeFilter.Model).
			Timing("meta-model-load", time.Since(start)).
			Build()
	}

	// Meta model requires only one CPU.
	options := tflite.NewInterpreterOptions()
	options.SetNumThread(1)
	options.SetErrorReporter(func(msg string, user_data any) {
		GetLogger().Error("TFLite meta model error", logger.String("message", msg))
	}, nil)

	// Create and allocate the TensorFlow Lite interpreter for the meta model.
	bn.RangeInterpreter = tflite.NewInterpreter(model, options)
	if bn.RangeInterpreter == nil {
		return errors.New(fmt.Errorf("cannot create meta model interpreter")).
			Category(errors.CategoryModelInit).
			Context("model_type", "range_filter").
			Context("range_filter_model", bn.Settings.BirdNET.RangeFilter.Model).
			Timing("meta-model-init", time.Since(start)).
			Build()
	}
	if status := bn.RangeInterpreter.AllocateTensors(); status != tflite.OK {
		return errors.Newf("tensor allocation failed for meta model: %v", status).
			Category(errors.CategoryModelInit).
			Context("model_type", "range_filter").
			Context("status_code", status).
			Timing("meta-model-allocate", time.Since(start)).
			Build()
	}

	// Force garbage collection to reclaim memory from meta model loading
	// The model data is no longer needed as TFLite has created its own internal copy
	runtime.GC()

	return nil
}

// determineThreadCount calculates the appropriate number of threads to use based on settings and system capabilities.
func (bn *BirdNET) determineThreadCount(configuredThreads int) int {
	systemCpuCount := runtime.NumCPU()

	// If threads are configured to 0, try to get optimal count from cpuspec
	if configuredThreads == 0 {
		spec := cpuspec.GetCPUSpec()
		optimalThreads := spec.GetOptimalThreadCount()
		if optimalThreads > 0 {
			return min(optimalThreads, systemCpuCount)
		}

		// If cpuspec doesn't know the CPU, use all available cores
		return systemCpuCount
	}

	// If threads are configured but exceed system CPU count, limit to system CPU count
	if configuredThreads > systemCpuCount {
		return systemCpuCount
	}

	return configuredThreads
}

// loadLabels extracts and loads labels from either the embedded files or an external file
func (bn *BirdNET) loadLabels() error {
	bn.Settings.BirdNET.Labels = []string{} // Reset labels.

	// Use embedded labels if no external label path is set
	if bn.Settings.BirdNET.LabelPath == "" {
		return bn.loadEmbeddedLabels()
	}

	// Otherwise use external labels
	return bn.loadExternalLabels()
}

// loadEmbeddedLabels loads labels from the embedded label files
func (bn *BirdNET) loadEmbeddedLabels() error {
	// if locale is not set use english as default
	if bn.Settings.BirdNET.Locale == "" {
		GetLogger().Info("BirdNET locale not set, using default",
			logger.String("default_locale", conf.DefaultFallbackLocale))
		bn.Settings.BirdNET.Locale = conf.DefaultFallbackLocale
	}

	// Get the appropriate locale code for the model version
	localeCode := bn.Settings.BirdNET.Locale

	// Use the new detailed loading function
	result := GetLabelFileDataWithResult(bn.ModelInfo.ID, localeCode, bn)
	if result.Error != nil {
		// Create enhanced error for telemetry reporting
		return errors.New(result.Error).
			Category(errors.CategoryLabelLoad).
			Context("requested_locale", localeCode).
			Context("model_version", bn.ModelInfo.ID).
			Context("fallback_locale", conf.DefaultFallbackLocale).
			Build()
	}

	// Check if fallback occurred and report to telemetry
	if result.FallbackOccurred {
		bn.Debug("Label file fallback occurred: requested '%s', using '%s'", result.RequestedLocale, result.ActualLocale)

		// ALWAYS report locale fallback to telemetry as a warning
		// This is critical for tracking configuration issues
		// Use deferred capture since BirdNET initializes before Sentry
		telemetry.CaptureMessageDeferred(
			fmt.Sprintf("Label file fallback: requested locale '%s' not available for model %s, using '%s'",
				result.RequestedLocale, bn.ModelInfo.ID, result.ActualLocale),
			sentry.LevelError,
			"birdnet-label-loading",
		)

		// Also log so users see it immediately
		GetLogger().Warn("Label file locale not available, using fallback",
			logger.String("requested_locale", result.RequestedLocale),
			logger.String("actual_locale", result.ActualLocale))
	}

	data := result.Data

	// Read the labels line by line
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			bn.Settings.BirdNET.Labels = append(bn.Settings.BirdNET.Labels, line)
		}
	}

	if err := scanner.Err(); err != nil {
		return errors.New(err).
			Category(errors.CategoryLabelLoad).
			Context("operation", "scan_labels").
			Context("locale", localeCode).
			Context("model_version", bn.ModelInfo.ID).
			Build()
	}

	// Check and log species missing from taxonomy
	bn.logMissingTaxonomyCodes()

	return nil
}

func (bn *BirdNET) loadExternalLabels() error {
	start := time.Now()

	// Report external label file usage to telemetry
	// Use deferred capture since BirdNET initializes before Sentry
	telemetry.CaptureMessageDeferred(
		fmt.Sprintf("Using external label file: %s", bn.Settings.BirdNET.LabelPath),
		sentry.LevelInfo,
		"birdnet-label-loading",
	)

	file, err := os.Open(bn.Settings.BirdNET.LabelPath)
	if err != nil {
		return errors.New(err).
			Category(errors.CategoryFileIO).
			Context("label_path", bn.Settings.BirdNET.LabelPath).
			Context("operation", "open").
			Timing("label-file-open", time.Since(start)).
			Build()
	}
	defer func() {
		if err := file.Close(); err != nil {
			GetLogger().Warn("Failed to close label file",
				logger.Error(err),
				logger.String("path", bn.Settings.BirdNET.LabelPath))
		}
	}()

	// Read the file directly as a text file
	err = bn.loadLabelsFromText(file)
	if err != nil {
		return errors.New(err).
			Category(errors.CategoryLabelLoad).
			Context("label_path", bn.Settings.BirdNET.LabelPath).
			Context("operation", "parse").
			Timing("label-file-load", time.Since(start)).
			Build()
	}

	// Check and log species missing from taxonomy
	bn.logMissingTaxonomyCodes()

	return nil
}

// logMissingTaxonomyCodes checks labels against the taxonomy map and logs information about missing species
func (bn *BirdNET) logMissingTaxonomyCodes() {
	// Validate labels against taxonomy
	complete, missing := IsTaxonomyComplete(bn.TaxonomyMap, bn.Settings.BirdNET.Labels)
	if !complete {
		// For custom models, provide more detailed information about missing taxonomy codes
		if bn.Settings.BirdNET.ModelPath != "" || bn.Settings.BirdNET.LabelPath != "" {
			bn.Debug("Custom model/labels detected: %d species are missing from the taxonomy data", len(missing))
			bn.Debug("Placeholder taxonomy codes will be generated for these species")
		} else {
			bn.Debug("Warning: %d species are missing from the taxonomy data", len(missing))
		}

		if bn.Settings.BirdNET.Debug {
			for i, species := range missing {
				if i < 10 { // Only show the first 10 to avoid flooding logs
					code := GeneratePlaceholderCode(species)
					scientific, common := SplitSpeciesName(species)
					bn.Debug("Missing taxonomy for '%s' (Sci: '%s', Common: '%s') - using placeholder code: %s",
						species, scientific, common, code)
				} else if i == 10 {
					bn.Debug("... and %d more", len(missing)-10)
					break
				}
			}
		}
	}
}

func (bn *BirdNET) loadLabelsFromText(file *os.File) error {
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		bn.Settings.BirdNET.Labels = append(bn.Settings.BirdNET.Labels, strings.TrimSpace(scanner.Text()))
	}
	return scanner.Err()
}

// clearSpeciesCache clears the species occurrence cache.
// This should be called when model/labels change, node is deleted, or location is updated.
func (bn *BirdNET) clearSpeciesCache() {
	bn.speciesCacheMu.Lock()
	clear(bn.speciesCache)
	bn.speciesCacheMu.Unlock()
}

// getCachedSpeciesScores returns species occurrence scores with caching to avoid repeated calls within same day
func (bn *BirdNET) getCachedSpeciesScores(targetDate time.Time) (map[string]float64, error) {
	// Build composite cache key: date + rounded lat/lon + model
	day := targetDate.Format("2006-01-02")
	cacheKey := fmt.Sprintf("%s|%.4f,%.4f|%s",
		day,
		bn.Settings.BirdNET.Latitude,
		bn.Settings.BirdNET.Longitude,
		bn.Settings.BirdNET.RangeFilter.Model,
	)

	// FAST PATH: read under RLock and return a defensive copy
	bn.speciesCacheMu.RLock()
	if entry, ok := bn.speciesCache[cacheKey]; ok && entry.key == cacheKey {
		out := make(map[string]float64, len(entry.scores))
		maps.Copy(out, entry.scores)
		bn.speciesCacheMu.RUnlock()
		return out, nil
	}
	bn.speciesCacheMu.RUnlock()

	// MISS PATH: fetch outside of any lock to avoid blocking readers
	speciesScores, err := bn.GetProbableSpecies(targetDate, 0.0)
	if err != nil {
		return nil, err
	}
	scores := make(map[string]float64, len(speciesScores))
	for _, s := range speciesScores {
		scores[s.Label] = s.Score
	}

	// WRITE PATH: double-check, evict old entries, and publish new results
	bn.speciesCacheMu.Lock()
	if entry, ok := bn.speciesCache[cacheKey]; ok && entry.key == cacheKey {
		out := make(map[string]float64, len(entry.scores))
		maps.Copy(out, entry.scores)
		bn.speciesCacheMu.Unlock()
		return out, nil
	}
	// Keep cache bounded by clearing before setting new key
	clear(bn.speciesCache)
	bn.speciesCache[cacheKey] = &speciesCacheEntry{
		key:    cacheKey,
		scores: scores,
	}
	out := make(map[string]float64, len(scores))
	maps.Copy(out, scores)
	bn.speciesCacheMu.Unlock()
	return out, nil
}

// Delete releases resources used by the TensorFlow Lite interpreters.
func (bn *BirdNET) Delete() {
	// Stop batch scheduler first
	if bn.batchScheduler != nil {
		bn.batchScheduler.Stop()
	}

	if bn.AnalysisInterpreter != nil {
		bn.AnalysisInterpreter.Delete()
	}
	if bn.RangeInterpreter != nil {
		bn.RangeInterpreter.Delete()
	}
	bn.clearSpeciesCache()
}

// DefaultBirdNETModelName is the expected filesystem basename for the main BirdNET analysis model file.
// This filename is used when searching standard paths for external model files in noembed builds.
const DefaultBirdNETModelName = "BirdNET_GLOBAL_6K_V2.4_Model_FP32.tflite"

// DefaultRangeFilterV1ModelName is the expected filesystem basename for the legacy (v1) range filter model file.
// This filename is used when RangeFilter.Model is set to "legacy" in noembed builds.
const DefaultRangeFilterV1ModelName = "BirdNET_GLOBAL_6K_V2.4_MData_Model_FP16.tflite"

// DefaultRangeFilterV2ModelName is the expected filesystem basename for the default (v2) range filter model file.
// This filename is used when RangeFilter.Model is set to "latest" or unspecified in noembed builds.
const DefaultRangeFilterV2ModelName = "BirdNET_GLOBAL_6K_V2.4_MData_Model_V2_FP16.tflite"

// DefaultModelDirectory is the default directory name where model files are expected to be found.
// This is a relative path that will be resolved against various base paths during model discovery.
// In Docker containers with WORKDIR /data, this resolves to /data/model/.
// Callers can override model locations by setting explicit ModelPath or RangeFilter.ModelPath in configuration.
const DefaultModelDirectory = "model"

// getOSSpecificSystemPaths returns OS-appropriate system installation paths.
func getOSSpecificSystemPaths(modelName string) []string {
	var paths []string

	// Docker container paths (works on all OS in containers)
	paths = append(paths,
		filepath.Join(string(filepath.Separator), "data", DefaultModelDirectory, modelName), // User custom models in /data/model
		filepath.Join(string(filepath.Separator), "models", modelName))                      // Built-in models in /models

	// OS-specific system paths
	switch runtime.GOOS {
	case "windows":
		// Windows system paths using environment variables
		// Use PROGRAMFILES env var, fall back to C:\Program Files if not set
		if programFiles := os.Getenv("PROGRAMFILES"); programFiles != "" {
			paths = append(paths, filepath.Join(programFiles, "BirdNET-Go", DefaultModelDirectory, modelName))
		} else {
			// Fallback to default location if env var not set
			paths = append(paths, filepath.Join("C:", string(filepath.Separator), "Program Files", "BirdNET-Go", DefaultModelDirectory, modelName))
		}

		// Use PROGRAMDATA env var, fall back to C:\ProgramData if not set
		if programData := os.Getenv("PROGRAMDATA"); programData != "" {
			paths = append(paths, filepath.Join(programData, "BirdNET-Go", DefaultModelDirectory, modelName))
		} else {
			// Fallback to default location if env var not set
			paths = append(paths, filepath.Join("C:", string(filepath.Separator), "ProgramData", "BirdNET-Go", DefaultModelDirectory, modelName))
		}

		// Windows user-specific path using LOCALAPPDATA
		if localAppData := os.Getenv("LOCALAPPDATA"); localAppData != "" {
			paths = append(paths, filepath.Join(localAppData, "BirdNET-Go", DefaultModelDirectory, modelName))
		} else if userProfile := os.Getenv("USERPROFILE"); userProfile != "" {
			// Fallback to constructing from USERPROFILE if LOCALAPPDATA not set
			paths = append(paths, filepath.Join(userProfile, "AppData", "Local", "BirdNET-Go", DefaultModelDirectory, modelName))
		}

	case "darwin": // macOS
		// macOS system paths
		paths = append(paths,
			filepath.Join(string(filepath.Separator), "usr", "local", "share", "birdnet-go", DefaultModelDirectory, modelName),
			filepath.Join(string(filepath.Separator), "opt", "birdnet-go", DefaultModelDirectory, modelName),
			filepath.Join(string(filepath.Separator), "Applications", "BirdNET-Go.app", "Contents", "Resources", DefaultModelDirectory, modelName),
		)

		// macOS user-specific path
		if home := os.Getenv("HOME"); home != "" {
			paths = append(paths,
				filepath.Join(home, "Library", "Application Support", "BirdNET-Go", DefaultModelDirectory, modelName),
			)
		}

	default: // Linux and other Unix-like systems
		// Linux/Unix system paths
		paths = append(paths,
			filepath.Join(string(filepath.Separator), "usr", "share", "birdnet-go", DefaultModelDirectory, modelName),
			filepath.Join(string(filepath.Separator), "opt", "birdnet-go", DefaultModelDirectory, modelName),
		)

		// XDG Base Directory specification for user data
		if xdgDataHome := os.Getenv("XDG_DATA_HOME"); xdgDataHome != "" {
			paths = append(paths, filepath.Join(xdgDataHome, "birdnet-go", DefaultModelDirectory, modelName))
		} else if home := os.Getenv("HOME"); home != "" {
			paths = append(paths, filepath.Join(home, ".local", "share", "birdnet-go", DefaultModelDirectory, modelName))
		}
	}

	return paths
}

// tryLoadModelFromStandardPaths attempts to load a model from standard locations.
// It returns the model data, path, and an error if not found.
// The error includes all attempted paths for debugging.
func tryLoadModelFromStandardPaths(modelName, modelType string) (data []byte, path string, err error) {
	// Build candidate paths using filepath.Join for all constructions
	var candidatePaths []string

	// Relative paths (resolved against current working directory)
	candidatePaths = append(candidatePaths,
		filepath.Join(DefaultModelDirectory, modelName),         // model/<name>
		filepath.Join("data", DefaultModelDirectory, modelName), // Legacy: data/model/<name>
	)

	// OS-specific system paths
	candidatePaths = append(candidatePaths, getOSSpecificSystemPaths(modelName)...)

	// Executable-relative paths
	if exePath, execErr := os.Executable(); execErr == nil {
		exeDir := filepath.Dir(exePath)
		candidatePaths = append(candidatePaths,
			filepath.Join(exeDir, DefaultModelDirectory, modelName),                              // <exe-dir>/model/<name>
			filepath.Join(exeDir, "..", DefaultModelDirectory, modelName),                        // <exe-dir>/../model/<name>
			filepath.Join(exeDir, "..", "share", "birdnet-go", DefaultModelDirectory, modelName), // <exe-dir>/../share/birdnet-go/model/<name>
		)
	}

	// Attempt to read from each candidate path directly (no os.Stat to avoid TOCTOU)
	for _, candidatePath := range candidatePaths {
		fileData, readErr := os.ReadFile(candidatePath) //nolint:gosec // G304: candidatePath built from known safe paths
		if readErr == nil {
			// Successfully loaded model
			return fileData, candidatePath, nil
		}
		// Continue trying other paths (collect I/O errors but don't return them individually)
	}

	// No model found in any standard location - build error with context
	return nil, "", errors.Newf("%s model '%s' not found in standard paths (built with noembed tag)", modelType, modelName).
		Category(errors.CategoryModelLoad).
		Context("embedded_models", hasEmbeddedModels).
		Context("model_type", modelType).
		Context("attempted_file", modelName).
		Context("attempted_paths", candidatePaths).
		Build()
}

// loadModel loads either the embedded model or an external model file
func (bn *BirdNET) loadModel() ([]byte, error) {
	start := time.Now()

	// If a specific model path is configured, use it
	if bn.Settings.BirdNET.ModelPath != "" {
		modelPath := bn.Settings.BirdNET.ModelPath
		// Expand environment variables first
		modelPath = os.ExpandEnv(modelPath)

		// Then expand ~ to home directory if needed
		if strings.HasPrefix(modelPath, "~/") {
			homeDir, err := os.UserHomeDir()
			if err != nil {
				return nil, errors.New(err).
					Category(errors.CategoryFileIO).
					Context("path", modelPath).
					Build()
			}
			modelPath = filepath.Join(homeDir, modelPath[2:])
		}

		data, err := os.ReadFile(modelPath) //nolint:gosec // G304: modelPath is from application settings
		if err != nil {
			return nil, errors.New(err).
				Category(errors.CategoryFileIO).
				ModelContext(modelPath, "external").
				Context("operation", "read").
				Timing("model-file-read", time.Since(start)).
				Build()
		}

		bn.Debug("Loaded external model file: %s (size: %d MB)", modelPath, len(data)/1024/1024)
		return data, nil
	}

	// No model path specified, try standard paths first (for noembed builds)
	if !hasEmbeddedModels {
		data, path, err := tryLoadModelFromStandardPaths(DefaultBirdNETModelName, "BirdNET")
		if err != nil {
			return nil, err
		}
		GetLogger().Info("Loaded BirdNET model from standard path", logger.String("path", path))
		bn.Debug("Loaded model from standard path: %s (size: %d MB)", path, len(data)/1024/1024)
		return data, nil
	}

	// Use embedded model if available
	if modelData != nil {
		return modelData, nil
	}

	return nil, errors.Newf("no model available: embedded model is nil").
		Category(errors.CategoryModelLoad).
		Context("embedded_models", hasEmbeddedModels).
		Build()
}

// validateModelAndLabels checks if the number of labels matches the model's output size
func (bn *BirdNET) validateModelAndLabels() error {
	// Get the output tensor to check its dimensions
	outputTensor := bn.AnalysisInterpreter.GetOutputTensor(0)
	if outputTensor == nil {
		return errors.New(fmt.Errorf("cannot get output tensor from model")).
			Category(errors.CategoryValidation).
			ModelContext(bn.Settings.BirdNET.ModelPath, bn.ModelInfo.ID).
			Context("interpreter_status", "failed").
			Build()
	}

	// Get the number of classes from the model's output tensor
	modelOutputSize := outputTensor.Dim(outputTensor.NumDims() - 1)
	labelCount := len(bn.Settings.BirdNET.Labels)

	// Compare with the number of labels
	if labelCount != modelOutputSize {
		return errors.Newf("label count mismatch: model expects %d classes but label file has %d labels",
			modelOutputSize, labelCount).
			Category(errors.CategoryValidation).
			ModelContext(bn.Settings.BirdNET.ModelPath, bn.ModelInfo.ID).
			Context("expected_labels", modelOutputSize).
			Context("actual_labels", labelCount).
			Context("locale", bn.Settings.BirdNET.Locale).
			Context("label_path_type", func() string {
				if bn.Settings.BirdNET.LabelPath == "" {
					return "embedded"
				}
				return "external"
			}()).
			Build()
	}

	// Pre-allocate results buffer with the model's output size
	if bn.resultsBuffer == nil || len(bn.resultsBuffer) != modelOutputSize {
		bn.resultsBuffer = make([]datastore.Results, modelOutputSize)
	}

	// Pre-allocate confidence buffer with the model's output size
	if bn.confidenceBuffer == nil || len(bn.confidenceBuffer) != modelOutputSize {
		bn.confidenceBuffer = make([]float32, modelOutputSize)
	}

	bn.Debug("\033[32m✅ Model validation successful: %d labels match model output size\033[0m", modelOutputSize)
	return nil
}

// ReloadModel safely reloads the BirdNET model and labels while handling ongoing analysis
func (bn *BirdNET) ReloadModel() error {
	bn.Debug("\033[33m🔒 Acquiring mutex for model reload\033[0m")
	bn.mu.Lock()
	defer bn.mu.Unlock()
	bn.Debug("\033[32m✅ Acquired mutex for model reload\033[0m")

	// Store old interpreters to clean up after successful reload
	oldAnalysisInterpreter := bn.AnalysisInterpreter
	oldRangeInterpreter := bn.RangeInterpreter

	// Re-determine model info if using a custom model path
	if bn.Settings.BirdNET.ModelPath != "" {
		var err error
		bn.ModelInfo, err = DetermineModelInfo(bn.Settings.BirdNET.ModelPath)
		if err != nil {
			return fmt.Errorf("\033[31m❌ failed to determine model information: %w\033[0m", err)
		}
	}

	// Reload taxonomy data if needed
	var err error
	bn.TaxonomyMap, bn.ScientificIndex, err = LoadTaxonomyData(bn.TaxonomyPath)
	if err != nil {
		return fmt.Errorf("\033[31m❌ failed to reload taxonomy data: %w\033[0m", err)
	}
	bn.Debug("\033[32m✅ Taxonomy data reloaded successfully\033[0m")

	// Initialize new model
	if err := bn.initializeModel(); err != nil {
		return fmt.Errorf("\033[31m❌ failed to reload model: %w\033[0m", err)
	}
	bn.Debug("\033[32m✅ Model initialized successfully\033[0m")

	// Initialize new meta model
	if err := bn.initializeMetaModel(); err != nil {
		// Clean up the newly created analysis interpreter if meta model fails
		if bn.AnalysisInterpreter != nil {
			bn.AnalysisInterpreter.Delete()
		}
		// Restore the old interpreters
		bn.AnalysisInterpreter = oldAnalysisInterpreter
		bn.RangeInterpreter = oldRangeInterpreter
		return fmt.Errorf("\033[31m❌ failed to reload meta model: %w\033[0m", err)
	}
	bn.Debug("\033[32m✅ Meta model initialized successfully\033[0m")

	// Reload labels
	if err := bn.loadLabels(); err != nil {
		// Clean up the newly created interpreters if label loading fails
		if bn.AnalysisInterpreter != nil {
			bn.AnalysisInterpreter.Delete()
		}
		if bn.RangeInterpreter != nil {
			bn.RangeInterpreter.Delete()
		}
		// Restore the old interpreters
		bn.AnalysisInterpreter = oldAnalysisInterpreter
		bn.RangeInterpreter = oldRangeInterpreter
		return fmt.Errorf("\033[31m❌ failed to reload labels: %w\033[0m", err)
	}
	bn.Debug("\033[32m✅ Labels loaded successfully\033[0m")

	// Validate that the model and labels match
	if err := bn.validateModelAndLabels(); err != nil {
		// Clean up the newly created interpreters if validation fails
		if bn.AnalysisInterpreter != nil {
			bn.AnalysisInterpreter.Delete()
		}
		if bn.RangeInterpreter != nil {
			bn.RangeInterpreter.Delete()
		}
		// Restore the old interpreters
		bn.AnalysisInterpreter = oldAnalysisInterpreter
		bn.RangeInterpreter = oldRangeInterpreter
		return fmt.Errorf("\033[31m❌ model validation failed: %w\033[0m", err)
	}

	// Clean up old interpreters after successful reload
	if oldAnalysisInterpreter != nil {
		oldAnalysisInterpreter.Delete()
	}
	if oldRangeInterpreter != nil {
		oldRangeInterpreter.Delete()
	}

	// Clear species cache as model/labels have changed
	bn.clearSpeciesCache()

	bn.Debug("\033[32m✅ Model reload completed successfully\033[0m")
	return nil
}

// GetSpeciesCode returns the eBird species code for a given label
func (bn *BirdNET) GetSpeciesCode(label string) (string, bool) {
	return GetSpeciesCodeFromName(bn.TaxonomyMap, bn.ScientificIndex, label)
}

// GetSpeciesWithScientificAndCommonName returns the scientific name and common name for a label
func (bn *BirdNET) GetSpeciesWithScientificAndCommonName(label string) (scientific, common string) {
	return SplitSpeciesName(label)
}

// Debug prints debug messages if debug mode is enabled.
// Uses the centralized logger for structured logging.
func (bn *BirdNET) Debug(format string, v ...any) {
	if bn.Settings.BirdNET.Debug {
		GetLogger().Debug(fmt.Sprintf(format, v...))
	}
}

// GetSpeciesOccurrence returns the occurrence probability for a given species based on current location and time
// Returns 0.0 if the species is not found or range filter is not enabled
func (bn *BirdNET) GetSpeciesOccurrence(species string) float64 {
	return bn.GetSpeciesOccurrenceAtTime(species, time.Now())
}

// GetSpeciesOccurrenceAtTime returns the occurrence probability for a species at a specific time
func (bn *BirdNET) GetSpeciesOccurrenceAtTime(species string, detectionTime time.Time) float64 {
	// Fast-path: if range interpreter is not initialized, return 0
	if bn.RangeInterpreter == nil {
		return 0.0
	}

	// If location not set, range filter is not active, return 0
	if bn.Settings.BirdNET.Latitude == 0 && bn.Settings.BirdNET.Longitude == 0 {
		return 0.0
	}

	// Try to get cached scores first
	cachedScores, err := bn.getCachedSpeciesScores(detectionTime)
	if err == nil && len(cachedScores) > 0 {
		if occurrence, found := cachedScores[species]; found {
			// Clamp the score to [0.0, 1.0] range
			if occurrence < 0.0 {
				return 0.0
			}
			if occurrence > 1.0 {
				return 1.0
			}
			return occurrence
		}
	}

	// Fallback to calculating probable species if cache miss
	day := detectionTime.Truncate(24 * time.Hour)
	speciesScores, err := bn.GetProbableSpecies(day, 0.0)
	if err != nil {
		bn.Debug("Error getting probable species for occurrence: %v", err)
		return 0.0
	}

	// Look for the species in the scores
	for _, score := range speciesScores {
		if score.Label == species {
			// Clamp the score to [0.0, 1.0] range
			if score.Score < 0.0 {
				return 0.0
			}
			if score.Score > 1.0 {
				return 1.0
			}
			return score.Score
		}
	}

	// Species not found in range filter results
	return 0.0
}

// EnrichResultWithTaxonomy adds taxonomy information to a detection result
// Returns scientific name, common name, and eBird code if available
func (bn *BirdNET) EnrichResultWithTaxonomy(speciesLabel string) (scientific, common, code string) {
	scientific, common = SplitSpeciesName(speciesLabel)

	// Try to get the eBird code
	code, exists := GetSpeciesCodeFromName(bn.TaxonomyMap, bn.ScientificIndex, speciesLabel)
	if !exists {
		// We got a placeholder code for a species not in our taxonomy
		if bn.Settings.BirdNET.Debug {
			bn.Debug("Species '%s' not found in taxonomy, using generated placeholder code: %s", speciesLabel, code)
		}
	}

	return scientific, common, code
}
