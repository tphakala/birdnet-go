package inference

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	ort "github.com/tphakala/birdnet-go/internal/inference/onnx"
	"github.com/tphakala/birdnet-go/internal/logger"
)

var (
	ortInitMu      sync.Mutex
	ortInitialized bool
	// ortRejected records a library that loaded but is below the supported
	// minimum. The binding keeps a successful load for the life of the process
	// and ignores later library paths, so the rejection is sticky: every later
	// InitONNXRuntime returns the same error without calling the binding.
	// Guarded by ortInitMu.
	ortRejected *ortRejection
)

// ortRejection describes a loaded ONNX Runtime library that was rejected for
// being older than the supported minimum.
type ortRejection struct {
	version string
	path    string
	err     error
}

// IsORTInitialized reports whether the ONNX Runtime has been successfully initialized. Thread-safe.
func IsORTInitialized() bool {
	ortInitMu.Lock()
	defer ortInitMu.Unlock()
	return ortInitialized
}

// ONNXClassifierOptions configures the ONNX species classifier.
type ONNXClassifierOptions struct {
	// Labels is the species label list. Required.
	Labels []string
	// Threads is the number of CPU threads for ONNX inference. 0 = use ONNX defaults.
	Threads int
	// SkipLabelValidation disables the label-count-vs-model-output check.
	// Use when the model is loaded only for embedding extraction and the
	// caller's label list may not match the model's logits dimension.
	SkipLabelValidation bool
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
	if opts.SkipLabelValidation {
		classifierOpts = append(classifierOpts, ort.WithSkipLabelValidation())
	}
	threads := opts.Threads
	if threads <= 0 {
		threads = runtime.NumCPU()
	}
	var configErr error
	classifierOpts = append(classifierOpts, ort.WithSessionOptions(func(so *ort.SessionOptions) {
		if err := so.SetIntraOpNumThreads(threads); err != nil && configErr == nil {
			configErr = fmt.Errorf("failed to set IntraOpNumThreads to %d: %w", threads, err)
		}
		if err := so.SetInterOpNumThreads(threads); err != nil && configErr == nil {
			configErr = fmt.Errorf("failed to set InterOpNumThreads to %d: %w", threads, err)
		}
	}))
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
	if c.classifier == nil {
		return nil, ort.ErrSessionClosed
	}
	return c.classifier.PredictRaw(samples)
}

