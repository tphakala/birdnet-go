//go:build onnx

package inference

import (
	"fmt"
	"sync"

	ort "github.com/tphakala/birdnet-go/internal/inference/onnx"
	ortlib "github.com/yalue/onnxruntime_go"
)

var (
	ortInitMu      sync.Mutex
	ortInitialized bool
)

// ONNXClassifierOptions configures the ONNX species classifier.
type ONNXClassifierOptions struct {
	// Labels is the species label list. Required.
	Labels []string
	// Threads is the number of CPU threads for ONNX inference. 0 = use ONNX defaults.
	Threads int
}

// onnxClassifier implements Classifier using an ONNX Runtime session.
type onnxClassifier struct {
	classifier *ort.Classifier
	numSpecies int
}

// NewONNXClassifier creates a Classifier backed by an ONNX Runtime model.
// The ONNX Runtime must be initialized via InitONNXRuntime before calling this.
func NewONNXClassifier(modelPath string, opts ONNXClassifierOptions) (Classifier, error) {
	if len(opts.Labels) == 0 {
		return nil, fmt.Errorf("ONNX classifier requires labels")
	}

	classifierOpts := []ort.ClassifierOption{
		ort.WithLabels(opts.Labels),
		ort.WithTopK(0),          // We handle topK in BirdNET-Go's post-processing
		ort.WithMinConfidence(0), // No filtering, return all raw scores
	}
	var configErr error
	if opts.Threads > 0 {
		threads := opts.Threads
		classifierOpts = append(classifierOpts, ort.WithSessionOptions(func(so *ortlib.SessionOptions) {
			if err := so.SetIntraOpNumThreads(threads); err != nil && configErr == nil {
				configErr = fmt.Errorf("failed to set IntraOpNumThreads to %d: %w", threads, err)
			}
			if err := so.SetInterOpNumThreads(threads); err != nil && configErr == nil {
				configErr = fmt.Errorf("failed to set InterOpNumThreads to %d: %w", threads, err)
			}
		}))
	}
	classifier, err := ort.NewClassifier(modelPath, classifierOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create ONNX classifier: %w", err)
	}
	if configErr != nil {
		_ = classifier.Close()
		return nil, fmt.Errorf("failed to configure ONNX session options: %w", configErr)
	}

	return &onnxClassifier{
		classifier: classifier,
		numSpecies: len(opts.Labels),
	}, nil
}

// Predict runs ONNX inference, returning raw logits (pre-activation).
func (c *onnxClassifier) Predict(samples []float32) ([]float32, error) {
	return c.classifier.PredictRaw(samples)
}

// PredictWithEmbeddings runs ONNX inference, returning both raw logits and embedding vector.
func (c *onnxClassifier) PredictWithEmbeddings(samples []float32) (logits, embeddings []float32, err error) {
	return c.classifier.PredictRawWithEmbeddings(samples)
}

// NumSpecies returns the number of species in the model output.
func (c *onnxClassifier) NumSpecies() int {
	return c.numSpecies
}

// Close releases the ONNX session resources.
func (c *onnxClassifier) Close() {
	if c.classifier != nil {
		_ = c.classifier.Close()
		c.classifier = nil
	}
}

// ONNXCustomClassifierOptions configures the ONNX custom classifier.
type ONNXCustomClassifierOptions struct {
	Labels     []string // Provide labels directly (takes priority over LabelsPath)
	LabelsPath string   // Load labels from file (text, CSV, or JSON)
	Threads    int
}

type onnxCustomClassifier struct {
	classifier *ort.CustomClassifier
}

