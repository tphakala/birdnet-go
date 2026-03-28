//go:build onnx

package onnx

import (
	"fmt"

	ort "github.com/yalue/onnxruntime_go"
)

// Classifier runs bird species classification inference on audio segments.
// It is safe for concurrent use from multiple goroutines.
type Classifier struct {
	session     *ort.DynamicAdvancedSession
	config      ModelConfig
	labels      []string
	topK        int
	minConf     float32
	inputName   string
	outputNames []string
}

// ClassifierOption configures classifier behavior.
type ClassifierOption func(*classifierConfig)

type classifierConfig struct {
	modelType     *ModelType
	labels        []string
	labelsPath    string
	topK          int
	minConf       float32
	sessionOptsFn func(*ort.SessionOptions)
}

func defaultClassifierConfig() *classifierConfig {
	return &classifierConfig{
		topK:    10,
		minConf: 0.0,
	}
}

// WithModelType overrides auto-detection of the model type.
func WithModelType(t ModelType) ClassifierOption {
	return func(c *classifierConfig) { c.modelType = &t }
}

// WithLabels provides species labels directly.
func WithLabels(labels []string) ClassifierOption {
	return func(c *classifierConfig) { c.labels = labels }
}

// WithLabelsPath loads species labels from a file (text, CSV, or JSON).
func WithLabelsPath(path string) ClassifierOption {
	return func(c *classifierConfig) { c.labelsPath = path }
}

// WithTopK sets how many top predictions to return. Default: 10.
func WithTopK(k int) ClassifierOption {
	return func(c *classifierConfig) { c.topK = k }
}

// WithMinConfidence sets the minimum confidence threshold. Default: 0.0.
func WithMinConfidence(threshold float32) ClassifierOption {
	return func(c *classifierConfig) { c.minConf = threshold }
}

// WithSessionOptions provides a callback to configure the ONNX Runtime session options.
// The callback receives the options after defaults (IntraOpNumThreads=1, InterOpNumThreads=1)
// have been set, allowing the caller to override or add execution providers.
func WithSessionOptions(fn func(*ort.SessionOptions)) ClassifierOption {
	return func(c *classifierConfig) { c.sessionOptsFn = fn }
}

// NewClassifier creates a new Classifier from an ONNX model file.
// Model type is auto-detected from tensor shapes unless overridden with WithModelType.
// Labels must be provided via WithLabels or WithLabelsPath.
func NewClassifier(modelPath string, opts ...ClassifierOption) (*Classifier, error) {
	if modelPath == "" {
		return nil, ErrModelPathRequired
	}

	cfg := defaultClassifierConfig()
	for _, opt := range opts {
		opt(cfg)
	}

	// Load model metadata and extract tensor info
	inputNames, inputShapes, outputNames, outputInfos, err := loadModelMetadata(modelPath)
	if err != nil {
		return nil, err
	}

	// Detect or use provided model type
	mt, err := resolveModelType(cfg, inputShapes, len(outputNames))
	if err != nil {
		return nil, err
	}

	// Build model config and load labels
	modelCfg := buildModelConfig(mt, inputShapes[0], len(outputNames))

	labels, err := resolveLabels(cfg)
	if err != nil {
		return nil, err
	}

	if err := validateLabelCount(&modelCfg, outputInfos, len(labels)); err != nil {
		return nil, err
	}

	// Create ONNX session
	session, err := createSession(modelPath, inputNames, outputNames, cfg.sessionOptsFn)
	if err != nil {
		return nil, err
	}

	return &Classifier{
		session:     session,
		config:      modelCfg,
		labels:      labels,
		topK:        cfg.topK,
		minConf:     cfg.minConf,
		inputName:   inputNames[0],
		outputNames: outputNames,
	}, nil
}

