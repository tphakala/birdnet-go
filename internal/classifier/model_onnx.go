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
	settings := bn.currentSettings()

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

	classifier, err := inference.NewONNXClassifier(settings.BirdNET.ModelPath, inference.ONNXClassifierOptions{
		Labels:  settings.BirdNET.Labels,
		Threads: settings.BirdNET.Threads,
	})
	if err != nil {
		return errors.New(err).
			Category(errors.CategoryModelInit).
			ModelContext(settings.BirdNET.ModelPath, bn.ModelInfo.ID).
			Timing("onnx-model-init", time.Since(start)).
			Build()
	}

	bn.classifier = classifier

	log.Info("ONNX model initialized",
		logger.String("model", settings.BirdNET.ModelPath),
		logger.Int("species", classifier.NumSpecies()))

	return nil
}

// initializeONNXMetaModel loads and initializes an ONNX range filter meta model from
// the given resolved settings. A geomodel-shaped config (model=="v3" or an ONNX model
// path paired with a companion labels file) delegates to initializeMappedGeoModel, which
// loads the geomodel with its own labels and wraps it in a mappedRangeFilter that scores
// by scientific name. An ONNX model path without a companion labels file uses the strict
// path, where the model output dimension must match the classifier label count.
func (bn *BirdNET) initializeONNXMetaModel(settings *conf.Settings) error {
	start := time.Now()
	rf := settings.BirdNET.RangeFilter
	mapped := resolveRangeFilterBackend(&rf) == rangeFilterBackendMappedGeomodel

	modelName := "ONNX range filter"
	modelCtx := "range_filter"
	switch {
	case rf.Model == "v3":
		modelName = "v3 geomodel"
		modelCtx = "v3_geomodel"
	case mapped:
		modelName = "geomodel range filter"
		modelCtx = "geomodel"
	}
	if err := checkORTOrFail(settings.BirdNET.ONNXRuntimePath, modelName, modelCtx, ""); err != nil {
		return err
	}

	if mapped {
		return bn.initializeMappedGeoModel(settings)
	}

	// Strict path: no companion labels file, so the classifier labels must match the
	// model output dimension one-to-one.
	// Ensure ONNX Runtime is initialized (idempotent - may already be init from classifier)
	if err := inference.InitONNXRuntime(settings.BirdNET.ONNXRuntimePath); err != nil {
		return errors.New(err).
			Category(errors.CategoryModelInit).
			Context("onnx_runtime_path", settings.BirdNET.ONNXRuntimePath).
			Timing("onnx-init", time.Since(start)).
			Build()
	}

	rangeFilter, err := inference.NewONNXRangeFilter(
		rf.ModelPath,
		inference.ONNXRangeFilterOptions{
			Labels: settings.BirdNET.Labels,
		},
	)
	if err != nil {
		return errors.New(err).
			Category(errors.CategoryModelInit).
			Context("model_type", "range_filter").
			Context("range_filter_model", rf.ModelPath).
			Timing("onnx-meta-model-init", time.Since(start)).
			Build()
	}

	bn.rangeFilter = rangeFilter
	return nil
}

// initializeMappedGeoModel loads the geomodel ONNX with its own labels (e.g. 12K
// species), then wraps the raw ONNX range filter in a mappedRangeFilter that remaps
// geomodel output scores to the classifier's label order by matching scientific names.
// This enables the geomodel to work with any classifier and makes a label-count
// difference a name-matching problem rather than a fatal LabelCountError. Used for both
// the explicit v3 config and orphaned geomodel configs (model=="" with a labels file).
func (bn *BirdNET) initializeMappedGeoModel(settings *conf.Settings) error {
	start := time.Now()
	log := GetLogger()
	rfSettings := settings.BirdNET.RangeFilter

	log.Info("Geomodel range filter: starting initialization",
		logger.String("model", rfSettings.Model),
		logger.String("model_path", rfSettings.ModelPath),
		logger.String("labels_path", rfSettings.LabelsPath),
		logger.Int("classifier_labels", len(settings.BirdNET.Labels)))

	if rfSettings.ModelPath == "" {
		return errors.Newf("geomodel range filter requires rangefilter.modelpath to be set").
			Category(errors.CategoryModelInit).
			Context("model_type", "geomodel").
			Build()
	}
	if rfSettings.LabelsPath == "" {
		return errors.Newf("geomodel range filter requires rangefilter.labelspath to be set").
			Category(errors.CategoryModelInit).
			Context("model_type", "geomodel").
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
	log.Debug("Geomodel range filter: initializing ONNX Runtime")
	if err := inference.InitONNXRuntime(settings.BirdNET.ONNXRuntimePath); err != nil {
		return errors.New(err).
			Category(errors.CategoryModelInit).
			Context("onnx_runtime_path", settings.BirdNET.ONNXRuntimePath).
			Timing("onnx-init", time.Since(start)).
			Build()
	}

	// Load geomodel labels from file
	log.Debug("Geomodel range filter: loading labels", logger.String("path", labelsPath))
	geoLabels, err := onnx.LoadLabels(labelsPath)
	if err != nil {
		return errors.New(err).
			Category(errors.CategoryModelInit).
			Context("model_type", "geomodel").
			Context("labels_path", labelsPath).
			Build()
	}

	if len(geoLabels) == 0 {
		return errors.Newf("geomodel labels file is empty: %s", labelsPath).
			Category(errors.CategoryModelInit).
			Context("model_type", "geomodel").
			Build()
	}

	log.Debug("Geomodel range filter: loaded labels",
		logger.Int("count", len(geoLabels)),
		logger.String("first", geoLabels[0]))

	// Create ONNX range filter using the geomodel's own labels
	log.Debug("Geomodel range filter: creating ONNX range filter", logger.String("model_path", modelPath))
	innerFilter, err := inference.NewONNXRangeFilter(
		modelPath,
		inference.ONNXRangeFilterOptions{
			Labels: geoLabels,
		},
	)
	if err != nil {
		return errors.New(err).
			Category(errors.CategoryModelInit).
			Context("model_type", "geomodel").
			Context("range_filter_model", modelPath).
			Timing("onnx-geomodel-init", time.Since(start)).
			Build()
	}

	classifierLabels := settings.BirdNET.Labels
	var unmappedScore float32
	if rfSettings.PassUnmappedSpecies {
		unmappedScore = 1.0
	}
	mapped := newMappedRangeFilter(innerFilter, classifierLabels, geoLabels, unmappedScore)

	if mapped.mappedCount == 0 && len(classifierLabels) > 0 {
		log.Warn("Geomodel range filter: no species matched classifier labels, range filter will filter out all detections (check labels file)",
			logger.Int("classifier_species", len(classifierLabels)),
			logger.String("labels_path", labelsPath))
	}
	log.Info("Geomodel range filter initialized with species mapping",
		logger.Int("geomodel_species", len(geoLabels)),
		logger.Int("classifier_species", len(classifierLabels)),
		logger.Int("mapped_species", mapped.mappedCount),
		logger.Int("unmapped_species", len(classifierLabels)-mapped.mappedCount),
		logger.String("duration", time.Since(start).String()))

	bn.rangeFilter = mapped
	return nil
}
