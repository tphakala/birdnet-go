package tflite

import (
	"fmt"
	"runtime"

	"github.com/tphakala/birdnet-go/internal/inference"
	tflitelib "github.com/tphakala/go-tflite"
)

// tfliteRangeFilter implements RangeFilter using a TensorFlow Lite interpreter.
type tfliteRangeFilter struct {
	interpreter *tflitelib.Interpreter
	numSpecies  int
}

// NewTFLiteRangeFilter creates a RangeFilter backed by a TensorFlow Lite meta model.
// The meta model predicts species occurrence probabilities for a geographic location and date.
// The errorFunc callback, if non-nil, receives TFLite runtime error messages.
func NewTFLiteRangeFilter(modelData []byte, errorFunc LogFunc) (inference.RangeFilter, error) {
	model := tflitelib.NewModel(modelData)
	if model == nil {
		return nil, fmt.Errorf("cannot create TFLite range filter model from data (%d bytes)", len(modelData))
	}

	// Meta model requires only one thread
	options := tflitelib.NewInterpreterOptions()
	options.SetNumThread(1)
	options.SetErrorReporter(func(msg string, _ any) {
		if errorFunc != nil {
			errorFunc(msg)
		}
	}, nil)

	interpreter := tflitelib.NewInterpreter(model, options)
	if interpreter == nil {
		return nil, fmt.Errorf("cannot create TFLite range filter interpreter")
	}

	if status := interpreter.AllocateTensors(); status != tflitelib.OK {
		return nil, fmt.Errorf("TFLite range filter tensor allocation failed: %v", status)
	}

	// Cache output size
	outputTensor := interpreter.GetOutputTensor(0)
	if outputTensor == nil {
		return nil, fmt.Errorf("cannot get output tensor from TFLite range filter model")
	}
	numSpecies := outputTensor.Dim(outputTensor.NumDims() - 1)

	// Model data is copied internally; allow GC to reclaim
	runtime.GC()

	return &tfliteRangeFilter{
		interpreter: interpreter,
		numSpecies:  numSpecies,
	}, nil
}

// rangeFilterInputSize is the number of input values expected by the range filter model
// (latitude, longitude, week).
const rangeFilterInputSize = 3

// Predict returns species occurrence scores for a geographic location and week.
func (r *tfliteRangeFilter) Predict(latitude, longitude, week float32) ([]float32, error) {
	input := r.interpreter.GetInputTensor(0)
	if input == nil {
		return nil, fmt.Errorf("cannot get range filter input tensor")
	}

	float32s := input.Float32s()
	if len(float32s) < rangeFilterInputSize {
		return nil, fmt.Errorf("range filter input tensor too small: need %d, have %d", rangeFilterInputSize, len(float32s))
	}

	float32s[0] = latitude
	float32s[1] = longitude
	float32s[2] = week

	if status := r.interpreter.Invoke(); status != tflitelib.OK {
		return nil, fmt.Errorf("TFLite range filter invoke failed: %v", status)
	}

	output := r.interpreter.GetOutputTensor(0)
	if output == nil {
		return nil, fmt.Errorf("cannot get range filter output tensor")
	}
	scores := make([]float32, r.numSpecies)
	copy(scores, output.Float32s()[:r.numSpecies])

	return scores, nil
}

// NumSpecies returns the number of species in the range filter model output.
func (r *tfliteRangeFilter) NumSpecies() int {
	return r.numSpecies
}

// Close releases the TFLite range filter interpreter resources.
func (r *tfliteRangeFilter) Close() {
	r.interpreter = nil
}
