package inference

import (
	"slices"
	"sync"
	"sync/atomic"

	"github.com/tphakala/birdnet-go/internal/errors"
	ov "github.com/tphakala/birdnet-go/internal/inference/openvino"
)

var (
	ovInitMu      sync.Mutex
	ovInitialized bool
)

// ovActiveClassifiers counts OpenVINO classifiers currently serving inference.
// It is NOT the same as ovInitialized: the device planner loads the OpenVINO core
// (setting ovInitialized) merely to enumerate devices, even on a host that then
// falls back to ORT. Diagnostics must report "OpenVINO active" only when a
// classifier is actually running on it, so they read this counter, not the
// core-loaded flag.
var ovActiveClassifiers atomic.Int64

// OpenVINO device names accepted by OpenVINOClassifierOptions.Device. They are
// the device strings the OpenVINO backend expects ("CPU", "GPU").
const (
	OVDeviceCPU = ov.DeviceCPU
	OVDeviceGPU = ov.DeviceGPU
)

// OVPrecisionF32 is the INFERENCE_PRECISION_HINT for full f32 inference, used to
// override the f16 default where the GPU plugin miscompiles a model at f16.
const OVPrecisionF32 = ov.PrecisionF32

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
	ovActiveClassifiers.Add(1)
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
		ovActiveClassifiers.Add(-1)
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

// OpenVINOStatus describes whether the OpenVINO backend is compiled in and
// whether it is actually serving inference. It mirrors ORTStatus for the
// diagnostics surface. Supported distinguishes "not an openvino build" from
// "openvino build that fell back to ORT", which is the key signal for confirming
// an opt-in took effect.
type OpenVINOStatus struct {
	// Supported is true when this build links the OpenVINO backend (built with
	// the "openvino" tag).
	Supported bool `json:"supported"`
	// Active is true when at least one classifier is currently running on the
	// OpenVINO backend. This is deliberately NOT "core loaded": the device planner
	// loads the core just to enumerate devices even on hosts that then fall back to
	// ORT, so the core-loaded flag would falsely report OpenVINO as in use.
	Active bool `json:"active"`
}

// CheckOpenVINOAvailability reports the OpenVINO backend status. It never mutates
// global OpenVINO state (it does not attempt to load the library).
func CheckOpenVINOAvailability() OpenVINOStatus {
	return OpenVINOStatus{
		Supported: ov.Supported,
		Active:    ovActiveClassifiers.Load() > 0,
	}
}
