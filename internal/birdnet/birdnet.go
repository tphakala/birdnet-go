// birdnet.go BirdNET model specific code
package birdnet

import (
	"bufio"
	"bytes"
	_ "embed" // Embedding data directly into the binary.
	"fmt"
	"log"
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
	"github.com/tphakala/birdnet-go/internal/telemetry"
	tflite "github.com/tphakala/go-tflite"
	"github.com/tphakala/go-tflite/delegates/xnnpack"
)

// Default model version for the embedded model
const DefaultModelVersion = "BirdNET_GLOBAL_6K_V2.4"

// Embedded TensorFlow Lite model data.
//
//go:embed data/BirdNET_GLOBAL_6K_V2.4_Model_FP32.tflite
var modelData []byte

// Embedded TensorFlow Lite range filter model data.
//
//go:embed data/BirdNET_GLOBAL_6K_V2.4_MData_Model_FP16.tflite
var metaModelDataV1 []byte

//go:embed data/BirdNET_GLOBAL_6K_V2.4_MData_Model_V2_FP16.tflite
var metaModelDataV2 []byte

// Model version string, default is the embedded model version
var modelVersion = "BirdNET GLOBAL 6K V2.4 FP32"

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
}

// NewBirdNET initializes a new BirdNET instance with given settings.
func NewBirdNET(settings *conf.Settings) (*BirdNET, error) {
	bn := &BirdNET{
		Settings:     settings,
		TaxonomyPath: "", // Default to embedded taxonomy
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
	if bn.Settings.BirdNET.UseXNNPACK {
		delegate := xnnpack.New(xnnpack.DelegateOptions{NumThreads: int32(max(1, threads-1))}) //nolint:gosec // G115: thread count bounded by CPU count, safe conversion
		if delegate == nil {
			fmt.Println("⚠️ Failed to create XNNPACK delegate, falling back to default CPU")
			fmt.Println("Please download updated tensorflow lite C API library from:")
			fmt.Println("https://github.com/tphakala/tflite_c/releases/tag/v2.17.1")
			fmt.Println("and install it to enable use of XNNPACK delegate")
			options.SetNumThread(threads)
		} else {
			options.AddDelegate(delegate)
			options.SetNumThread(1)
		}
	} else {
		options.SetNumThread(threads)
	}

	options.SetErrorReporter(func(msg string, user_data interface{}) {
		fmt.Println(msg)
	}, nil)

	// Create and allocate the TensorFlow Lite interpreter.
	bn.AnalysisInterpreter = tflite.NewInterpreter(model, options)
	if bn.AnalysisInterpreter == nil {
		return fmt.Errorf("cannot create interpreter")
	}
	if status := bn.AnalysisInterpreter.AllocateTensors(); status != tflite.OK {
		return fmt.Errorf("tensor allocation failed")
	}

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

	// Get CPU information for detailed message
	var initMessage string
	if bn.Settings.BirdNET.Threads == 0 {
		spec := cpuspec.GetCPUSpec()
		if spec.PerformanceCores > 0 {
			initMessage = fmt.Sprintf("%s model initialized, optimized to use %v threads on %v P-cores (system has %v total CPUs)",
				modelVersion, threads, spec.PerformanceCores, runtime.NumCPU())
		} else {
			initMessage = fmt.Sprintf("%s model initialized, using %v threads of available %v CPUs",
				modelVersion, threads, runtime.NumCPU())
		}
	} else {
		initMessage = fmt.Sprintf("%s model initialized, using configured %v threads of available %v CPUs",
			modelVersion, threads, runtime.NumCPU())
	}
	fmt.Println(initMessage)
	return nil
}

// getMetaModelData returns the appropriate meta model data based on the settings.
func (bn *BirdNET) getMetaModelData() []byte {
	if bn.Settings.BirdNET.RangeFilter.Model == "legacy" {
		fmt.Printf("⚠️ Using legacy range filter model")
		return metaModelDataV1
	}
	return metaModelDataV2
}

// initializeMetaModel loads and initializes the meta model used for range filtering.
func (bn *BirdNET) initializeMetaModel() error {
	start := time.Now()

	metaModelData := bn.getMetaModelData()

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
	options.SetErrorReporter(func(msg string, user_data interface{}) {
		fmt.Println(msg)
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
		fmt.Printf("BirdNET locale not set, using %s as default\n", conf.DefaultFallbackLocale)
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

		// Also log to console so users see it immediately
		fmt.Printf("⚠️  Label file warning: locale '%s' not available, using '%s' instead\n",
			result.RequestedLocale, result.ActualLocale)
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
			log.Printf("Failed to close label file: %v", err)
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

// Delete releases resources used by the TensorFlow Lite interpreters.
func (bn *BirdNET) Delete() {
	if bn.AnalysisInterpreter != nil {
		bn.AnalysisInterpreter.Delete()
	}
	if bn.RangeInterpreter != nil {
		bn.RangeInterpreter.Delete()
	}
}

// loadModel loads either the embedded model or an external model file
func (bn *BirdNET) loadModel() ([]byte, error) {
	start := time.Now()

	if bn.Settings.BirdNET.ModelPath == "" {
		return modelData, nil
	}

	modelPath := bn.Settings.BirdNET.ModelPath
	data, err := os.ReadFile(modelPath)
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

// Debug prints debug messages if debug mode is enabled
func (bn *BirdNET) Debug(format string, v ...interface{}) {
	if bn.Settings.BirdNET.Debug {
		if len(v) == 0 {
			log.Print("[birdnet] " + format)
		} else {
			log.Printf("[birdnet] "+format, v...)
		}
	}
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
