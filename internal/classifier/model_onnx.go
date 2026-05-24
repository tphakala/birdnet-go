package classifier

import (
	"os"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/inference"
	onnx "github.com/tphakala/birdnet-go/internal/inference/onnx"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// initializeONNXModel loads and initializes an ONNX model as the classifier backend.
func (bn *BirdNET) initializeONNXModel() error {
	start := time.Now()
	log := GetLogger()

	// Pre-check ORT availability before attempting model load.
	ortStatus := inference.CheckORTAvailability(bn.Settings.BirdNET.ONNXRuntimePath)
	if !ortStatus.Available {
		log.Warn("ONNX classifier requires ONNX Runtime which is not available",
			logger.String("error", ortStatus.Error))
		emitORTUnavailableNotification("BirdNET ONNX Classifier", ortStatus.Error)
		return errors.Newf("ONNX classifier requires ONNX Runtime %s: %s",
			inference.ORTRequiredVersion(), ortStatus.Error).
			Category(errors.CategoryModelInit).
			Context("model", "onnx_classifier").
			Context("ort_error", ortStatus.Error).
			Timing("ort-check", time.Since(start)).
			Build()
	}

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
// For v3 geomodel, delegates to initializeV3GeoModel which loads the geomodel
// with its own 12K labels and wraps it in a mappedRangeFilter.
func (bn *BirdNET) initializeONNXMetaModel() error {
	settings := bn.currentSettings()
	log := GetLogger()
	start := time.Now()

	ortStatus := inference.CheckORTAvailability(settings.BirdNET.ONNXRuntimePath)
	if !ortStatus.Available {
		modelCtx := "range_filter"
		modelName := "ONNX range filter"
		notifName := "BirdNET Range Filter"
		if settings.BirdNET.RangeFilter.Model == "v3" {
			modelCtx = "v3_geomodel"
			modelName = "v3 geomodel"
			notifName = "BirdNET Geomodel"
		}
		log.Warn(modelName+" requires ONNX Runtime which is not available",
			logger.String("error", ortStatus.Error))
		emitORTUnavailableNotification(notifName, ortStatus.Error)
		return errors.Newf("%s requires ONNX Runtime %s: %s",
			modelName, inference.ORTRequiredVersion(), ortStatus.Error).
			Category(errors.CategoryModelInit).
			Context("model", modelCtx).
			Context("ort_error", ortStatus.Error).
			Timing("ort-check", time.Since(start)).
			Build()
	}

	if settings.BirdNET.RangeFilter.Model == "v3" {
		return bn.initializeV3GeoModel()
	}

	// Ensure ONNX Runtime is initialized (idempotent - may already be init from classifier)
	if err := inference.InitONNXRuntime(settings.BirdNET.ONNXRuntimePath); err != nil {
		return errors.New(err).
			Category(errors.CategoryModelInit).
			Context("onnx_runtime_path", settings.BirdNET.ONNXRuntimePath).
			Timing("onnx-init", time.Since(start)).
			Build()
	}

	rangeFilter, err := inference.NewONNXRangeFilter(
		settings.BirdNET.RangeFilter.ModelPath,
		inference.ONNXRangeFilterOptions{
			Labels: settings.BirdNET.Labels,
		},
	)
	if err != nil {
		return errors.New(err).
			Category(errors.CategoryModelInit).
			Context("model_type", "range_filter").
			Context("range_filter_model", settings.BirdNET.RangeFilter.ModelPath).
			Timing("onnx-meta-model-init", time.Since(start)).
			Build()
	}

	bn.rangeFilter = rangeFilter
	return nil
}

// initializeV3GeoModel loads the v3.0 geomodel ONNX with its own 12K labels,
// then wraps the raw ONNX range filter in a mappedRangeFilter that remaps
// geomodel output scores to the classifier's label order by matching scientific
// names. This enables the 12K-species geomodel to work with any classifier.
func (bn *BirdNET) initializeV3GeoModel() error {
	start := time.Now()
	log := GetLogger()
	settings := bn.currentSettings()
	rfSettings := settings.BirdNET.RangeFilter

	log.Info("V3 geomodel: starting initialization",
		logger.String("model_path", rfSettings.ModelPath),
		logger.String("labels_path", rfSettings.LabelsPath),
		logger.Int("classifier_labels", len(settings.BirdNET.Labels)))

	if rfSettings.ModelPath == "" {
		return errors.Newf("v3 geomodel requires rangefilter.modelpath to be set").
			Category(errors.CategoryModelInit).
			Context("model", "v3").
			Build()
	}
	if rfSettings.LabelsPath == "" {
		return errors.Newf("v3 geomodel requires rangefilter.labelspath to be set").
			Category(errors.CategoryModelInit).
			Context("model", "v3").
			Build()
	}

	// Expand environment variables and ~ prefix in paths (consistent with getMetaModelData)
	modelPath := os.ExpandEnv(rfSettings.ModelPath)
	modelPath, err := conf.ExpandTildePath(modelPath)
	if err != nil {
		return errors.New(err).
			Category(errors.CategoryFileIO).
			Context("path", rfSettings.ModelPath).
			Build()
	}

	labelsPath := os.ExpandEnv(rfSettings.LabelsPath)
	labelsPath, err = conf.ExpandTildePath(labelsPath)
	if err != nil {
		return errors.New(err).
			Category(errors.CategoryFileIO).
			Context("path", rfSettings.LabelsPath).
			Build()
	}

	// Ensure ONNX Runtime is initialized (ORT availability checked by initializeONNXMetaModel)
	log.Debug("V3 geomodel: initializing ONNX Runtime")
	if err := inference.InitONNXRuntime(settings.BirdNET.ONNXRuntimePath); err != nil {
		return errors.New(err).
			Category(errors.CategoryModelInit).
			Context("onnx_runtime_path", settings.BirdNET.ONNXRuntimePath).
			Timing("onnx-init", time.Since(start)).
			Build()
	}

	// Load geomodel labels from file
	log.Debug("V3 geomodel: loading labels", logger.String("path", labelsPath))
	geoLabels, err := onnx.LoadLabels(labelsPath)
	if err != nil {
		return errors.New(err).
			Category(errors.CategoryModelInit).
			Context("model_type", "v3_geomodel").
			Context("labels_path", labelsPath).
			Build()
	}

	if len(geoLabels) == 0 {
		return errors.Newf("v3 geomodel labels file is empty: %s", labelsPath).
			Category(errors.CategoryModelInit).
			Context("model_type", "v3_geomodel").
			Build()
	}

	log.Debug("V3 geomodel: loaded labels",
		logger.Int("count", len(geoLabels)),
		logger.String("first", geoLabels[0]))

	// Create ONNX range filter using the geomodel's own labels
	log.Debug("V3 geomodel: creating ONNX range filter", logger.String("model_path", modelPath))
	innerFilter, err := inference.NewONNXRangeFilter(
		modelPath,
		inference.ONNXRangeFilterOptions{
			Labels: geoLabels,
		},
	)
	if err != nil {
		return errors.New(err).
			Category(errors.CategoryModelInit).
			Context("model_type", "v3_geomodel").
			Context("range_filter_model", modelPath).
			Timing("onnx-v3-geomodel-init", time.Since(start)).
			Build()
	}

	classifierLabels := settings.BirdNET.Labels
	var unmappedScore float32
	if rfSettings.PassUnmappedSpecies {
		unmappedScore = 1.0
	}
	mapped := newMappedRangeFilter(innerFilter, classifierLabels, geoLabels, unmappedScore)

	if mapped.mappedCount == 0 && len(classifierLabels) > 0 {
		log.Warn("V3 geomodel: no species matched classifier labels, range filter will filter out all detections (check labels file)",
			logger.Int("classifier_species", len(classifierLabels)),
			logger.String("labels_path", labelsPath))
	}
	log.Info("V3 geomodel initialized with species mapping",
		logger.Int("geomodel_species", len(geoLabels)),
		logger.Int("classifier_species", len(classifierLabels)),
		logger.Int("mapped_species", mapped.mappedCount),
		logger.Int("unmapped_species", len(classifierLabels)-mapped.mappedCount),
		logger.String("duration", time.Since(start).String()))

	bn.rangeFilter = mapped
	return nil
}
