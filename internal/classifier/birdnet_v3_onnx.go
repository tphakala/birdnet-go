// birdnet_v3_onnx.go provides BirdNET v3.0 acoustic classifier support using the
// ONNX backend. Label parsing is in birdnet_v3.go.
package classifier

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/inference"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// BirdNETV3 represents a loaded BirdNET v3.0 acoustic classifier.
// Implements ModelInstance. Goroutine-safe via internal mutex.
type BirdNETV3 struct {
	classifier inference.Classifier
	labels     []string
	info       ModelInfo
	mu         sync.Mutex
	// device is the compute device the classifier bound to: the OpenVINO device
	// (CPU/GPU) when the OV path succeeds, otherwise deviceCPU for the ONNX Runtime
	// CPU EP. Set once at construction; reported via RuntimeInfo().
	device string
	// backend is the live execution backend (BackendOpenVINO on the OV path, else
	// BackendONNX), and precision is the effective runtime precision (FP16 on the OV
	// path; the weight precision detected from the model filename on the ORT path).
	// Both set once at construction; reported via RuntimeInfo().
	backend   string
	precision string
}

// BirdNETV3Config holds configuration for creating a BirdNET v3.0 model instance.
type BirdNETV3Config struct {
	ModelPath       string // Path to the BirdNET v3.0 ONNX model file
	LabelPath       string // Path to the BirdNET v3.0 label file
	ONNXRuntimePath string // Path to ONNX Runtime shared library
	Threads         int    // CPU threads for inference (0 = default)

	// OpenVINO opt-in, sourced from BirdNET settings. The v3.0 GPU-native model runs
	// on OpenVINO directly (its mel front-end is a Conv1d, not the STFT op that
	// blocks OpenVINO), so there is no model-variant gate; when the gate allows it
	// BirdNET v3.0 runs on OpenVINO (ARM A76 f16 CPU or Intel iGPU) and any failure
	// falls back to ORT.
	Backend        string // BirdNET.Backend ("auto"/"onnx"/"openvino")
	OpenVINOPath   string // BirdNET.OpenVINOPath (libopenvino_c location)
	OpenVINODevice string // BirdNET.OpenVINODevice ("auto"/"cpu"/"gpu")
}

// NewBirdNETV3 creates a new BirdNET v3.0 model instance.
func NewBirdNETV3(cfg *BirdNETV3Config) (*BirdNETV3, error) {
	log := GetLogger()

	// Load and parse labels
	labelData, err := os.ReadFile(cfg.LabelPath)
	if err != nil {
		return nil, errors.New(err).
			Category(errors.CategoryFileIO).
			Context("label_path", cfg.LabelPath).
			Build()
	}

	labels, err := ParseBirdNETV3Labels(labelData)
	if err != nil {
		return nil, errors.New(err).
			Category(errors.CategoryLabelLoad).
			Context("label_path", cfg.LabelPath).
			Build()
	}

	if len(labels) == 0 {
		return nil, errors.Newf("no labels parsed from %s", cfg.LabelPath).
			Category(errors.CategoryLabelLoad).
			Build()
	}

	// Initialize the ONNX Runtime up front, before attempting OpenVINO. The
	// OpenVINO path's predictions-port detection (DetectPredictionsOutput) reads the
	// model metadata through ONNX Runtime, and the ORT fallback classifier needs it
	// too; InitONNXRuntime is idempotent. Doing this here (mirroring NewBat) ensures
	// OpenVINO is not silently skipped when v3.0 is the first ONNX model loaded: an
	// uninitialized runtime would make DetectPredictionsOutput fail and force the ORT
	// fallback even when the user selected the OpenVINO backend.
	if err := inference.InitONNXRuntime(cfg.ONNXRuntimePath); err != nil {
		return nil, errors.New(err).
			Category(errors.CategoryModelInit).
			Context("onnx_runtime_path", cfg.ONNXRuntimePath).
			Build()
	}

	// Prefer the OpenVINO backend when the gate allows it, falling back to ORT on
	// any failure. OpenVINO must never make BirdNET v3.0 fail to load, so
	// tryBirdNETV3OpenVINO logs and swallows OV errors and returns ok=false. device
	// records the compute device actually bound to (the OpenVINO device on the OV
	// path, else the ONNX Runtime CPU EP).
	classifier, device, ok := tryBirdNETV3OpenVINO(cfg, labels)
	// The v3.0 OpenVINO path uses the backend default precision (f16 on every
	// device; openVINOPrecisionFor returns "" for v3.0). The ORT path overrides both
	// below.
	backend := BackendOpenVINO
	precision := string(QuantizationFP16)
	if !ok {
		// Create the ONNX Runtime classifier (the runtime was initialized above).
		var cerr error
		classifier, cerr = inference.NewONNXClassifier(cfg.ModelPath, inference.ONNXClassifierOptions{
			Labels:  labels,
			Threads: cfg.Threads,
		})
		if cerr != nil {
			return nil, errors.New(cerr).
				Category(errors.CategoryModelInit).
				Context("model_path", cfg.ModelPath).
				Context("label_count", len(labels)).
				Build()
		}
		// ONNX Runtime runs BirdNET v3.0 on the CPU execution provider, executing the
		// model file as-is: surface the weight precision detected from the filename
		// (e.g. FP16 for a *_fp16.onnx file; empty when no token).
		device = deviceCPU
		backend = BackendONNX
		precision = string(detectQuantization(cfg.ModelPath))
	}

	info := ModelRegistry[RegistryIDBirdNETV3]
	info.Description = fmt.Sprintf("BirdNET v3.0 acoustic classifier with %d species", len(labels))
	info.NumSpecies = len(labels)

	log.Info("BirdNET v3.0 model initialized",
		logger.String("model_path", cfg.ModelPath),
		logger.String("backend", backend),
		logger.String("device", device),
		logger.Int("species", len(labels)))

	return &BirdNETV3{
		classifier: classifier,
		labels:     labels,
		info:       info,
		device:     device,
		backend:    backend,
		precision:  precision,
	}, nil
}

