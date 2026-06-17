package classifier

import (
	"os"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/cpuspec"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/inference"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// shouldTryOpenVINO reports whether the OpenVINO f16 backend should be attempted
// for the primary classifier before falling back to ORT. All of: built with the
// openvino tag, model is the BirdNET v2.4 identity on the ONNX backend, the CPU
// has native f16 (ASIMDHP/A76+), and config does not opt out.
func (bn *BirdNET) shouldTryOpenVINO() bool {
	if !openvinoBackendAvailable {
		return false
	}
	// Explicit ONNX preference opts out. "openvino" and "auto"/"" still require
	// the f16 hardware gate below (an explicit opt-in must not bypass it, or a
	// non-A76 host would SIGILL on f16 kernels) and the supported model.
	if bn.Settings.BirdNET.Backend == conf.BackendPrefONNX {
		return false
	}
	if bn.ModelInfo.ID != DefaultModelVersion {
		return false // BirdNET v2.4 only for the PoC
	}
	return cpuspec.HasNativeF16()
}

// initializeOpenVINOModel loads the FP32 ONNX classifier via the OpenVINO
// backend. Returns a non-nil error on any failure so the caller falls back to
// ORT; it never panics, so a missing library or unsupported model cannot
// prevent startup.
func (bn *BirdNET) initializeOpenVINOModel() error {
	start := time.Now()
	log := GetLogger()
	settings := bn.Settings

	modelPath := bn.onnxModelPath()
	if modelPath == "" {
		return errors.Newf("OpenVINO classifier model path is empty").
			Category(errors.CategoryModelInit).Build()
	}
	rawPath := modelPath
	modelPath = os.ExpandEnv(modelPath)
	modelPath, err := conf.ExpandTildePath(modelPath)
	if err != nil {
		return errors.New(err).Category(errors.CategoryFileIO).Context("path", rawPath).Build()
	}

	if err := inference.InitOpenVINO(settings.BirdNET.OpenVINOPath); err != nil {
		return errors.New(err).Category(errors.CategoryModelInit).
			Context("openvino_path", settings.BirdNET.OpenVINOPath).
			Timing("openvino-init", time.Since(start)).Build()
	}

	classifier, err := inference.NewOpenVINOClassifier(modelPath, inference.OpenVINOClassifierOptions{
		Labels:  settings.BirdNET.Labels,
		Threads: settings.BirdNET.Threads,
	})
	if err != nil {
		return errors.New(err).Category(errors.CategoryModelInit).
			ModelContext(modelPath, bn.ModelInfo.ID).
			Timing("openvino-model-init", time.Since(start)).Build()
	}

	bn.classifier = classifier
	log.Info("OpenVINO model initialized",
		logger.String("model", modelPath),
		logger.Int("species", classifier.NumSpecies()))
	return nil
}
