// Package repository provides V2 repository interfaces and implementations.
package repository

import (
	"context"
	"fmt"

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
// The migration Worker uses it to copy legacy records into v2.
// deps.SpeciesLabelTypeID, deps.AvesClassID, and deps.ChiropteraClassID must be initialized.
func ConvertToV2Detection(ctx context.Context, result *detection.Result, deps *ConversionDeps) (*entities.Detection, error) {
	// Get or create model first (needed for label creation)
	m := result.Model.WithDefaults()
	model, err := deps.ModelRepo.GetOrCreate(ctx, m.Name, m.Version, m.Variant, detection.ResolveModelType(m.Name, m.Version), m.ClassifierPath)
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
	var clipSampleRate *int
	if result.ClipName != "" {
		clipName = &result.ClipName
		if result.ClipSampleRate > 0 {
			clipSampleRate = &result.ClipSampleRate
		}
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
		ClipSampleRate:   clipSampleRate,
		ProcessingTimeMs: processingTimeMs,
		Unlikely:         result.Unlikely,
		LegacyID:         &legacyID,
	}

	return det, nil
}
