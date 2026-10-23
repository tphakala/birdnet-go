package onnx

import (
	"context"
	"fmt"
	"slices"

	ort "github.com/microsoft/onnxruntime/go/onnxruntime"
)

// Classifier runs bird species classification inference on audio segments.
// It is safe for concurrent use from multiple goroutines.
type Classifier struct {
	session     *ort.Session
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
	modelType           *ModelType
	labels              []string
	labelsPath          string
	topK                int
	minConf             float32
	sessionOptsFn       func(*SessionOptions)
	skipLabelValidation bool
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

// WithSkipLabelValidation disables the label-count-vs-model-output check.
// Use when the model is loaded only for embedding extraction and the caller's
// label list may not match the model's logits output dimension.
func WithSkipLabelValidation() ClassifierOption {
	return func(c *classifierConfig) { c.skipLabelValidation = true }
}

// WithSessionOptions provides a callback to configure the ONNX Runtime session options.
// The callback receives the options after defaults (IntraOpNumThreads=1, InterOpNumThreads=1,
// thread-pool spinning disabled) have been set, allowing the caller to override or add
// execution providers.
func WithSessionOptions(fn func(*SessionOptions)) ClassifierOption {
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

	// Create the ONNX session first: it carries the model's tensor metadata,
	// so the model is loaded once. Every later error closes it again.
	session, err := createSession(modelPath, cfg.sessionOptsFn)
	if err != nil {
		return nil, err
	}

	classifier, err := newClassifierFromSession(session, cfg)
	if err != nil {
		_ = session.Close()
		return nil, err
	}
	return classifier, nil
}

// newClassifierFromSession derives the model configuration and labels for an
// open session. It does not close the session on error; the caller does.
func newClassifierFromSession(session *ort.Session, cfg *classifierConfig) (*Classifier, error) {
	outputInfos := session.Outputs()
	inputNames, inputShapes, outputNames, err := modelMetadata(session.Inputs(), outputInfos)
	if err != nil {
		return nil, err
	}

	// Detect or use provided model type
	mt, err := resolveModelType(cfg, inputShapes, len(outputNames))
	if err != nil {
		return nil, err
	}

	// Build model config and load labels
	outputShapes := make([][]int64, len(outputInfos))
	for i := range outputInfos {
		outputShapes[i] = outputInfos[i].Shape
	}
	modelCfg := buildModelConfig(mt, inputShapes[0], outputShapes)

	labels, err := resolveLabels(cfg)
	if err != nil {
		return nil, err
	}

	if !cfg.skipLabelValidation {
		if err := validateLabelCount(&modelCfg, outputInfos, len(labels)); err != nil {
			return nil, err
		}
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

// modelMetadata extracts input/output tensor names and shapes from the
// metadata of an open session.
func modelMetadata(inputInfos, outputInfos []ort.IOInfo) (
	inputNames []string, inputShapes [][]int64,
	outputNames []string, err error,
) {
	if len(inputInfos) == 0 {
		return nil, nil, nil, &ModelDetectionError{Reason: "model has no input tensors"}
	}

	inputNames = make([]string, len(inputInfos))
	inputShapes = make([][]int64, len(inputInfos))
	for i := range inputInfos {
		inputNames[i] = inputInfos[i].Name
		inputShapes[i] = inputInfos[i].Shape
	}

	outputNames = make([]string, len(outputInfos))
	for i := range outputInfos {
		outputNames[i] = outputInfos[i].Name
	}

	if len(inputShapes[0]) < 2 {
		return nil, nil, nil, &ModelDetectionError{
			Reason: fmt.Sprintf("input shape has %d dimensions, expected at least 2", len(inputShapes[0])),
		}
	}

	return inputNames, inputShapes, outputNames, nil
}

// resolveModelType returns the model type from config or auto-detects from shapes.
func resolveModelType(cfg *classifierConfig, inputShapes [][]int64, numOutputs int) (ModelType, error) {
	if cfg.modelType != nil {
		return *cfg.modelType, nil
	}
	return detectModelTypeFromShapes(inputShapes, numOutputs)
}

// validateLabelCount checks that the label count matches the model's logits output dimension.
func validateLabelCount(modelCfg *ModelConfig, outputInfos []ort.IOInfo, labelCount int) error {
	// An embedding-only model (LogitsIndex < 0) produces no logits, so it cannot serve
	// the provided species labels. Reject it here so a model misconfigured as a
	// label-validated classifier fails at construction with a clear message rather than
	// only later at prediction time. Embedding-only consumers (the bat pipeline) load
	// the model with WithSkipLabelValidation, which bypasses this check entirely.
	if modelCfg.LogitsIndex < 0 {
		return fmt.Errorf("birdnet: model produces no logits (embedding-only); it cannot be used as a label-validated classifier; load it with WithSkipLabelValidation for embedding extraction")
	}
	if modelCfg.LogitsIndex >= len(outputInfos) {
		return fmt.Errorf("birdnet: LogitsIndex %d is out of range for model with %d outputs", modelCfg.LogitsIndex, len(outputInfos))
	}
	logitsDims := outputInfos[modelCfg.LogitsIndex].Shape
	if len(logitsDims) < 2 {
		return nil
	}
	logitsSize := int(logitsDims[len(logitsDims)-1])
	if logitsSize > 0 && labelCount != logitsSize {
		return &LabelCountError{Expected: logitsSize, Got: labelCount}
	}
	return nil
}

// sessionOptionSetter is the subset of *SessionOptions that
// applySessionDefaults configures, so the defaults can be tested without
// loading the ONNX Runtime library.
type sessionOptionSetter interface {
	SetIntraOpNumThreads(n int) error
	SetInterOpNumThreads(n int) error
	AddConfigEntry(key, value string) error
}

// ONNX Runtime session config keys (onnxruntime_session_options_config_keys.h)
// that control whether idle thread-pool workers spin-wait for work or block.
const (
	ortConfigAllowIntraOpSpinning = "session.intra_op.allow_spinning"
	ortConfigAllowInterOpSpinning = "session.inter_op.allow_spinning"
	ortConfigValueDisabled        = "0"
)

// applySessionDefaults sets the session options every classifier session
// starts from, before the caller's WithSessionOptions callback runs.
//
// Thread-pool spinning is disabled: ONNX Runtime spins by default, which keeps
// every pool worker busy-waiting between ops and after each run. Real-time
// analysis runs one short inference every second or so, so spinning burns
// several times the CPU of the inference itself for little or no latency gain.
// The entries have no effect at the single-thread default set here; they matter
// because WithSessionOptions callbacks raise the thread count afterwards
// (NewONNXClassifier uses runtime.NumCPU), and config entries survive that.
func applySessionDefaults(so sessionOptionSetter) error {
	if err := so.SetIntraOpNumThreads(1); err != nil {
		return fmt.Errorf("birdnet: failed to set intra-op threads: %w", err)
	}
	if err := so.SetInterOpNumThreads(1); err != nil {
		return fmt.Errorf("birdnet: failed to set inter-op threads: %w", err)
	}
	if err := so.AddConfigEntry(ortConfigAllowIntraOpSpinning, ortConfigValueDisabled); err != nil {
		return fmt.Errorf("birdnet: failed to disable intra-op spinning: %w", err)
	}
	if err := so.AddConfigEntry(ortConfigAllowInterOpSpinning, ortConfigValueDisabled); err != nil {
		return fmt.Errorf("birdnet: failed to disable inter-op spinning: %w", err)
	}
	return nil
}

// createSession builds an ONNX Runtime session with default options.
func createSession(modelPath string, sessionOptsFn func(*SessionOptions)) (*ort.Session, error) {
	sessOpts, err := ort.NewSessionOptions()
	if err != nil {
		return nil, fmt.Errorf("birdnet: failed to create session options: %w", err)
	}
	defer func() { _ = sessOpts.Close() }()

	if err := applySessionDefaults(sessOpts); err != nil {
		return nil, err
	}

	if sessionOptsFn != nil {
		sessionOptsFn(sessOpts)
	}

	session, err := ort.NewSession(modelPath, sessOpts)
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
		return LoadLabels(cfg.labelsPath)
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
	logits, _, err := c.PredictRawWithEmbeddings(audio)
	return logits, err
}

// PredictRawWithEmbeddings runs inference and returns both raw logits and embedding vector.
// Returns nil embeddings if the model does not produce embeddings.
func (c *Classifier) PredictRawWithEmbeddings(audio []float32) (logits, embeddings []float32, err error) {
	if c.session == nil {
		return nil, nil, ErrSessionClosed
	}
	if len(audio) != c.config.SampleCount {
		return nil, nil, &InputSizeError{Expected: c.config.SampleCount, Got: len(audio)}
	}

	inputShape := makeBatchInputShape(c.config.InputShape, 1)
	inputTensor, err := ort.CreateTensor(inputShape, audio)
	if err != nil {
		return nil, nil, fmt.Errorf("birdnet: failed to create input tensor: %w", err)
	}
	defer func() { _ = inputTensor.Close() }()

	outputs, release, err := c.run(inputTensor, 1, "inference failed")
	if err != nil {
		return nil, nil, err
	}
	defer release()

	// Embedding-only models (LogitsIndex < 0, e.g. the head-pruned bat backbone)
	// produce no logits; skip logits extraction and return nil logits.
	if c.config.LogitsIndex >= 0 {
		allLogits, err := ort.TensorData[float32](outputs[c.config.LogitsIndex])
		if err != nil {
			return nil, nil, fmt.Errorf("birdnet: logits tensor has unexpected type: %w", err)
		}
		logitsSize := c.config.LogitsSize
		if logitsSize <= 0 {
			return nil, nil, fmt.Errorf("birdnet: invalid logits size %d", logitsSize)
		}
		if logitsSize > len(allLogits) {
			return nil, nil, fmt.Errorf("birdnet: logits tensor too small: need %d, have %d", logitsSize, len(allLogits))
		}
		logits = make([]float32, logitsSize)
		copy(logits, allLogits[:logitsSize])
	}

	if c.config.EmbeddingIndex >= 0 {
		allEmb, err := ort.TensorData[float32](outputs[c.config.EmbeddingIndex])
		if err != nil {
			return nil, nil, fmt.Errorf("birdnet: embedding tensor has unexpected type: %w", err)
		}
		if c.config.EmbeddingSize > len(allEmb) {
			return nil, nil, fmt.Errorf("birdnet: embedding tensor too small: need %d, have %d", c.config.EmbeddingSize, len(allEmb))
		}
		embeddings = make([]float32, c.config.EmbeddingSize)
		copy(embeddings, allEmb[:c.config.EmbeddingSize])
	}

	return logits, embeddings, nil
}

// Predict runs inference on a single audio segment.
// The audio slice must contain exactly Config().SampleCount float32 samples
// (mono, at the model's sample rate, normalized to [-1.0, 1.0]).
func (c *Classifier) Predict(audio []float32) (*Result, error) {
	if c.session == nil {
		return nil, ErrSessionClosed
	}
	if len(audio) != c.config.SampleCount {
		return nil, &InputSizeError{Expected: c.config.SampleCount, Got: len(audio)}
	}

	// Build input shape for batch size 1
	inputShape := makeBatchInputShape(c.config.InputShape, 1)

	// Create input tensor (wraps Go slice, no copy)
	inputTensor, err := ort.CreateTensor(inputShape, audio)
	if err != nil {
		return nil, fmt.Errorf("birdnet: failed to create input tensor: %w", err)
	}
	defer func() { _ = inputTensor.Close() }()

	// Run inference; ONNX Runtime allocates the outputs
	outputs, release, err := c.run(inputTensor, 1, "inference failed")
	if err != nil {
		return nil, err
	}
	defer release()

	// Process results
	return c.processOutput(outputs, 0)
}

// PredictBatch runs inference on multiple audio segments in a single batch.
// Each segment must contain exactly Config().SampleCount samples.
func (c *Classifier) PredictBatch(segments [][]float32) ([]*Result, error) {
	if c.session == nil {
		return nil, ErrSessionClosed
	}
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
	inputTensor, err := ort.CreateTensor(inputShape, flat)
	if err != nil {
		return nil, fmt.Errorf("birdnet: failed to create batch input tensor: %w", err)
	}
	defer func() { _ = inputTensor.Close() }()

	// Run inference; ONNX Runtime allocates the outputs
	outputs, release, err := c.run(inputTensor, batchSize, "batch inference failed")
	if err != nil {
		return nil, err
	}
	defer release()

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
		err := c.session.Close()
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

// run executes the model on input and returns the outputs in model output
// order, so LogitsIndex and EmbeddingIndex keep their meaning. The outputs
// live in ONNX Runtime memory: the caller must call release once it has copied
// what it needs, and must not keep any slice that points into them.
// failure is the message prefix for an inference error.
func (c *Classifier) run(input *ort.Tensor, batchSize int, failure string) (outputs []*ort.Tensor, release func(), err error) {
	named, err := c.session.Run(context.Background(), map[string]*ort.Tensor{c.inputName: input}, c.outputNames)
	if err != nil {
		return nil, nil, fmt.Errorf("birdnet: %s: %w", failure, err)
	}
	release = func() { closeTensors(named) }

	outputs = make([]*ort.Tensor, len(c.outputNames))
	for i, name := range c.outputNames {
		outputs[i] = named[name]
	}
	if err := c.validateOutputShapes(outputs, batchSize); err != nil {
		release()
		return nil, nil, err
	}
	return outputs, release, nil
}

// validateOutputShapes checks that every output has the shape the model
// configuration expects for batchSize. ONNX Runtime allocates the outputs, so
// this replaces the check that preallocated output tensors gave implicitly.
func (c *Classifier) validateOutputShapes(outputs []*ort.Tensor, batchSize int) error {
	if len(outputs) != c.config.NumOutputs {
		return fmt.Errorf("birdnet: model returned %d outputs, expected %d", len(outputs), c.config.NumOutputs)
	}
	for i, out := range outputs {
		if out == nil {
			return fmt.Errorf("birdnet: output %d (%s) missing from the inference result", i, c.outputNames[i])
		}
		want, err := c.outputShape(i, batchSize)
		if err != nil {
			return err
		}
		if got := out.Shape(); !slices.Equal(got, want) {
			return fmt.Errorf("birdnet: output %d (%s) has shape %v, expected %v", i, c.outputNames[i], got, want)
		}
	}
	return nil
}

// outputShape returns the expected shape for a given output tensor index.
func (c *Classifier) outputShape(outputIdx, batchSize int) ([]int64, error) {
	batch := int64(batchSize)
	switch c.config.Type {
	case BirdNETv24:
		// Index-aware so it covers the 2-output model (logits@0 + embedding@1), the
		// head-pruned embedding-only model (embedding@0, no logits), and the plain
		// 1-output classifier (logits@0, no embedding). EmbeddingIndex/LogitsIndex are
		// -1 when absent, which never equals a real outputIdx (>= 0).
		switch outputIdx {
		case c.config.EmbeddingIndex:
			if c.config.EmbeddingSize > 0 {
				return []int64{batch, int64(c.config.EmbeddingSize)}, nil
			}
		case c.config.LogitsIndex:
			return []int64{batch, int64(c.config.LogitsSize)}, nil
		}
	case BirdNETv30:
		// Index-aware (like the BirdNETv24 case) so it covers both export orderings:
		// EmbeddingIndex/LogitsIndex are resolved by size in buildModelConfig.
		switch outputIdx {
		case c.config.EmbeddingIndex:
			return []int64{batch, int64(c.config.EmbeddingSize)}, nil
		case c.config.LogitsIndex:
			return []int64{batch, int64(c.config.LogitsSize)}, nil
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
			return []int64{batch, int64(c.config.LogitsSize)}, nil
		}
	}
	return nil, fmt.Errorf("birdnet: unexpected output index %d for model %s", outputIdx, c.config.Type)
}

// processOutput extracts predictions and embeddings from output tensors for a given batch index.
func (c *Classifier) processOutput(outputs []*ort.Tensor, batchIdx int) (*Result, error) {
	// An embedding-only model produces no logits and therefore no species
	// predictions; callers that need embeddings must use PredictRawWithEmbeddings.
	if c.config.LogitsIndex < 0 {
		return nil, fmt.Errorf("birdnet: model has no logits output; use PredictRawWithEmbeddings for embedding extraction")
	}
	allLogits, err := ort.TensorData[float32](outputs[c.config.LogitsIndex])
	if err != nil {
		return nil, fmt.Errorf("birdnet: logits tensor has unexpected type: %w", err)
	}
	stride := c.config.LogitsSize
	if stride <= 0 {
		return nil, fmt.Errorf("birdnet: invalid logits size %d", stride)
	}
	start := batchIdx * stride
	end := start + stride
	if end > len(allLogits) {
		return nil, fmt.Errorf("birdnet: logits tensor too small for batch index %d", batchIdx)
	}
	logits := allLogits[start:end]

	scores := activationFor(c.config.Type, logits)

	var embeddings []float32
	if c.config.EmbeddingIndex >= 0 {
		allEmb, err := ort.TensorData[float32](outputs[c.config.EmbeddingIndex])
		if err != nil {
			return nil, fmt.Errorf("birdnet: embedding tensor has unexpected type: %w", err)
		}
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

// runSingleOutput runs a one-input session and returns the float32 data of the
// named output after checking its shape. The data points into ONNX Runtime
// memory: the caller must copy what it needs and then call release, and must
// not keep the slice.
func runSingleOutput(session *ort.Session, inputName string, input *ort.Tensor, outputName string, wantShape []int64) (data []float32, release func(), err error) {
	outs, err := session.Run(context.Background(), map[string]*ort.Tensor{inputName: input}, []string{outputName})
	if err != nil {
		return nil, nil, err
	}
	release = func() { closeTensors(outs) }
	out := outs[outputName]
	if out == nil {
		release()
		return nil, nil, fmt.Errorf("output %q missing from the inference result", outputName)
	}
	if got := out.Shape(); !slices.Equal(got, wantShape) {
		release()
		return nil, nil, fmt.Errorf("output %q has shape %v, expected %v", outputName, got, wantShape)
	}
	data, err = ort.TensorData[float32](out)
	if err != nil {
		release()
		return nil, nil, err
	}
	return data, release, nil
}

// closeTensors closes every tensor in the result map of a session run.
func closeTensors(tensors map[string]*ort.Tensor) {
	for _, t := range tensors {
		_ = t.Close()
	}
}
