// Package mapper provides conversion functions between domain models and database entities.
package mapper

import (
	"github.com/tphakala/birdnet-go/internal/datastore/entities"
	"github.com/tphakala/birdnet-go/internal/detection"
)

// DateFormat is the date layout used for database storage (YYYY-MM-DD).
const DateFormat = "2006-01-02"

// TimeFormat is the time layout used for database storage (HH:MM:SS).
const TimeFormat = "15:04:05"

// AdditionalResultToEntity converts a domain AdditionalResult to a database ResultsEntity.
func AdditionalResultToEntity(ar *detection.AdditionalResult, noteID uint) *entities.ResultsEntity {
	// Combine scientific and common name in the format expected by legacy code
	speciesStr := ar.Species.StoredLabel()

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

// LoadAdditionalResults populates the AdditionalResults from entity Results.
// This is called when loading from database with preloaded relations.
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
