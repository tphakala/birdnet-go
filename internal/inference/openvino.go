package inference

import (
	"slices"
	"sync"

	"github.com/tphakala/birdnet-go/internal/errors"
	ov "github.com/tphakala/birdnet-go/internal/inference/openvino"
)

var (
	ovInitMu      sync.Mutex
	ovInitialized bool
)

// OpenVINO device names accepted by OpenVINOClassifierOptions.Device. They are
// the device strings the OpenVINO backend expects ("CPU", "GPU").
const (
	OVDeviceCPU = ov.DeviceCPU
	OVDeviceGPU = ov.DeviceGPU
)

// OpenVINOClassifierOptions configures the OpenVINO species classifier.
type OpenVINOClassifierOptions struct {
	Labels        []string // species labels; required (validated against model output)
	Threads       int      // INFERENCE_NUM_THREADS; 0 = OpenVINO auto-tune (CPU only)
	PrecisionHint string   // INFERENCE_PRECISION_HINT; "" defaults to f16
	Device        string   // ov.DeviceCPU (default) or ov.DeviceGPU
	OutputIndex   int      // logits output port index; 0 default, 3 for Perch v2
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
	threads := max(opts.Threads, 0)
	c, err := ov.NewClassifier(modelPath, ov.Options{
		PrecisionHint: prec,
		Threads:       threads,
		Device:        opts.Device,
		OutputIndex:   opts.OutputIndex,
	})
	if err != nil {
		return nil, err
	}
	// Validate the model's real output dimension against the label count. The OV
	// backend reports NumClasses from the compiled model (not the label list), so
	// a mismatch is a genuine model/label inconsistency: reject it and let the
	// caller fall back to ORT. (Issue #1112: when numSpecies was len(labels) this
	// check was tautological and could never catch a wrong model or label file.)
	if n := c.NumClasses(); n != len(opts.Labels) {
		_ = c.Close()
		return nil, errors.Newf("OpenVINO model output dimension %d does not match label count %d", n, len(opts.Labels)).
			Category(errors.CategoryValidation).Build()
	}
	return &openvinoClassifier{c: c, numSpecies: c.NumClasses()}, nil
}

// OpenVINOHasDevice reports whether the named OpenVINO device (e.g. ov.DeviceGPU)
// is available. It returns false on any error (backend not compiled in, core not
// initialized, or query failure), so callers can use it as a plain gate.
func OpenVINOHasDevice(name string) bool {
	devs, err := ov.AvailableDevices()
	if err != nil {
		return false
	}
	return slices.Contains(devs, name)
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