// loadModelMetadata reads input/output tensor names and shapes from the model file.
func loadModelMetadata(modelPath string) (
	inputNames []string, inputShapes [][]int64,
	outputNames []string, outputInfos []ort.InputOutputInfo,
	err error,
) {
	inputInfos, outputInfos, err := ort.GetInputOutputInfo(modelPath)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("birdnet: failed to load model metadata: %w", err)
	}

	if len(inputInfos) == 0 {
		return nil, nil, nil, nil, &ModelDetectionError{Reason: "model has no input tensors"}
	}

	inputNames = make([]string, len(inputInfos))
	inputShapes = make([][]int64, len(inputInfos))
	for i := range inputInfos {
		inputNames[i] = inputInfos[i].Name
		inputShapes[i] = inputInfos[i].Dimensions
	}

	outputNames = make([]string, len(outputInfos))
	for i := range outputInfos {
		outputNames[i] = outputInfos[i].Name
	}

	if len(inputShapes[0]) < 2 {
		return nil, nil, nil, nil, &ModelDetectionError{
			Reason: fmt.Sprintf("input shape has %d dimensions, expected at least 2", len(inputShapes[0])),
		}
	}

	return inputNames, inputShapes, outputNames, outputInfos, nil
}

// resolveModelType returns the model type from config or auto-detects from shapes.
func resolveModelType(cfg *classifierConfig, inputShapes [][]int64, numOutputs int) (ModelType, error) {
	if cfg.modelType != nil {
		return *cfg.modelType, nil
	}
	return detectModelTypeFromShapes(inputShapes, numOutputs)
}

// validateLabelCount checks that the label count matches the model's logits output dimension.
func validateLabelCount(modelCfg *ModelConfig, outputInfos []ort.InputOutputInfo, labelCount int) error {
	if modelCfg.LogitsIndex < 0 || modelCfg.LogitsIndex >= len(outputInfos) {
		return fmt.Errorf("birdnet: LogitsIndex %d is out of range for model with %d outputs", modelCfg.LogitsIndex, len(outputInfos))
	}
	logitsDims := outputInfos[modelCfg.LogitsIndex].Dimensions
	if len(logitsDims) < 2 {
		return nil
	}
	logitsSize := int(logitsDims[len(logitsDims)-1])
	if logitsSize > 0 && labelCount != logitsSize {
		return &LabelCountError{Expected: logitsSize, Got: labelCount}
	}
	return nil
}

// createSession builds an ONNX Runtime session with default options.
func createSession(modelPath string, inputNames, outputNames []string, sessionOptsFn func(*ort.SessionOptions)) (*ort.DynamicAdvancedSession, error) {
	sessOpts, err := ort.NewSessionOptions()
	if err != nil {
		return nil, fmt.Errorf("birdnet: failed to create session options: %w", err)
	}
	defer func() { _ = sessOpts.Destroy() }()

	if err := sessOpts.SetIntraOpNumThreads(1); err != nil {
		return nil, fmt.Errorf("birdnet: failed to set intra-op threads: %w", err)
	}
	if err := sessOpts.SetInterOpNumThreads(1); err != nil {
		return nil, fmt.Errorf("birdnet: failed to set inter-op threads: %w", err)
	}

	if sessionOptsFn != nil {
		sessionOptsFn(sessOpts)
	}

	session, err := ort.NewDynamicAdvancedSession(modelPath, inputNames, outputNames, sessOpts)
	if err != nil {
		return nil, fmt.Errorf("birdnet: failed to create ONNX session: %w", err)
	}
	return session, nil
}

func resolveLabels(cfg *classifierConfig) ([]string, error) {
	if len(cfg.labels) > 0 {
		return cfg.labels, nil
	}
	if cfg.labelsPath != "" {
		return loadLabels(cfg.labelsPath)
	}
	return nil, ErrLabelsRequired
}

// Config returns the model configuration.
func (c *Classifier) Config() ModelConfig {
	cfg := c.config
	cfg.InputShape = make([]int64, len(c.config.InputShape))
	copy(cfg.InputShape, c.config.InputShape)
	return cfg
}

// Labels returns a copy of the species labels.
func (c *Classifier) Labels() []string {
	cp := make([]string, len(c.labels))
	copy(cp, c.labels)
	return cp
}

