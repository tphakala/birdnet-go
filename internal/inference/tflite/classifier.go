package tflite

import (
	"fmt"
	"runtime"

	"github.com/tphakala/birdnet-go/internal/inference"
	tflitelib "github.com/tphakala/go-tflite"
	"github.com/tphakala/go-tflite/delegates/xnnpack"
)

// LogFunc is a callback for logging messages from the inference backend.
type LogFunc func(msg string)

// TFLiteClassifierOptions configures the TFLite species classifier.
type TFLiteClassifierOptions struct {
	// Threads is the number of CPU threads for inference. 0 = auto-detect.
	Threads int
	// UseXNNPACK enables the XNNPACK delegate for accelerated inference.
	UseXNNPACK bool
	// ErrorFunc is called for TFLite runtime error messages. If nil, errors are discarded.
	ErrorFunc LogFunc
	// WarnFunc is called for warning messages (e.g., XNNPACK unavailable). If nil, warnings are discarded.
	WarnFunc LogFunc
}

// tfliteClassifier implements Classifier using a TensorFlow Lite interpreter.
type tfliteClassifier struct {
	interpreter *tflitelib.Interpreter
	numSpecies  int
}

// NewTFLiteClassifier creates a Classifier backed by a TensorFlow Lite model.
// The modelData is consumed during initialization and can be freed afterward.
// Returns the classifier and the resolved thread count (for logging by callers).
func NewTFLiteClassifier(modelData []byte, opts TFLiteClassifierOptions) (inference.Classifier, int, error) {
	model := tflitelib.NewModel(modelData)
	if model == nil {
		return nil, 0, fmt.Errorf("cannot create TFLite model from data (%d bytes)", len(modelData))
	}

	threads := determineThreadCount(opts.Threads)

	options := tflitelib.NewInterpreterOptions()

	if opts.UseXNNPACK {
		delegate := xnnpack.New(xnnpack.DelegateOptions{NumThreads: int32(max(1, threads-1))}) //nolint:gosec // G115: thread count bounded by CPU count, safe conversion
		if delegate == nil {
			if opts.WarnFunc != nil {
				opts.WarnFunc("Failed to create XNNPACK delegate, falling back to default CPU")
			}
			options.SetNumThread(threads)
		} else {
			options.AddDelegate(delegate)
			options.SetNumThread(1)
		}
	} else {
		options.SetNumThread(threads)
	}

	options.SetErrorReporter(func(msg string, _ any) {
		if opts.ErrorFunc != nil {
			opts.ErrorFunc(msg)
		}
	}, nil)

	interpreter := tflitelib.NewInterpreter(model, options)
	if interpreter == nil {
		return nil, 0, fmt.Errorf("cannot create TFLite interpreter")
	}

	if status := interpreter.AllocateTensors(); status != tflitelib.OK {
		return nil, 0, fmt.Errorf("TFLite tensor allocation failed")
	}

	// Cache the number of species from the output tensor shape
	outputTensor := interpreter.GetOutputTensor(0)
	if outputTensor == nil {
		return nil, 0, fmt.Errorf("cannot get output tensor from TFLite model")
	}
	numSpecies := outputTensor.Dim(outputTensor.NumDims() - 1)

	// Model data is copied internally by TFLite; allow GC to reclaim it
	runtime.GC()

	return &tfliteClassifier{
		interpreter: interpreter,
		numSpecies:  numSpecies,
	}, threads, nil
}

// Predict runs species classification, returning raw logits (pre-activation).
func (c *tfliteClassifier) Predict(samples []float32) ([]float32, error) {
	inputTensor := c.interpreter.GetInputTensor(0)
	if inputTensor == nil {
		return nil, fmt.Errorf("cannot get input tensor")
	}

	inputSlice := inputTensor.Float32s()
	if len(samples) != len(inputSlice) {
		return nil, fmt.Errorf("input size mismatch: expected %d samples, got %d", len(inputSlice), len(samples))
	}
	copy(inputSlice, samples)

	if status := c.interpreter.Invoke(); status != tflitelib.OK {
		return nil, fmt.Errorf("TFLite invoke failed: %v", status)
	}

	outputTensor := c.interpreter.GetOutputTensor(0)
	if outputTensor == nil {
		return nil, fmt.Errorf("cannot get output tensor")
	}
	predictions := make([]float32, c.numSpecies)
	copy(predictions, outputTensor.Float32s()[:c.numSpecies])

	return predictions, nil
}

// NumSpecies returns the number of species in the model output.
func (c *tfliteClassifier) NumSpecies() int {
	return c.numSpecies
}

// Close releases the TFLite interpreter resources.
func (c *tfliteClassifier) Close() {
	c.interpreter = nil
}