// tryBirdNETV3OpenVINO attempts to build an OpenVINO classifier for BirdNET v3.0.
// It returns (classifier, device, true) on success or (nil, "", false) to fall
// back to ORT, where device is the concrete OpenVINO device the classifier bound
// to (inference.OVDeviceCPU/OVDeviceGPU). Any failure (gate denied,
// init/compile/validation error) is logged and swallowed: OpenVINO must never make
// BirdNET v3.0 fail to load. Unlike Perch there is no model-variant filename gate:
// the v3.0 GPU-native model has no STFT op, so it compiles on OpenVINO directly.
func tryBirdNETV3OpenVINO(cfg *BirdNETV3Config, labels []string) (inference.Classifier, string, bool) {
	// openVINOPlanFor gates on the build tag, backend preference, and device
	// availability without needing the output port, so run it first; only read the
	// model metadata to resolve the predictions port once OpenVINO is actually in
	// play. This avoids a wasted metadata read on the common non-OpenVINO build. The
	// output index passed here is a placeholder, overwritten below once detected.
	plan, ok, reason := openVINOPlanFor(cfg.Backend, cfg.OpenVINODevice, RegistryIDBirdNETV3, cfg.OpenVINOPath, 0)
	if !ok {
		logOpenVINODeclined(RegistryIDBirdNETV3, cfg.Backend, reason)
		return nil, "", false
	}

	log := GetLogger()

	// Resolve the predictions output port by size (the output whose dimension equals
	// the label count). The v3.0 graph also exposes a 1280-dim embeddings output, and
	// export tooling has emitted the two outputs in both orders, so binding OpenVINO
	// to a fixed port would risk the wrong tensor. On any detection failure, decline
	// OpenVINO and fall back to ORT, which derives the port internally.
	outIdx, err := inference.DetectPredictionsOutput(cfg.ModelPath, len(labels))
	if err != nil {
		log.Warn("BirdNET v3.0 OpenVINO predictions-output detection failed; using ONNX Runtime",
			logger.String("model_path", cfg.ModelPath),
			logger.Error(err))
		return nil, "", false
	}
	plan.outputIndex = outIdx

	// InitOpenVINO is idempotent: the auto/GPU plan above may already have loaded the
	// core to enumerate devices, but the explicit-CPU plan path does not, so init
	// here to cover it. A load failure means no usable OpenVINO; fall back to ORT.
	if err := inference.InitOpenVINO(cfg.OpenVINOPath); err != nil {
		log.Warn("BirdNET v3.0 OpenVINO init failed; using ONNX Runtime", logger.Error(err))
		return nil, "", false
	}

	start := time.Now()
	classifier, err := inference.NewOpenVINOClassifier(cfg.ModelPath, inference.OpenVINOClassifierOptions{
		Labels:        labels,
		Threads:       cfg.Threads,
		Device:        plan.device,
		OutputIndex:   plan.outputIndex,
		PrecisionHint: plan.precision, // "" => f16 default; v3.0 f16 validated OK
	})
	if err != nil {
		log.Warn("BirdNET v3.0 OpenVINO classifier init failed; using ONNX Runtime",
			logger.String("device", plan.device),
			logger.Error(err))
		return nil, "", false
	}

	log.Info("BirdNET v3.0 model using OpenVINO backend",
		logger.String("device", plan.device),
		logger.String("precision", openVINOPrecisionLabel(plan.precision)),
		logger.Int("species", classifier.NumSpecies()),
		logger.String("init_time", time.Since(start).String()))
	return classifier, plan.device, true
}