// PredictWithEmbeddings runs ONNX inference, returning both raw logits and embedding vector.
func (c *onnxClassifier) PredictWithEmbeddings(samples []float32) (logits, embeddings []float32, err error) {
	if c.classifier == nil {
		return nil, nil, ort.ErrSessionClosed
	}
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

// DetectEmbeddingOutput returns the output-port index and size of a model's BirdNET
// v2.4 embedding output ([.,1024]): index 1 for the 2-output backbone (logits@0 +
// embedding@1), index 0 for the head-pruned, embedding-only model. The bat OpenVINO
// path uses it to bind the embedding extractor to the correct port before inference.
// Returns an error when the model has no 1024-dim output. The ONNX Runtime must be
// initialized via InitONNXRuntime before calling.
func DetectEmbeddingOutput(modelPath string) (index, size int, err error) {
	return ort.DetectEmbeddingOutput(modelPath)
}

// DetectPredictionsOutput returns the output-port index of a model's
// species-predictions output: the output whose last dimension equals numClasses.
// BirdNET v3.0 also exposes a 1280-dim embeddings output whose position varies by
// export, so the OpenVINO path uses this to bind the classifier to the correct
// port before inference. Returns an error when no output matches numClasses.
func DetectPredictionsOutput(modelPath string, numClasses int) (index int, err error) {
	return ort.DetectPredictionsOutput(modelPath, numClasses)
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

	threads := opts.Threads
	if threads <= 0 {
		threads = runtime.NumCPU()
	}
	var configErr error
	builder = builder.SessionOptions(func(so *ort.SessionOptions) {
		if err := so.SetIntraOpNumThreads(threads); err != nil && configErr == nil {
			configErr = fmt.Errorf("failed to set IntraOpNumThreads to %d: %w", threads, err)
		}
		if err := so.SetInterOpNumThreads(threads); err != nil && configErr == nil {
			configErr = fmt.Errorf("failed to set InterOpNumThreads to %d: %w", threads, err)
		}
	})

	cc, err := builder.Build()
	if err != nil {
		return nil, fmt.Errorf("failed to create ONNX custom classifier: %w", err)
	}
	if configErr != nil {
		_ = cc.Close()
		return nil, fmt.Errorf("failed to configure ONNX custom classifier session: %w", configErr)
	}

	return &onnxCustomClassifier{
		classifier: cc,
	}, nil
}

// PredictEmbedding runs inference on an embedding vector.
func (c *onnxCustomClassifier) PredictEmbedding(embeddings []float32) ([]float32, error) {
	if c.classifier == nil {
		return nil, ort.ErrSessionClosed
	}
	return c.classifier.PredictRaw(embeddings)
}

// NumClasses returns the number of output classes.
func (c *onnxCustomClassifier) NumClasses() int {
	if c.classifier == nil {
		return 0
	}
	return c.classifier.NumClasses()
}

// InputDim returns the embedding vector length the classifier expects as input.
func (c *onnxCustomClassifier) InputDim() int {
	if c.classifier == nil {
		return 0
	}
	return c.classifier.InputDim()
}

// Labels returns the classification labels.
func (c *onnxCustomClassifier) Labels() []string {
	if c.classifier == nil {
		return nil
	}
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

// onnxRangeFilter implements BatchRangeFilter using an ONNX Runtime session.
type onnxRangeFilter struct {
	filter     *ort.RangeFilter
	numSpecies int
}

// NewONNXRangeFilter creates a BatchRangeFilter backed by an ONNX Runtime meta model.
func NewONNXRangeFilter(modelPath string, opts ONNXRangeFilterOptions) (BatchRangeFilter, error) {
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
	if r.filter == nil {
		return nil, ort.ErrSessionClosed
	}
	return r.filter.PredictRaw(latitude, longitude, week)
}

// PredictBatch runs batch inference on multiple location/week inputs.
// inputs is a flat slice of [lat, lon, week] triples: len(inputs) must equal batchSize * 3.
// Returns a flat slice of [batchSize * numSpecies] scores in row-major order.
func (r *onnxRangeFilter) PredictBatch(inputs []float32, batchSize int) ([]float32, error) {
	if r.filter == nil {
		return nil, ort.ErrSessionClosed
	}
	return r.filter.PredictBatchRaw(inputs, batchSize)
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
// When libraryPath is empty, searches standard system library paths for
// libonnxruntime.so (Linux) or onnxruntime.dll (Windows).
// Safe to call multiple times; skips if already initialized successfully.
// A failed load (missing file, wrong path) stays retryable with a corrected
// path. A library that loads but is older than the supported minimum stays
// rejected until the process restarts, because the binding keeps the first
// successful load. Do not call it after DestroyONNXRuntime, which is terminal.
func InitONNXRuntime(libraryPath string) (err error) {
	ortInitMu.Lock()
	defer ortInitMu.Unlock()

	if ortInitialized {
		return nil
	}
	if ortRejected != nil {
		return ortRejected.err
	}

	configured := libraryPath != ""
	if libraryPath == "" {
		libraryPath = findONNXRuntimeLibrary()
	}

	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("failed to initialize ONNX Runtime: %v; install guide: %s", r, ORTInstallGuideURL)
		}
	}()

	optOut, initErr := ort.InitORT(libraryPath)
	if initErr != nil {
		return describeORTInitError(libraryPath, initErr)
	}
	if version := ort.Version(); belowORTMinimum(version) {
		ortRejected = &ortRejection{
			version: version,
			path:    libraryPath,
			err:     rejectedORTError(libraryPath, version),
		}
		return ortRejected.err
	}
	ortInitialized = true
	publishORTRuntime(libraryPath, configured)
	logORTInitialized(ort.Version(), libraryPath, optOut)
	return nil
}

// ortAPIBaseTooOldMarker is part of the text the official binding returns when
// the loaded library cannot serve even its lowest C API level (17): "ort: init
// api: GetApi failed; ORT library may be too old (need API >= 17)" (ort.go:67-69
// in github.com/microsoft/onnxruntime/go at the pinned commit). It fires only
// below ONNX Runtime 1.17; the 1.29 floor is enforced after load.
const ortAPIBaseTooOldMarker = "ORT library may be too old"

// belowORTMinimum reports whether version parses and is older than the
// supported minimum. An unparseable version is accepted, matching the
// optimistic rule in CheckORTAvailability.
func belowORTMinimum(version string) bool {
	meets, known := ortVersionMeetsMinimum(version)
	return known && !meets
}

// rejectedORTError is the error for a library that loaded but is older than
// the supported minimum. The binding cannot load a second library in the same
// process, so the user has to restart after installing a newer one.
func rejectedORTError(path, version string) error {
	return fmt.Errorf("ONNX Runtime library %q is version %s, older than the supported minimum (%s required); restart BirdNET-Go after installing a newer library, because the loaded one cannot be replaced while the process runs; install guide: %s",
		path, version, ORTRequiredVersion(), ORTInstallGuideURL)
}

// rejectedORT returns the recorded rejection, or nil when none happened.
func rejectedORT() *ortRejection {
	ortInitMu.Lock()
	defer ortInitMu.Unlock()
	return ortRejected
}

// describeORTInitError turns an InitORT failure into a user-facing error. The
// cryptic "too old" failure gets a readable message; any other failure keeps
// the generic wording. It must not call ort.Version: after a failed init
// that points into an unloaded library.
func describeORTInitError(path string, err error) error {
	if !strings.Contains(err.Error(), ortAPIBaseTooOldMarker) {
		return fmt.Errorf("failed to initialize ONNX Runtime: %w; install guide: %s", err, ORTInstallGuideURL)
	}
	hint := ""
	if v := inferVersionFromPath(path); v != "" {
		if _, known := ortVersionMeetsMinimum(v); known {
			hint = "; its file name suggests version " + v
		}
	}
	return fmt.Errorf("ONNX Runtime library %q is older than the supported minimum (%s required)%s: %w; install guide: %s",
		path, ORTRequiredVersion(), hint, err, ORTInstallGuideURL)
}

// logORTInitialized logs the loaded library and the telemetry opt-out state.
func logORTInitialized(version, libraryPath string, optOut ort.TelemetryOptOut) {
	logger.Global().Module("inference").Info("ONNX Runtime initialized",
		logger.String("version", version),
		logger.String("library_path", libraryPath),
		logger.String("ort_disable_telemetry", optOut.Value),
		logger.Bool("telemetry_disabled", optOut.Disabled),
		logger.Bool("telemetry_api_disabled", optOut.APIDisabled))
}

// findONNXRuntimeLibrary searches standard system paths for the ONNX Runtime
// shared library. Returns the first path found, or "onnxruntime" as a
// fallback for dlopen's default search.
func findONNXRuntimeLibrary() string {
	var candidates []string
	switch runtime.GOOS {
	case "windows":
		candidates = []string{"onnxruntime.dll"}
		if exePath, err := os.Executable(); err == nil {
			candidates = append([]string{filepath.Join(filepath.Dir(exePath), "onnxruntime.dll")}, candidates...)
		}
		for i, c := range candidates {
			if abs, err := filepath.Abs(c); err == nil {
				candidates[i] = abs
			}
		}
	case "darwin":
		candidates = []string{
			"/opt/homebrew/lib/libonnxruntime.dylib",
			"/usr/local/lib/libonnxruntime.dylib",
			"libonnxruntime.dylib",
		}
	default:
		candidates = []string{
			"/usr/lib/libonnxruntime.so",
			"/usr/local/lib/libonnxruntime.so",
			"/usr/lib/aarch64-linux-gnu/libonnxruntime.so",
			"/usr/lib/x86_64-linux-gnu/libonnxruntime.so",
			"libonnxruntime.so",
		}
	}
	for _, path := range candidates {
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	if runtime.GOOS == "darwin" {
		return "libonnxruntime.dylib"
	}
	return "onnxruntime"
}

// DestroyONNXRuntime tears down the ONNX Runtime environment. The teardown is
// terminal: the official binding cannot initialize again afterwards, so
// InitONNXRuntime must not be called after it in the same process. It fails
// while any session is still open (the error says how many) and then leaves
// the state unchanged. It clears the initialization state and the runtime
// snapshot on success. It is a no-op (returns nil) when the runtime was never
// initialized, mirroring DestroyOpenVINO, so a shutdown teardown can call it
// unconditionally.
func DestroyONNXRuntime() error {
	ortInitMu.Lock()
	defer ortInitMu.Unlock()
	if !ortInitialized {
		return nil
	}
	if err := ort.DestroyORT(); err != nil {
		return err
	}
	ortInitialized = false
	clearORTRuntime()
	return nil
}
