package onnx

import (
	"fmt"

	ort "github.com/microsoft/onnxruntime/go/onnxruntime"
)

// CustomClassifier runs secondary classification on embedding vectors from a primary model.
// Used for custom classification heads (e.g., bat species detection from BirdNET embeddings).
// It is safe for concurrent use from multiple goroutines.
type CustomClassifier struct {
	session    *ort.Session
	labels     []string
	inputName  string
	outputName string
	inputDim   int
	numClasses int
	topK       int
	minConf    float32
}

// CustomClassifierBuilder constructs a CustomClassifier with validated configuration.
type CustomClassifierBuilder struct {
	modelPath     string
	labelsPath    string
	labels        []string
	topK          int
	minConf       float32
	sessionOptsFn func(*SessionOptions)
}

// NewCustomClassifierBuilder creates a new builder.
func NewCustomClassifierBuilder() *CustomClassifierBuilder {
	return &CustomClassifierBuilder{}
}

// ModelPath sets the ONNX model path.
func (b *CustomClassifierBuilder) ModelPath(path string) *CustomClassifierBuilder {
	b.modelPath = path
	return b
}

// LabelsPath sets the labels file path (text, CSV, or JSON).
func (b *CustomClassifierBuilder) LabelsPath(path string) *CustomClassifierBuilder {
	b.labelsPath = path
	return b
}

// Labels provides species labels directly.
func (b *CustomClassifierBuilder) Labels(labels []string) *CustomClassifierBuilder {
	b.labels = labels
	return b
}

// TopK sets how many top predictions to return. Default: all classes.
func (b *CustomClassifierBuilder) TopK(k int) *CustomClassifierBuilder {
	b.topK = k
	return b
}

// MinConfidence sets the minimum confidence threshold.
func (b *CustomClassifierBuilder) MinConfidence(threshold float32) *CustomClassifierBuilder {
	b.minConf = threshold
	return b
}

// SessionOptions provides a callback to configure the ONNX Runtime session options.
func (b *CustomClassifierBuilder) SessionOptions(fn func(*SessionOptions)) *CustomClassifierBuilder {
	b.sessionOptsFn = fn
	return b
}

// Build creates the CustomClassifier. Returns an error if model path or labels are not set,
// the model cannot be loaded, or the label count does not match the model output.
func (b *CustomClassifierBuilder) Build() (*CustomClassifier, error) {
	if b.modelPath == "" {
		return nil, ErrModelPathRequired
	}

	// The session carries the model metadata, so the model is loaded once.
	session, err := createSession(b.modelPath, b.sessionOptsFn)
	if err != nil {
		return nil, err
	}

	classifier, err := b.newFromSession(session)
	if err != nil {
		_ = session.Close()
		return nil, err
	}
	return classifier, nil
}

// newFromSession validates the model and labels against an open session. It
// does not close the session on error; the caller does.
func (b *CustomClassifierBuilder) newFromSession(session *ort.Session) (*CustomClassifier, error) {
	inputInfos, outputInfos := session.Inputs(), session.Outputs()
	if len(inputInfos) == 0 || len(outputInfos) == 0 {
		return nil, &ModelDetectionError{Reason: "custom model must have at least one input and one output"}
	}

	inputDim := lastDim(inputInfos[0].Shape)
	if inputDim <= 0 {
		return nil, &ModelDetectionError{Reason: "custom model input has dynamic or missing dimensions"}
	}

	numClasses := lastDim(outputInfos[0].Shape)
	if numClasses <= 0 {
		return nil, &ModelDetectionError{Reason: "custom model output has dynamic or missing dimensions"}
	}

	var labels []string
	switch {
	case len(b.labels) > 0:
		labels = b.labels
	case b.labelsPath != "":
		var err error
		labels, err = LoadLabels(b.labelsPath)
		if err != nil {
			return nil, err
		}
	default:
		return nil, ErrLabelsRequired
	}

	if len(labels) != numClasses {
		return nil, &LabelCountError{Expected: numClasses, Got: len(labels)}
	}

	topK := b.topK
	if topK == 0 {
		topK = numClasses
	}

	return &CustomClassifier{
		session:    session,
		labels:     labels,
		inputName:  inputInfos[0].Name,
		outputName: outputInfos[0].Name,
		inputDim:   inputDim,
		numClasses: numClasses,
		topK:       topK,
		minConf:    b.minConf,
	}, nil
}