// Predict runs inference on the given audio samples.
// Implements ModelInstance.
func (b *BirdNETV3) Predict(ctx context.Context, samples [][]float32) ([]datastore.Results, error) {
	span, _ := startPredictSpan(ctx, RegistryIDBirdNETV3, samples)
	defer span.Finish()

	start := time.Now()

	// Guard against empty sample slice. Pre-inference rejections are tagged but not
	// counted as predictions, mirroring BirdNET.Predict.
	if len(samples) == 0 || len(samples[0]) == 0 {
		span.markErrored(errTypeEmptySample)
		return nil, errors.Newf("empty audio sample").
			Category(errors.CategoryValidation).
			Build()
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	// Guard against nil classifier (e.g., after Close() is called concurrently)
	if b.classifier == nil {
		span.markErrored(errTypeClassifierNil)
		return nil, errors.Newf("BirdNET v3.0 classifier is not initialized").
			Category(errors.CategoryModelInit).
			Build()
	}

	// The classifier returns the v3.0 predictions output, which is already a
	// per-class sigmoid (multi-label, applied in-graph, values in [0,1]). Unlike
	// Perch (softmax) and BirdNET v2.4 (sigmoid), no activation is applied here:
	// re-applying sigmoid would double-squash the scores.
	scores, err := b.classifier.Predict(samples[0])
	if err != nil {
		err = errors.New(err).
			Category(errors.CategoryAudio).
			Context("model", RegistryIDBirdNETV3).
			Build()
		recordPredictionFailure(span, RegistryIDBirdNETV3, errTypeInvokeFailed, start, err)
		return nil, err
	}

	// Pair labels with predictions
	results, err := pairLabelsAndConfidence(b.labels, scores)
	if err != nil {
		recordPredictionFailure(span, RegistryIDBirdNETV3, errTypeLabelMismatch, start, err)
		return nil, err
	}

	// Success: Finish records the single prediction because the span is not errored.
	topResults := getTopKResults(results, defaultTopKResults)
	recordPredictionSuccess(span, len(topResults), start)

	return topResults, nil
}

// Spec returns the audio requirements for BirdNET v3.0.
func (b *BirdNETV3) Spec() ModelSpec { return b.info.Spec }

// ModelID returns the unique model identifier.
func (b *BirdNETV3) ModelID() string { return b.info.ID }

// ModelName returns the human-readable model name.
func (b *BirdNETV3) ModelName() string { return b.info.Name }

// ModelVersion returns the model version string.
func (b *BirdNETV3) ModelVersion() string { return "V3.0" }

// NumSpecies returns the number of species.
func (b *BirdNETV3) NumSpecies() int { return len(b.labels) }

// Labels returns a copy of the species labels to prevent mutation of internal state.
func (b *BirdNETV3) Labels() []string {
	out := make([]string, len(b.labels))
	copy(out, b.labels)
	return out
}

// RuntimeInfo returns the device, backend, and effective precision the BirdNET
// v3.0 classifier bound to at construction: the OpenVINO device on the OV path
// (else "CPU"); BackendOpenVINO on the OV path (else BackendONNX); FP16 on the OV
// path or the weight precision detected from the model filename on the ORT path.
// All three are set once and never mutated, so no lock is needed. Implements
// ModelInstance.
func (b *BirdNETV3) RuntimeInfo() (device, backend, precision string) {
	return b.device, b.backend, b.precision
}

// Close releases resources held by the BirdNET v3.0 model.
func (b *BirdNETV3) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.classifier != nil {
		b.classifier.Close()
		b.classifier = nil
	}
	return nil
}