// PredictRaw runs inference and returns raw logits (pre-activation) for a single segment.
// This is used by adapters that handle their own post-processing (e.g., sensitivity-adjusted sigmoid).
func (c *Classifier) PredictRaw(audio []float32) ([]float32, error) {
	if len(audio) != c.config.SampleCount {
		return nil, &InputSizeError{Expected: c.config.SampleCount, Got: len(audio)}
	}

	inputShape := makeBatchInputShape(c.config.InputShape, 1)
	inputTensor, err := ort.NewTensor(ort.NewShape(inputShape...), audio)
	if err != nil {
		return nil, fmt.Errorf("birdnet: failed to create input tensor: %w", err)
	}
	defer func() { _ = inputTensor.Destroy() }()

	outputs, err := c.createOutputTensors(1)
	if err != nil {
		return nil, err
	}
	defer destroyTensors(outputs)

	err = c.session.Run([]ort.Value{inputTensor}, outputs)
	if err != nil {
		return nil, fmt.Errorf("birdnet: inference failed: %w", err)
	}

	// Extract raw logits without applying activation function
	logitsTensor, ok := outputs[c.config.LogitsIndex].(*ort.Tensor[float32])
	if !ok {
		return nil, fmt.Errorf("birdnet: logits tensor has unexpected type")
	}
	allLogits := logitsTensor.GetData()
	numClasses := len(c.labels)
	if numClasses > len(allLogits) {
		return nil, fmt.Errorf("birdnet: logits tensor too small: need %d, have %d", numClasses, len(allLogits))
	}

	logits := make([]float32, numClasses)
	copy(logits, allLogits[:numClasses])
	return logits, nil
}

// Predict runs inference on a single audio segment.
// The audio slice must contain exactly Config().SampleCount float32 samples
// (mono, at the model's sample rate, normalized to [-1.0, 1.0]).
func (c *Classifier) Predict(audio []float32) (*Result, error) {
	if len(audio) != c.config.SampleCount {
		return nil, &InputSizeError{Expected: c.config.SampleCount, Got: len(audio)}
	}

	// Build input shape for batch size 1
	inputShape := makeBatchInputShape(c.config.InputShape, 1)

	// Create input tensor (wraps Go slice, no copy)
	inputTensor, err := ort.NewTensor(ort.NewShape(inputShape...), audio)
	if err != nil {
		return nil, fmt.Errorf("birdnet: failed to create input tensor: %w", err)
	}
	defer func() { _ = inputTensor.Destroy() }()

	// Create output tensors
	outputs, err := c.createOutputTensors(1)
	if err != nil {
		return nil, err
	}
	defer destroyTensors(outputs)

	// Run inference
	err = c.session.Run(
		[]ort.Value{inputTensor},
		outputs,
	)
	if err != nil {
		return nil, fmt.Errorf("birdnet: inference failed: %w", err)
	}

	// Process results
	return c.processOutput(outputs, 0)
}

// PredictBatch runs inference on multiple audio segments in a single batch.
// Each segment must contain exactly Config().SampleCount samples.
func (c *Classifier) PredictBatch(segments [][]float32) ([]*Result, error) {
	if len(segments) == 0 {
		return nil, ErrEmptyBatch
	}

	// Validate all segment sizes
	for i, seg := range segments {
		if len(seg) != c.config.SampleCount {
			return nil, &BatchInputSizeError{Index: i, Expected: c.config.SampleCount, Got: len(seg)}
		}
	}

	batchSize := len(segments)

	// Concatenate into flat slice
	flat := make([]float32, 0, batchSize*c.config.SampleCount)
	for _, seg := range segments {
		flat = append(flat, seg...)
	}

	// Build input shape for batch
	inputShape := makeBatchInputShape(c.config.InputShape, int64(batchSize))

	// Create input tensor
	inputTensor, err := ort.NewTensor(ort.NewShape(inputShape...), flat)
	if err != nil {
		return nil, fmt.Errorf("birdnet: failed to create batch input tensor: %w", err)
	}
	defer func() { _ = inputTensor.Destroy() }()

	// Create output tensors
	outputs, err := c.createOutputTensors(batchSize)
	if err != nil {
		return nil, err
	}
	defer destroyTensors(outputs)

	// Run inference
	err = c.session.Run(
		[]ort.Value{inputTensor},
		outputs,
	)
	if err != nil {
		return nil, fmt.Errorf("birdnet: batch inference failed: %w", err)
	}

	// Process results for each segment in the batch
	results := make([]*Result, batchSize)
	for i := range batchSize {
		results[i], err = c.processOutput(outputs, i)
		if err != nil {
			return nil, fmt.Errorf("birdnet: failed to process batch output %d: %w", i, err)
		}
	}
	return results, nil
}

