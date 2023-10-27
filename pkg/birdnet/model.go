package birdnet

import (
	"bufio"
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/tphakala/go-tflite"
)

var interpreter *tflite.Interpreter
var labels []string

// initializeModel loads the model from the given path and creates a new interpreter
func InitializeModel(modelPath string) error {
	// Load the TensorFlow Lite model from the provided path
	model := tflite.NewModelFromFile(modelPath)
	if model == nil {
		return fmt.Errorf("cannot load model from path: %s", modelPath)
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
	interpreter = tflite.NewInterpreter(model, options)
	if interpreter == nil {
		return fmt.Errorf("cannot create interpreter")
	}

	// Allocate tensors required for the interpreter
	status := interpreter.AllocateTensors()
	if status != tflite.OK {
		return fmt.Errorf("tensor allocation failed")
	}

	return nil
}

// loadLabels loads the labels from the provided file path into a slice and returns them.
// An error is returned if there's an issue reading the file or its content.
func LoadLabels(labelspath string) error {

	// Open the labels file.
	file, err := os.Open(labelspath)
	if err != nil {
		return err
	}
	defer file.Close()

	// Read each line in the file and append to the labels slice.
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		labels = append(labels, strings.TrimSpace(scanner.Text()))
	}

	// Check for errors from the scanner.
	if err := scanner.Err(); err != nil {
		return err
	}

	return nil
}

func DeleteInterpreter() {
	interpreter.Delete()
}
