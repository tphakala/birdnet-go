// Package repository provides V2 repository interfaces and implementations.
package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/detection"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// ConversionDeps contains dependencies needed for detection conversion.
type ConversionDeps struct {
	LabelRepo  LabelRepository
	ModelRepo  ModelRepository
	SourceRepo AudioSourceRepository
	Logger     logger.Logger

	// Cached lookup table IDs (must be initialized before use)
	SpeciesLabelTypeID uint // ID for "species" label type
	AvesClassID        uint // ID for "Aves" taxonomic class
	ChiropteraClassID  uint // ID for "Chiroptera" taxonomic class
}

// ConvertToV2Detection converts a domain Result to a v2 Detection entity.
// This is shared between DualWriteRepository and migration Worker.
// deps.SpeciesLabelTypeID, deps.AvesClassID, and deps.ChiropteraClassID must be initialized.
func ConvertToV2Detection(ctx context.Context, result *detection.Result, deps *ConversionDeps) (*entities.Detection, error) {
	// Get or create model first (needed for label creation)
	modelName := result.Model.Name
	if modelName == "" {
		modelName = detection.DefaultModelName
	}
	modelVersion := result.Model.Version
	if modelVersion == "" {
		modelVersion = detection.DefaultModelVersion
	}
	modelVariant := result.Model.Variant
	if modelVariant == "" {
		modelVariant = detection.DefaultModelVariant
	}

	model, err := deps.ModelRepo.GetOrCreate(ctx, modelName, modelVersion, modelVariant, entities.ModelTypeBird, result.Model.ClassifierPath)
	if err != nil {
		return nil, fmt.Errorf("model resolution failed: %w", err)
	}

	// Determine taxonomic class ID based on model type
	var taxonomicClassID *uint
	switch model.ModelType {
	case entities.ModelTypeBird:
		if deps.AvesClassID != 0 {
			taxonomicClassID = &deps.AvesClassID
		}
	case entities.ModelTypeBat:
		if deps.ChiropteraClassID != 0 {
			taxonomicClassID = &deps.ChiropteraClassID
		}
	case entities.ModelTypeMulti:
		// Multi-type models can detect multiple taxonomic classes; no default
	}

	// Resolve or create label (with model ID for model-specific labels)
	label, err := deps.LabelRepo.GetOrCreate(ctx, result.Species.ScientificName, model.ID, deps.SpeciesLabelTypeID, taxonomicClassID)
	if err != nil {
		return nil, fmt.Errorf("label resolution failed: %w", err)
	}

	// Get or create audio source
	var sourceID *uint
	if result.AudioSource.SafeString != "" {
		displayName := result.AudioSource.DisplayName
		source, err := deps.SourceRepo.GetOrCreate(ctx,
			result.AudioSource.SafeString,
			result.SourceNode,
			&displayName,
			entities.SourceType(result.AudioSource.Type))
		if err != nil {
			if deps.Logger != nil {
				deps.Logger.Warn("audio source resolution failed", logger.Error(err))
			}
			// Continue without source - not fatal
		} else {
			sourceID = &source.ID
		}
	}

	// Convert times
	var beginTime, endTime *int64
	if !result.BeginTime.IsZero() {
		bt := result.BeginTime.UnixMilli()
		beginTime = &bt
	}
	if !result.EndTime.IsZero() {
		et := result.EndTime.UnixMilli()
		endTime = &et
	}

	// Convert clip name
	var clipName *string
	if result.ClipName != "" {
		clipName = &result.ClipName
	}

	// Convert location
	var lat, lon *float64
	if result.Latitude != 0 {
		lat = &result.Latitude
	}
	if result.Longitude != 0 {
		lon = &result.Longitude
	}

	// Convert processing time
	var processingTimeMs *int64
	if result.ProcessingTime > 0 {
		pt := result.ProcessingTime.Milliseconds()
		processingTimeMs = &pt
	}

	legacyID := result.ID
	det := &entities.Detection{
		ID:               result.ID,
		LabelID:          label.ID,
		ModelID:          model.ID,
		SourceID:         sourceID,
		DetectedAt:       result.Timestamp.Unix(),
		BeginTime:        beginTime,
		EndTime:          endTime,
		Confidence:       result.Confidence,
		Latitude:         lat,
		Longitude:        lon,
		ClipName:         clipName,
		ProcessingTimeMs: processingTimeMs,
		LegacyID:         &legacyID,
	}

	return det, nil
}

// ConvertToPredictions converts additional results to v2 prediction entities.
// This is shared between DualWriteRepository and migration Worker.
// modelID and speciesLabelTypeID must be provided for label creation.
func ConvertToPredictions(ctx context.Context, detectionID, modelID, speciesLabelTypeID uint, taxonomicClassID *uint, additional []detection.AdditionalResult, labelRepo LabelRepository) ([]*entities.DetectionPrediction, error) {
	preds := make([]*entities.DetectionPrediction, 0, len(additional))

	for i, ar := range additional {
		label, err := labelRepo.GetOrCreate(ctx, ar.Species.ScientificName, modelID, speciesLabelTypeID, taxonomicClassID)
		if err != nil {
			return nil, fmt.Errorf("prediction label resolution failed: %w", err)
		}

		preds = append(preds, &entities.DetectionPrediction{
			DetectionID: detectionID,
			LabelID:     label.ID,
			Confidence:  ar.Confidence,
			Rank:        i + 2, // Primary is rank 1, additional start at 2
		})
	}

	return preds, nil
}

// ConvertFromV2Detection converts a v2 Detection entity to a domain Result.
func ConvertFromV2Detection(det *entities.Detection) *detection.Result {
	result := &detection.Result{
		ID:        det.ID,
		Timestamp: time.Unix(det.DetectedAt, 0),
	}

	// Convert label to species
	if det.Label != nil {
		result.Species = detection.Species{
			ScientificName: det.Label.ScientificName,
		}
	}

	// Convert model
	if det.Model != nil {
		result.Model = detection.ModelInfo{
			Name:           det.Model.Name,
			Version:        det.Model.Version,
			Variant:        det.Model.Variant,
			ClassifierPath: det.Model.ClassifierPath,
		}
	}

	// Convert source
	if det.Source != nil {
		displayName := ""
		if det.Source.DisplayName != nil {
			displayName = *det.Source.DisplayName
		}
		result.AudioSource = detection.AudioSource{
			SafeString:  det.Source.SourceURI,
			Type:        string(det.Source.SourceType),
			DisplayName: displayName,
		}
		result.SourceNode = det.Source.NodeName
	}

	// Convert times
	if det.BeginTime != nil {
		result.BeginTime = time.UnixMilli(*det.BeginTime)
	}
	if det.EndTime != nil {
		result.EndTime = time.UnixMilli(*det.EndTime)
	}

	// Convert confidence
	result.Confidence = det.Confidence

	// Convert location
	if det.Latitude != nil {
		result.Latitude = *det.Latitude
	}
	if det.Longitude != nil {
		result.Longitude = *det.Longitude
	}

	// Convert clip name
	if det.ClipName != nil {
		result.ClipName = *det.ClipName
	}

	// Convert processing time
	if det.ProcessingTimeMs != nil {
		result.ProcessingTime = time.Duration(*det.ProcessingTimeMs) * time.Millisecond
	}

	return result
}

// ConvertFromV2Detections converts multiple v2 Detection entities to domain Results.
func ConvertFromV2Detections(dets []*entities.Detection) []*detection.Result {
	results := make([]*detection.Result, 0, len(dets))
	for _, det := range dets {
		results = append(results, ConvertFromV2Detection(det))
	}
	return results
}
