// birdnet.go BirdNET model specific code
package birdnet

import (
	"archive/zip"
	"bufio"
	"bytes"
	_ "embed" // Embedding data directly into the binary.
	"fmt"
	"log"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/go-tflite"
)

// Embedded TensorFlow Lite model data.
//
//go:embed BirdNET_GLOBAL_6K_V2.4_Model_FP32.tflite
var modelData []byte

// Embedded TensorFlow Lite range filter model data.
//
//go:embed BirdNET_GLOBAL_6K_V2.4_MData_Model_FP16.tflite
var metaModelDataV1 []byte

//go:embed BirdNET_GLOBAL_6K_V2.4_MData_Model_V2_FP16.tflite
var metaModelDataV2 []byte

const modelVersion = "BirdNET GLOBAL 6K V2.4 FP32"

// Embedded labels in zip format.
//
//go:embed labels.zip
var labelsZip []byte

// BirdNET struct represents the BirdNET model with interpreters and configuration.
type BirdNET struct {
	AnalysisInterpreter *tflite.Interpreter
	RangeInterpreter    *tflite.Interpreter
	Labels              []string
	Settings            *conf.Settings
	SpeciesListUpdated  time.Time // Timestamp for the last update of the species list.
	mu                  sync.Mutex
}

// NewBirdNET initializes a new BirdNET instance with given settings.
func NewBirdNET(settings *conf.Settings) (*BirdNET, error) {
	bn := &BirdNET{
		Settings: settings,
	}

	if err := bn.initializeModel(); err != nil {
		return nil, fmt.Errorf("failed to initialize model: %w", err)
	}

	if err := bn.initializeMetaModel(); err != nil {
		return nil, fmt.Errorf("failed to initialize meta model: %w", err)
	}

	if err := bn.loadLabels(); err != nil {
		return nil, fmt.Errorf("failed to load labels: %w", err)
	}

	// Normalize and validate locale setting.
	inputLocale := strings.ToLower(settings.BirdNET.Locale)
	normalizedLocale, err := conf.NormalizeLocale(inputLocale)
	if err != nil {
		return nil, err
	}
	settings.BirdNET.Locale = normalizedLocale

	return bn, nil
}

// initializeModel loads and initializes the primary BirdNET model.
func (bn *BirdNET) initializeModel() error {
	model := tflite.NewModel(modelData)
	if model == nil {
		return fmt.Errorf("cannot load model from embedded data")
	}

	// Determine the number of threads for the interpreter based on settings and system capacity.
	threads := bn.determineThreadCount(bn.Settings.BirdNET.Threads)

	// Configure interpreter options.
	options := tflite.NewInterpreterOptions()
	options.SetNumThread(threads)
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

	fmt.Printf("%s model initialized, using %v threads of available %v CPUs\n", modelVersion, threads, runtime.NumCPU())
	return nil
}

// getMetaModelData returns the appropriate meta model data based on the settings.
func (bn *BirdNET) getMetaModelData() []byte {
	if bn.Settings.BirdNET.RangeFilter.Model == "legacy" {
		log.Printf("Using legacy range filter model")
		return metaModelDataV1
	}
	log.Printf("Using latest range filter model")
	return metaModelDataV2
}

// initializeMetaModel loads and initializes the meta model used for additional analysis.
func (bn *BirdNET) initializeMetaModel() error {
	metaModelData := bn.getMetaModelData()

	model := tflite.NewModel(metaModelData)
	if model == nil {
		return fmt.Errorf("cannot load meta model from embedded data")
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
		return fmt.Errorf("cannot create meta model interpreter")
	}
	if status := bn.RangeInterpreter.AllocateTensors(); status != tflite.OK {
		return fmt.Errorf("tensor allocation failed for meta model")
	}

	return nil
}

// determineThreadCount calculates the appropriate number of threads to use based on settings and system capabilities.
func (bn *BirdNET) determineThreadCount(configuredThreads int) int {
	systemCpuCount := runtime.NumCPU()
	if configuredThreads <= 0 || configuredThreads > systemCpuCount {
		return systemCpuCount
	}
	return configuredThreads
}

// loadLabels extracts and loads labels from the embedded zip file based on the configured locale.
func (bn *BirdNET) loadLabels() error {
	bn.Labels = []string{} // Reset labels.

	reader := bytes.NewReader(labelsZip)
	zipReader, err := zip.NewReader(reader, int64(len(labelsZip)))
	if err != nil {
		return err
	}

	// if locale is not set use english as default
	if bn.Settings.BirdNET.Locale == "" {
		fmt.Println("BirdNET locale not set, using English as default")
		bn.Settings.BirdNET.Locale = "en"
	}

	labelFileName := fmt.Sprintf("labels_%s.txt", bn.Settings.BirdNET.Locale)
	for _, file := range zipReader.File {
		if file.Name == labelFileName {
			return bn.readLabelFile(file)
		}
	}
	return fmt.Errorf("label file '%s' not found in the zip archive", labelFileName)
}

// readLabelFile reads and processes the label file from the zip archive.
func (bn *BirdNET) readLabelFile(file *zip.File) error {
	fileReader, err := file.Open()
	if err != nil {
		return err
	}
	defer fileReader.Close()

	scanner := bufio.NewScanner(fileReader)
	for scanner.Scan() {
		bn.Labels = append(bn.Labels, strings.TrimSpace(scanner.Text()))
	}
	return scanner.Err() // Returns nil if no errors occurred during scanning.
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
