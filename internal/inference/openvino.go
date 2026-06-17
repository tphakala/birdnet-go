package inference

import (
	"sync"

	"github.com/tphakala/birdnet-go/internal/errors"
	ov "github.com/tphakala/birdnet-go/internal/inference/openvino"
)

var (
	ovInitMu      sync.Mutex
	ovInitialized bool
)

// OpenVINOClassifierOptions configures the OpenVINO species classifier.
type OpenVINOClassifierOptions struct {
	Labels        []string // species labels; required (drives NumSpecies)
	Threads       int      // INFERENCE_NUM_THREADS; 0 = OpenVINO auto-tune
	PrecisionHint string   // INFERENCE_PRECISION_HINT; "" defaults to f16
}

type openvinoClassifier struct {
	c          ov.Classifier
	numSpecies int
}

// NewOpenVINOClassifier creates a Classifier backed by the native OpenVINO
// backend. InitOpenVINO must be called first; if the OpenVINO core is not
// initialized (or the backend is not compiled in), construction returns
// ov.ErrOpenVINOUnavailable, which the caller treats as fall back to ORT.
func NewOpenVINOClassifier(modelPath string, opts OpenVINOClassifierOptions) (Classifier, error) {
	if len(opts.Labels) == 0 {
		return nil, errors.Newf("OpenVINO classifier requires labels").Build()
	}
	prec := opts.PrecisionHint
	if prec == "" {
		prec = ov.DefaultPrecisionHint
	}
	threads := opts.Threads
	if threads < 0 {
		threads = 0
	}
	c, err := ov.NewClassifier(modelPath, ov.Options{PrecisionHint: prec, Threads: threads})
	if err != nil {
		return nil, err
	}
	return &openvinoClassifier{c: c, numSpecies: len(opts.Labels)}, nil
}

func (c *openvinoClassifier) Predict(samples []float32) ([]float32, error) {
	if c.c == nil {
		return nil, ov.ErrOpenVINOUnavailable
	}
	return c.c.PredictRaw(samples)
}

func (c *openvinoClassifier) NumSpecies() int { return c.numSpecies }

func (c *openvinoClassifier) Close() {
	if c.c != nil {
		_ = c.c.Close()
		c.c = nil
	}
}

// InitOpenVINO initializes the OpenVINO runtime (loads libopenvino_c and the
// process-global core). Safe to call repeatedly; retries after failure for
// hot-reload recovery. Mirrors InitONNXRuntime.
func InitOpenVINO(libraryPath string) error {
	ovInitMu.Lock()
	defer ovInitMu.Unlock()
	if ovInitialized {
		return nil
	}
	if err := ov.InitOV(libraryPath); err != nil {
		return err
	}
	ovInitialized = true
	return nil
}

// DestroyOpenVINO tears down the OpenVINO core. Call only on shutdown.
func DestroyOpenVINO() error {
	ovInitMu.Lock()
	defer ovInitMu.Unlock()
	if !ovInitialized {
		return nil
	}
	if err := ov.DestroyOV(); err != nil {
		return err
	}
	ovInitialized = false
	return nil
}

// IsOpenVINOInitialized reports whether the OpenVINO core is initialized.
func IsOpenVINOInitialized() bool {
	ovInitMu.Lock()
	defer ovInitMu.Unlock()
	return ovInitialized
}
