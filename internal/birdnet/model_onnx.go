//go:build onnx

package birdnet

import (
	"time"

	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/inference"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// initializeONNXModel loads and initializes an ONNX model as the classifier backend.
func (bn *BirdNET) initializeONNXModel() error {
	start := time.Now()
	log := GetLogger()

	// Initialize ONNX Runtime if not already done
	if err := inference.InitONNXRuntime(bn.Settings.BirdNET.ONNXRuntimePath); err != nil {
		return errors.New(err).
			Category(errors.CategoryModelInit).
			Context("onnx_runtime_path", bn.Settings.BirdNET.ONNXRuntimePath).
			Timing("onnx-init", time.Since(start)).
			Build()
	}

	classifier, err := inference.NewONNXClassifier(bn.Settings.BirdNET.ModelPath, inference.ONNXClassifierOptions{
		Labels:  bn.Settings.BirdNET.Labels,
		Threads: bn.Settings.BirdNET.Threads,
	})
	if err != nil {
		return errors.New(err).
			Category(errors.CategoryModelInit).
			ModelContext(bn.Settings.BirdNET.ModelPath, bn.ModelInfo.ID).
			Timing("onnx-model-init", time.Since(start)).
			Build()
	}

	bn.classifier = classifier

	log.Info("ONNX model initialized",
		logger.String("model", bn.Settings.BirdNET.ModelPath),
		logger.Int("species", classifier.NumSpecies()))

	return nil
}

// initializeONNXMetaModel loads and initializes an ONNX range filter meta model.
func (bn *BirdNET) initializeONNXMetaModel() error {
	start := time.Now()

	// Ensure ONNX Runtime is initialized (idempotent — may already be init from classifier)
	if err := inference.InitONNXRuntime(bn.Settings.BirdNET.ONNXRuntimePath); err != nil {
		return errors.New(err).
			Category(errors.CategoryModelInit).
			Context("onnx_runtime_path", bn.Settings.BirdNET.ONNXRuntimePath).
			Timing("onnx-init", time.Since(start)).
			Build()
	}

	rangeFilter, err := inference.NewONNXRangeFilter(
		bn.Settings.BirdNET.RangeFilter.ModelPath,
		inference.ONNXRangeFilterOptions{
			Labels: bn.Settings.BirdNET.Labels,
		},
	)
	if err != nil {
		return errors.New(err).
			Category(errors.CategoryModelInit).
			Context("model_type", "range_filter").
			Context("range_filter_model", bn.Settings.BirdNET.RangeFilter.ModelPath).
			Timing("onnx-meta-model-init", time.Since(start)).
			Build()
	}

	bn.rangeFilter = rangeFilter
	return nil
}

// isONNXSupported returns true when the binary is built with ONNX support.
func isONNXSupported() bool {
	return true
}
