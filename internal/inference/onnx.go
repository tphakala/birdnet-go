//go:build onnx

package inference

import (
	"fmt"

	ort "github.com/tphakala/birdnet-go/internal/inference/onnx"
	ortlib "github.com/yalue/onnxruntime_go"
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
	if opts.Threads > 0 {
		threads := opts.Threads
		classifierOpts = append(classifierOpts, ort.WithSessionOptions(func(so *ortlib.SessionOptions) {
			_ = so.SetIntraOpNumThreads(threads)
			_ = so.SetInterOpNumThreads(threads)
		}))
	}
	classifier, err := ort.NewClassifier(modelPath, classifierOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create ONNX classifier: %w", err)
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
// Must be called before creating any ONNX classifiers or range filters.
func InitONNXRuntime(libraryPath string) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("failed to initialize ONNX Runtime: %v", r)
		}
	}()
	ort.MustInitORT(libraryPath)
	return nil
}

// DestroyONNXRuntime tears down the ONNX Runtime environment.
func DestroyONNXRuntime() error {
	return ort.DestroyORT()
}