// NewONNXCustomClassifier creates a CustomClassifier backed by an ONNX Runtime model.
func NewONNXCustomClassifier(modelPath string, opts ONNXCustomClassifierOptions) (CustomClassifier, error) {
	builder := ort.NewCustomClassifierBuilder().
		ModelPath(modelPath).
		TopK(0).
		MinConfidence(0)

	switch {
	case len(opts.Labels) > 0:
		builder = builder.Labels(opts.Labels)
	case opts.LabelsPath != "":
		builder = builder.LabelsPath(opts.LabelsPath)
	default:
		return nil, fmt.Errorf("ONNX custom classifier requires labels or labels path")
	}

	if opts.Threads > 0 {
		threads := opts.Threads
		builder = builder.SessionOptions(func(so *ortlib.SessionOptions) {
			_ = so.SetIntraOpNumThreads(threads)
			_ = so.SetInterOpNumThreads(threads)
		})
	}

	cc, err := builder.Build()
	if err != nil {
		return nil, fmt.Errorf("failed to create ONNX custom classifier: %w", err)
	}

	return &onnxCustomClassifier{
		classifier: cc,
	}, nil
}

// PredictEmbedding runs inference on an embedding vector.
func (c *onnxCustomClassifier) PredictEmbedding(embeddings []float32) ([]float32, error) {
	return c.classifier.PredictRaw(embeddings)
}

// NumClasses returns the number of output classes.
func (c *onnxCustomClassifier) NumClasses() int {
	return c.classifier.NumClasses()
}

// Labels returns the classification labels.
func (c *onnxCustomClassifier) Labels() []string {
	return c.classifier.Labels()
}

// Close releases the ONNX custom classifier session.
func (c *onnxCustomClassifier) Close() {
	if c.classifier != nil {
		_ = c.classifier.Close()
		c.classifier = nil
	}
}

// ONNXRangeFilterOptions configures the ONNX range filter.
type ONNXRangeFilterOptions struct {
	// Labels is the species label list. Required.
	Labels []string
}

// onnxRangeFilter implements RangeFilter using an ONNX Runtime session.
type onnxRangeFilter struct {
	filter     *ort.RangeFilter
	numSpecies int
}

// NewONNXRangeFilter creates a RangeFilter backed by an ONNX Runtime meta model.
func NewONNXRangeFilter(modelPath string, opts ONNXRangeFilterOptions) (RangeFilter, error) {
	if len(opts.Labels) == 0 {
		return nil, fmt.Errorf("ONNX range filter requires labels")
	}

	filter, err := ort.NewRangeFilter(modelPath,
		ort.WithRangeFilterLabels(opts.Labels),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create ONNX range filter: %w", err)
	}

	return &onnxRangeFilter{
		filter:     filter,
		numSpecies: len(opts.Labels),
	}, nil
}

// Predict returns species occurrence scores for a geographic location and week.
func (r *onnxRangeFilter) Predict(latitude, longitude, week float32) ([]float32, error) {
	return r.filter.PredictRaw(latitude, longitude, week)
}

// NumSpecies returns the number of species in the range filter model output.
func (r *onnxRangeFilter) NumSpecies() int {
	return r.numSpecies
}

// Close releases the ONNX range filter session resources.
func (r *onnxRangeFilter) Close() {
	if r.filter != nil {
		_ = r.filter.Close()
		r.filter = nil
	}
}

// InitONNXRuntime initializes the ONNX Runtime with the given shared library path.
// Safe to call multiple times — skips if already initialized successfully.
// On failure, allows retry with a corrected path (supports hot-reload recovery).
func InitONNXRuntime(libraryPath string) (err error) {
	ortInitMu.Lock()
	defer ortInitMu.Unlock()

	if ortInitialized {
		return nil
	}

	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("failed to initialize ONNX Runtime: %v", r)
		}
	}()

	ort.MustInitORT(libraryPath)
	ortInitialized = true
	return nil
}

// DestroyONNXRuntime tears down the ONNX Runtime environment.
// Resets initialization state so InitONNXRuntime can be called again.
func DestroyONNXRuntime() error {
	ortInitMu.Lock()
	ortInitialized = false
	ortInitMu.Unlock()
	return ort.DestroyORT()
}
