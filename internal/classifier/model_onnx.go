package classifier

import (
	"os"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/inference"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// onnxModelPath resolves the ONNX classifier model file: the explicit config
// model path when set, otherwise ModelInfo.CustomPath (set by the arm64 default
// resolver, defaultClassifierModelInfo). The first value is the RESOLVED
// configured path (see BirdNET.configuredModelPath), not the raw setting.
// Returns "" when neither is set. Keeping
// the default in CustomPath avoids mutating settings.BirdNET.ModelPath, which
// would make the default indistinguishable from a user override.
func (bn *BirdNET) onnxModelPath() string {
	if p := bn.configuredModelPath(); p != "" {
		return p
	}
	return bn.ModelInfo.CustomPath
}

// initializeONNXModel loads and initializes an ONNX model as the classifier backend.
func (bn *BirdNET) initializeONNXModel() error {
	start := time.Now()
	log := GetLogger()
	settings := bn.Settings

	modelPath := bn.onnxModelPath()
	if modelPath == "" {
		return errors.Newf("ONNX classifier model path is empty").
			Category(errors.CategoryModelInit).
			Context("model_id", bn.ModelInfo.ID).
			Build()
	}

	// Expand environment variables and the ~ prefix so dispatch and loading agree:
	// usesONNXBackend env-expands the path before checking the .onnx extension, so a
	// configured $VAR/~ ONNX path would dispatch here yet fail to open if left raw.
	rawPath := modelPath
	modelPath = os.ExpandEnv(modelPath)
	modelPath, err := conf.ExpandTildePath(modelPath)
	if err != nil {
		return errors.New(err).
			Category(errors.CategoryFileIO).
			Context("path", rawPath).
			Build()
	}

	if err := checkORTOrFail(settings.BirdNET.ONNXRuntimePath, "ONNX classifier", "onnx_classifier", ""); err != nil {
		return err
	}

	// Initialize ONNX Runtime if not already done
	if err := inference.InitONNXRuntime(settings.BirdNET.ONNXRuntimePath); err != nil {
		return errors.New(err).
			Category(errors.CategoryModelInit).
			Context("onnx_runtime_path", settings.BirdNET.ONNXRuntimePath).
			Timing("onnx-init", time.Since(start)).
			Build()
	}

	classifier, err := inference.NewONNXClassifier(modelPath, inference.ONNXClassifierOptions{
		Labels:  settings.BirdNET.Labels,
		Threads: settings.BirdNET.Threads,
	})
	if err != nil {
		return errors.New(err).
			Category(errors.CategoryModelInit).
			ModelContext(modelPath, bn.ModelInfo.ID).
			Timing("onnx-model-init", time.Since(start)).
			Build()
	}

	bn.classifier = classifier
	// We ship only the CPU execution provider for ONNX Runtime today (other ORT
	// EPs like CUDA/DirectML/CoreML would set the device from the bound provider).
	// ONNX Runtime executes the model file as-is, so the runtime precision is the
	// weight precision recorded in ModelInfo.Quantization (e.g. INT8 for the arm64
	// int8 variant, FP32 for an fp32 ONNX model).
	bn.setRuntimeInfo(deviceCPU, BackendONNX, string(bn.ModelInfo.Quantization))

	log.Info("ONNX model initialized",
		logger.String("model", modelPath),
		logger.Int("species", classifier.NumSpecies()))

	return nil
}
