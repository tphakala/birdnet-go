package birdnet

import (
	"archive/zip"
	"bufio"
	"bytes"
	_ "embed"
	"fmt"
	"runtime"
	"strings"

	"github.com/tphakala/BirdNET-Go/internal/config"
	"github.com/tphakala/go-tflite"
)

// var interpreter *tflite.Interpreter
//var labels []string

//go:embed BirdNET_GLOBAL_6K_V2.4_Model_FP32.tflite
var modelData []byte

//go:embed BirdNET_GLOBAL_6K_V2.4_MData_Model_FP16.tflite
var metaModelData []byte

const modelVersion = "BirdNET GLOBAL 6K V2.4 FP32"

//go:embed labels.zip
var labelsZip []byte

// Setup initializes and loads the BirdNET model.
// It prints a loading message, initializes the model with embedded data,
// and loads the labels according to the provided locale in the config.
// It returns an error if any step in the initialization process fails.
func Setup(ctx *config.Context) error {
	// Initialize the BirdNET model
	if err := initializeModel(ctx); err != nil {
		// Return an error allowing the caller to handle it.
		return fmt.Errorf("failed to initialize model: %w", err)
	}

	// Initialize the BirdNET meta model
	if err := initializeMetaModel(ctx); err != nil {
		// Return an error allowing the caller to handle it.
		return fmt.Errorf("failed to initialize model: %w", err)
	}

	// Load the labels for the BirdNET model based on the locale specified in the configuration.
	if err := loadLabels(ctx); err != nil {
		// Return an error allowing the caller to handle it.
		return fmt.Errorf("failed to load labels: %w", err)
	}

	// If everything was successful, return nil indicating no error occurred.
	return nil
}

// initializeModel loads the model from embedded data and creates a new interpreter
func initializeModel(ctx *config.Context) error {
	fmt.Print("Loading BirdNET model")
	// Load the TensorFlow Lite model from embedded data
	model := tflite.NewModel(modelData)
	if model == nil {
		return fmt.Errorf("cannot load model from embedded data")
	}

	// Get cpu core count for interpreter options
	cpuCount := runtime.NumCPU()

	// Configure the interpreter options
	options := tflite.NewInterpreterOptions()
	// XNNPACK delegate is commented out for now as interpreter creation fails
	// if it is used
	//options.AddDelegate(xnnpack.New(xnnpack.DelegateOptions{NumThreads: 1}))
	options.SetNumThread(cpuCount)
	options.SetErrorReporter(func(msg string, user_data interface{}) {
		fmt.Println(msg)
	}, nil)

	// Create a new TensorFlow Lite interpreter using the model and options
	ctx.AnalysisInterpreter = tflite.NewInterpreter(model, options)
	if ctx.AnalysisInterpreter == nil {
		return fmt.Errorf("cannot create interpreter")
	}

	// Allocate tensors required for the interpreter
	status := ctx.AnalysisInterpreter.AllocateTensors()
	if status != tflite.OK {
		return fmt.Errorf("tensor allocation failed")
	}

	fmt.Printf(" - %s model initialized\n", modelVersion)
	return nil
}

// initializeModel loads the model from embedded data and creates a new interpreter
func initializeMetaModel(ctx *config.Context) error {
	// Load the TensorFlow Lite model from embedded data
	model := tflite.NewModel(metaModelData)
	if model == nil {
		return fmt.Errorf("cannot load model from embedded data")
	}

	// Single CPU is enough for meta model
	cpuCount := 1

	// Configure the interpreter options
	options := tflite.NewInterpreterOptions()
	options.SetNumThread(cpuCount)
	options.SetErrorReporter(func(msg string, user_data interface{}) {
		fmt.Println(msg)
	}, nil)

	// Create a new TensorFlow Lite interpreter using the model and options
	ctx.FilterInterpreter = tflite.NewInterpreter(model, options)
	if ctx.FilterInterpreter == nil {
		return fmt.Errorf("cannot create interpreter")
	}

	// Allocate tensors required for the interpreter
	status := ctx.FilterInterpreter.AllocateTensors()
	if status != tflite.OK {
		return fmt.Errorf("tensor allocation failed")
	}

	return nil
}

// LoadLabels extracts the specified label file from the embedded zip archive
// and loads the labels into a slice based on the provided locale.
func loadLabels(ctx *config.Context) error {
	// Reset labels slice to ensure it's empty before loading new labels
	ctx.Labels = nil

	// Create a new reader for the embedded labels.zip
	reader := bytes.NewReader(labelsZip)
	r, err := zip.NewReader(reader, int64(len(labelsZip)))
	if err != nil {
		return err
	}

	labelFileName := fmt.Sprintf("labels_%s.txt", ctx.Settings.Locale)

	// Search for the matching labels file in the zip archive
	for _, zipFile := range r.File {
		if zipFile.Name == labelFileName {
			// File found, open it
			rc, err := zipFile.Open()
			if err != nil {
				return err
			}
			defer rc.Close()

			// Read the file content line by line
			scanner := bufio.NewScanner(rc)
			for scanner.Scan() {
				ctx.Labels = append(ctx.Labels, strings.TrimSpace(scanner.Text()))
			}

			// Check for errors from the scanner
			if err := scanner.Err(); err != nil {
				return err
			}

			// Successfully loaded labels
			return nil
		}
	}

	// If the loop completes without returning, the label file was not found
	return fmt.Errorf("label file '%s' not found in the zip archive", labelFileName)
}

// DeleteInterpreter safely removes the current instance of the interpreter
func DeleteInterpreter(ctx *config.Context) {
	ctx.AnalysisInterpreter.Delete()
	ctx.FilterInterpreter.Delete()
}