// Predict runs inference on a single embedding vector.
func (c *CustomClassifier) Predict(embeddings []float32) ([]Prediction, error) {
	scores, err := c.PredictRaw(embeddings)
	if err != nil {
		return nil, err
	}
	return topK(scores, c.labels, c.topK, c.minConf), nil
}

// PredictRaw runs inference on a single embedding vector and returns all
// sigmoid-applied scores as a flat array (no topK filtering).
func (c *CustomClassifier) PredictRaw(embeddings []float32) ([]float32, error) {
	if c.session == nil {
		return nil, ErrSessionClosed
	}
	if len(embeddings) != c.inputDim {
		return nil, &EmbeddingDimMismatchError{Expected: c.inputDim, Got: len(embeddings)}
	}

	inputTensor, err := ort.CreateTensor([]int64{1, int64(c.inputDim)}, embeddings)
	if err != nil {
		return nil, fmt.Errorf("birdnet: failed to create embedding tensor: %w", err)
	}
	defer func() { _ = inputTensor.Close() }()

	logits, release, err := runSingleOutput(c.session, c.inputName, inputTensor, c.outputName, []int64{1, int64(c.numClasses)})
	if err != nil {
		return nil, fmt.Errorf("birdnet: custom classifier inference failed: %w", err)
	}
	defer release()

	// sigmoidSlice returns a fresh slice, so nothing points into ONNX Runtime
	// memory once release runs.
	return sigmoidSlice(logits), nil
}

// PredictBatch runs inference on multiple embedding vectors in a single batch.
func (c *CustomClassifier) PredictBatch(embeddings [][]float32) ([][]Prediction, error) {
	if c.session == nil {
		return nil, ErrSessionClosed
	}
	if len(embeddings) == 0 {
		return nil, ErrEmptyBatch
	}

	batchSize := len(embeddings)
	flat := make([]float32, 0, batchSize*c.inputDim)
	for i, emb := range embeddings {
		if len(emb) != c.inputDim {
			return nil, fmt.Errorf("birdnet: embedding %d: %w", i, &EmbeddingDimMismatchError{
				Expected: c.inputDim, Got: len(emb),
			})
		}
		flat = append(flat, emb...)
	}

	inputTensor, err := ort.CreateTensor([]int64{int64(batchSize), int64(c.inputDim)}, flat)
	if err != nil {
		return nil, fmt.Errorf("birdnet: failed to create batch embedding tensor: %w", err)
	}
	defer func() { _ = inputTensor.Close() }()

	allLogits, release, err := runSingleOutput(c.session, c.inputName, inputTensor, c.outputName, []int64{int64(batchSize), int64(c.numClasses)})
	if err != nil {
		return nil, fmt.Errorf("birdnet: custom classifier batch inference failed: %w", err)
	}
	defer release()

	results := make([][]Prediction, batchSize)
	for i := range batchSize {
		start := i * c.numClasses
		end := start + c.numClasses
		if end > len(allLogits) {
			return nil, fmt.Errorf("birdnet: custom classifier output too small for batch index %d: need %d, have %d", i, end, len(allLogits))
		}
		scores := sigmoidSlice(allLogits[start:end])
		results[i] = topK(scores, c.labels, c.topK, c.minConf)
	}
	return results, nil
}

// InputDim returns the expected embedding dimension.
func (c *CustomClassifier) InputDim() int {
	return c.inputDim
}

// NumClasses returns the number of output classes.
func (c *CustomClassifier) NumClasses() int {
	return c.numClasses
}

// Labels returns a copy of the classification labels.
func (c *CustomClassifier) Labels() []string {
	cp := make([]string, len(c.labels))
	copy(cp, c.labels)
	return cp
}

// Close releases the ONNX session and associated resources.
func (c *CustomClassifier) Close() error {
	if c.session != nil {
		err := c.session.Close()
		c.session = nil
		return err
	}
	return nil
}