// Close releases the ONNX session and associated resources.
func (c *Classifier) Close() error {
	if c.session != nil {
		err := c.session.Destroy()
		c.session = nil
		return err
	}
	return nil
}

// makeBatchInputShape replaces the batch dimension (first element) in the input shape.
func makeBatchInputShape(modelShape []int64, batchSize int64) []int64 {
	shape := make([]int64, len(modelShape))
	copy(shape, modelShape)
	shape[0] = batchSize
	return shape
}

// createOutputTensors creates pre-sized output tensors based on model config.
func (c *Classifier) createOutputTensors(batchSize int) ([]ort.Value, error) {
	outputs := make([]ort.Value, c.config.NumOutputs)
	for i := range c.config.NumOutputs {
		shape, err := c.outputShape(i, batchSize)
		if err != nil {
			destroyTensors(outputs)
			return nil, err
		}
		t, err := ort.NewEmptyTensor[float32](ort.NewShape(shape...))
		if err != nil {
			destroyTensors(outputs)
			return nil, fmt.Errorf("birdnet: failed to create output tensor %d: %w", i, err)
		}
		outputs[i] = t
	}
	return outputs, nil
}

// outputShape returns the expected shape for a given output tensor index.
func (c *Classifier) outputShape(outputIdx, batchSize int) ([]int64, error) {
	batch := int64(batchSize)
	switch c.config.Type {
	case BirdNETv24:
		return []int64{batch, int64(len(c.labels))}, nil
	case BirdNETv30:
		switch outputIdx {
		case 0:
			return []int64{batch, int64(c.config.EmbeddingSize)}, nil
		case 1:
			return []int64{batch, int64(len(c.labels))}, nil
		}
	case PerchV2:
		switch outputIdx {
		case 0:
			return []int64{batch, int64(c.config.EmbeddingSize)}, nil
		case 1:
			return []int64{batch, 16, 4, int64(c.config.EmbeddingSize)}, nil
		case 2:
			return []int64{batch, 500, 128}, nil
		case 3:
			return []int64{batch, int64(len(c.labels))}, nil
		}
	}
	return nil, fmt.Errorf("birdnet: unexpected output index %d for model %s", outputIdx, c.config.Type)
}

// processOutput extracts predictions and embeddings from output tensors for a given batch index.
func (c *Classifier) processOutput(outputs []ort.Value, batchIdx int) (*Result, error) {
	logitsTensor, ok := outputs[c.config.LogitsIndex].(*ort.Tensor[float32])
	if !ok {
		return nil, fmt.Errorf("birdnet: logits tensor has unexpected type")
	}
	allLogits := logitsTensor.GetData()
	numClasses := len(c.labels)
	start := batchIdx * numClasses
	end := start + numClasses
	if end > len(allLogits) {
		return nil, fmt.Errorf("birdnet: logits tensor too small for batch index %d", batchIdx)
	}
	logits := allLogits[start:end]

	var scores []float32
	switch c.config.Type {
	case BirdNETv24, BirdNETv30:
		scores = sigmoidSlice(logits)
	case PerchV2:
		scores = softmax(logits)
	}

	var embeddings []float32
	if c.config.EmbeddingIndex >= 0 {
		embTensor, ok := outputs[c.config.EmbeddingIndex].(*ort.Tensor[float32])
		if !ok {
			return nil, fmt.Errorf("birdnet: embedding tensor has unexpected type")
		}
		allEmb := embTensor.GetData()
		embStart := batchIdx * c.config.EmbeddingSize
		embEnd := embStart + c.config.EmbeddingSize
		if embEnd > len(allEmb) {
			return nil, fmt.Errorf("birdnet: embedding tensor too small for batch index %d", batchIdx)
		}
		embeddings = make([]float32, c.config.EmbeddingSize)
		copy(embeddings, allEmb[embStart:embEnd])
	}

	predictions := topK(scores, c.labels, c.topK, c.minConf)

	return &Result{
		ModelType:   c.config.Type,
		Predictions: predictions,
		Embeddings:  embeddings,
		RawScores:   scores,
	}, nil
}

// destroyTensors destroys all non-nil tensors in the slice.
func destroyTensors(tensors []ort.Value) {
	for _, t := range tensors {
		if t != nil {
			_ = t.Destroy()
		}
	}
}
