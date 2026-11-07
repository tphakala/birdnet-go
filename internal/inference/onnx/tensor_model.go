package onnx

import (
	"fmt"
	"math"
	"slices"

	ort "github.com/yalue/onnxruntime_go"
)

// dynamicDim is the dimension value ONNX Runtime reports for a symbolic or
// unknown dimension.
const dynamicDim int64 = -1

// pinnedBatchSize is the value a dynamic leading (batch) dimension is pinned to.
const pinnedBatchSize int64 = 1

// TensorInputSizeError reports an input whose element count does not match the
// model's input tensor.
type TensorInputSizeError struct {
	Expected int
	Got      int
}

// Error implements the error interface.
func (e *TensorInputSizeError) Error() string {
	return fmt.Sprintf("birdnet: tensor input has %d elements, expected %d", e.Got, e.Expected)
}

// TensorModelOptions configures NewTensorModel.
type TensorModelOptions struct {
	// SessionOptions, when non-nil, configures the ONNX Runtime session
	// options after the package defaults are applied.
	SessionOptions func(*ort.SessionOptions)
}

// TensorModel runs an ONNX model that takes one float32 tensor of any rank and
// returns raw float32 outputs, with no activation or post-processing. The
// input shape comes from the model metadata; a dynamic leading (batch)
// dimension is pinned to 1. It is safe for concurrent use from multiple
// goroutines, and Close may run concurrently with Run.
type TensorModel struct {
	session      guardedSession
	inputShape   ort.Shape
	inputSize    int
	outputShapes []ort.Shape
	outputSizes  []int
}

// resolveTensorShape pins a dynamic leading dimension to one and returns the
// resolved shape with its element count. Any other dynamic or non-positive
// dimension, an empty shape, or a count that overflows int32 is rejected with
// a ModelDetectionError. The input slice is not modified.
func resolveTensorShape(dims []int64) (shape []int64, elements int, err error) {
	if len(dims) == 0 {
		return nil, 0, &ModelDetectionError{Reason: "tensor has no dimensions"}
	}
	shape = slices.Clone(dims)
	if shape[0] == dynamicDim {
		shape[0] = pinnedBatchSize
	}
	total := int64(1)
	for i, d := range shape {
		if d <= 0 {
			return nil, 0, &ModelDetectionError{Reason: fmt.Sprintf("tensor dimension %d is dynamic or non-positive (%d)", i, d)}
		}
		if total > math.MaxInt32/d {
			return nil, 0, &ModelDetectionError{Reason: "tensor element count is too large"}
		}
		total *= d
	}
	return shape, int(total), nil
}

// requireFloatTensor returns a ModelDetectionError unless info describes a
// float32 tensor. Run binds the input and every output as float32 tensors, so
// any other type would fail on each call instead of at load time.
func requireFloatTensor(info *ort.InputOutputInfo) error {
	if info.OrtValueType != ort.ONNXTypeTensor {
		return &ModelDetectionError{Reason: fmt.Sprintf("value is a %v, expected a tensor", info.OrtValueType)}
	}
	if info.DataType != ort.TensorElementDataTypeFloat {
		return &ModelDetectionError{Reason: fmt.Sprintf("tensor element type is %v, expected float32", info.DataType)}
	}
	return nil
}

