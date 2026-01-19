// Package mapper provides conversion functions between domain models and database entities.
package mapper

import (
	"fmt"
	"time"

	"github.com/tphakala/birdnet-go/internal/datastore/entities"
	"github.com/tphakala/birdnet-go/internal/detection"
)

// Time format constants - match existing database format.
const (
	DateFormat = "2006-01-02"
	TimeFormat = "15:04:05"
)

// ResultToEntity converts a domain Result to a database NoteEntity.
func ResultToEntity(r *detection.Result) *entities.NoteEntity {
	return &entities.NoteEntity{
		ID:             r.ID,
		SourceNode:     r.SourceNode,
		Date:           r.Timestamp.Format(DateFormat),
		Time:           r.Timestamp.Format(TimeFormat),
		BeginTime:      r.BeginTime,
		EndTime:        r.EndTime,
		SpeciesCode:    r.Species.Code,
		ScientificName: r.Species.ScientificName,
		CommonName:     r.Species.CommonName,
		Confidence:     r.Confidence,
		Latitude:       r.Latitude,
		Longitude:      r.Longitude,
		Threshold:      r.Threshold,
		Sensitivity:    r.Sensitivity,
		ClipName:       r.ClipName,
		ProcessingTime: r.ProcessingTime,
	}
}

// EntityToResult converts a database NoteEntity to a domain Result.
// The timezone parameter is used to reconstruct the timestamp from date/time strings.
func EntityToResult(e *entities.NoteEntity, tz *time.Location) (*detection.Result, error) {
	// Parse date and time strings back to timestamp
	dateTime := e.Date + " " + e.Time
	timestamp, err := time.ParseInLocation(DateFormat+" "+TimeFormat, dateTime, tz)
	if err != nil {
		return nil, fmt.Errorf("failed to parse timestamp from date=%q time=%q: %w", e.Date, e.Time, err)
	}

	result := &detection.Result{
		ID:         e.ID,
		Timestamp:  timestamp,
		SourceNode: e.SourceNode,
		BeginTime:  e.BeginTime,
		EndTime:    e.EndTime,
		Species: detection.Species{
			ScientificName: e.ScientificName,
			CommonName:     e.CommonName,
			Code:           e.SpeciesCode,
		},
		Confidence:     e.Confidence,
		Latitude:       e.Latitude,
		Longitude:      e.Longitude,
		Threshold:      e.Threshold,
		Sensitivity:    e.Sensitivity,
		ClipName:       e.ClipName,
		ProcessingTime: e.ProcessingTime,
		Model:          detection.DefaultModelInfo(),
	}

	// Populate virtual fields from relations
	if e.Review != nil {
		result.Verified = e.Review.Verified
	}
	if e.Lock != nil {
		result.Locked = true
	}
	if len(e.Comments) > 0 {
		result.Comments = make([]detection.Comment, len(e.Comments))
		for i, c := range e.Comments {
			result.Comments[i] = detection.Comment{
				ID:        c.ID,
				Entry:     c.Entry,
				CreatedAt: c.CreatedAt,
				UpdatedAt: c.UpdatedAt,
			}
		}
	}

	return result, nil
}

// AdditionalResultToEntity converts a domain AdditionalResult to a database ResultsEntity.
func AdditionalResultToEntity(ar *detection.AdditionalResult, noteID uint) *entities.ResultsEntity {
	// Combine scientific and common name in the format expected by legacy code
	speciesStr := ar.Species.ScientificName + "_" + ar.Species.CommonName
	if ar.Species.Code != "" {
		speciesStr += "_" + ar.Species.Code
	}

	return &entities.ResultsEntity{
		NoteID:     noteID,
		Species:    speciesStr,
		Confidence: float32(ar.Confidence),
	}
}

// EntityToAdditionalResult converts a database ResultsEntity to a domain AdditionalResult.
func EntityToAdditionalResult(e *entities.ResultsEntity) *detection.AdditionalResult {
	return &detection.AdditionalResult{
		Species:    detection.ParseSpeciesString(e.Species),
		Confidence: float64(e.Confidence),
	}
}

// EntitiesToResults converts a slice of NoteEntity to a slice of domain Results.
func EntitiesToResults(noteEntities []*entities.NoteEntity, tz *time.Location) ([]*detection.Result, error) {
	results := make([]*detection.Result, 0, len(noteEntities))
	for _, e := range noteEntities {
		r, err := EntityToResult(e, tz)
		if err != nil {
			return nil, fmt.Errorf("failed to convert entity ID=%d: %w", e.ID, err)
		}
		results = append(results, r)
	}
	return results, nil
}

// LoadAdditionalResults populates the AdditionalResults from entity Results.
// This is called after EntityToResult when loading from database with preloaded relations.
func LoadAdditionalResults(e *entities.NoteEntity) []detection.AdditionalResult {
	if len(e.Results) == 0 {
		return nil
	}

	additional := make([]detection.AdditionalResult, len(e.Results))
	for i, r := range e.Results {
		ar := EntityToAdditionalResult(&r)
		additional[i] = *ar
	}
	return additional
}

// AdditionalResultsToEntities converts a slice of domain AdditionalResults to entities.
// Used when saving a detection with its secondary predictions.
func AdditionalResultsToEntities(results []detection.AdditionalResult, noteID uint) []entities.ResultsEntity {
	if len(results) == 0 {
		return nil
	}

	resultEntities := make([]entities.ResultsEntity, len(results))
	for i := range results {
		e := AdditionalResultToEntity(&results[i], noteID)
		resultEntities[i] = *e
	}
	return resultEntities
}