// NewTensorModel loads the model at modelPath. It requires exactly one input
// and at least one output, all float32 tensors, and resolves every shape with
// the rules of resolveTensorShape. The ONNX Runtime must be initialized first.
func NewTensorModel(modelPath string, opts TensorModelOptions) (*TensorModel, error) {
	if modelPath == "" {
		return nil, ErrModelPathRequired
	}

	inputInfos, outputInfos, err := ort.GetInputOutputInfo(modelPath)
	if err != nil {
		return nil, fmt.Errorf("birdnet: failed to load tensor model metadata: %w", err)
	}
	if len(inputInfos) != 1 {
		return nil, &ModelDetectionError{Reason: fmt.Sprintf("tensor model must have exactly one input, got %d", len(inputInfos))}
	}
	if len(outputInfos) == 0 {
		return nil, &ModelDetectionError{Reason: "tensor model has no outputs"}
	}

	if err := requireFloatTensor(&inputInfos[0]); err != nil {
		return nil, fmt.Errorf("birdnet: input %q: %w", inputInfos[0].Name, err)
	}
	inputShape, inputSize, err := resolveTensorShape(inputInfos[0].Dimensions)
	if err != nil {
		return nil, fmt.Errorf("birdnet: input %q: %w", inputInfos[0].Name, err)
	}

	outputNames := make([]string, len(outputInfos))
	outputShapes := make([]ort.Shape, len(outputInfos))
	outputSizes := make([]int, len(outputInfos))
	for i := range outputInfos {
		info := &outputInfos[i]
		if err := requireFloatTensor(info); err != nil {
			return nil, fmt.Errorf("birdnet: output %q: %w", info.Name, err)
		}
		shape, size, err := resolveTensorShape(info.Dimensions)
		if err != nil {
			return nil, fmt.Errorf("birdnet: output %q: %w", info.Name, err)
		}
		outputNames[i] = info.Name
		outputShapes[i] = shape
		outputSizes[i] = size
	}

	session, err := createSession(modelPath, []string{inputInfos[0].Name}, outputNames, opts.SessionOptions)
	if err != nil {
		return nil, err
	}

	return &TensorModel{
		session:      guardedSession{s: session},
		inputShape:   inputShape,
		inputSize:    inputSize,
		outputShapes: outputShapes,
		outputSizes:  outputSizes,
	}, nil
}

// Run feeds input to the model and returns one raw float32 slice per bound
// output. The input length must equal the product of InputShape. The returned
// slices are copies owned by the caller.
func (m *TensorModel) Run(input []float32) ([][]float32, error) {
	if m.session.isClosed() {
		return nil, ErrSessionClosed
	}
	if len(input) != m.inputSize {
		return nil, &TensorInputSizeError{Expected: m.inputSize, Got: len(input)}
	}

	inputTensor, err := ort.NewTensor(m.inputShape, input)
	if err != nil {
		return nil, fmt.Errorf("birdnet: failed to create input tensor: %w", err)
	}
	defer func() { _ = inputTensor.Destroy() }()

	outputTensors := make([]*ort.Tensor[float32], len(m.outputShapes))
	outputValues := make([]ort.Value, len(m.outputShapes))
	defer func() {
		for _, t := range outputTensors {
			if t != nil {
				_ = t.Destroy()
			}
		}
	}()
	for i, shape := range m.outputShapes {
		t, err := ort.NewEmptyTensor[float32](shape)
		if err != nil {
			return nil, fmt.Errorf("birdnet: failed to create output tensor %d: %w", i, err)
		}
		outputTensors[i] = t
		outputValues[i] = t
	}

	if err := m.session.run([]ort.Value{inputTensor}, outputValues); err != nil {
		return nil, fmt.Errorf("birdnet: tensor model inference failed: %w", err)
	}

	results := make([][]float32, len(outputTensors))
	for i, t := range outputTensors {
		data := t.GetData()
		results[i] = slices.Clone(data)
	}
	return results, nil
}

// InputShape returns a copy of the resolved input shape, with a dynamic batch
// dimension pinned to 1.
func (m *TensorModel) InputShape() []int64 {
	return slices.Clone(m.inputShape)
}

// OutputSizes returns the element count of each bound output, in binding order.
func (m *TensorModel) OutputSizes() []int {
	return slices.Clone(m.outputSizes)
}

// Close releases the ONNX session. It waits for in-flight runs, is safe to
// call more than once, and every later Run returns ErrSessionClosed.
func (m *TensorModel) Close() error {
	return m.session.close()
}
