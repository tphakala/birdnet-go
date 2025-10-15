package detection

import (
	"strings"

	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/observation"
)

// Mapper converts between domain models (Detection) and database entities (datastore.Note).
// This separation allows the database schema to evolve independently from the runtime model.
type Mapper struct {
	speciesCache *SpeciesCache
}

// NewMapper creates a new mapper with optional species cache.
func NewMapper(cache *SpeciesCache) *Mapper {
	return &Mapper{
		speciesCache: cache,
	}
}

// ToDatastore converts a Detection domain model to a datastore.Note entity for persistence.
// Note: Some fields are runtime-only and are not persisted:
//   - Source (AudioSource): Only SourceNode is saved
//   - Occurrence: Calculated field, not persisted
func (m *Mapper) ToDatastore(d *Detection) datastore.Note {
	return datastore.Note{
		ID:             d.ID,
		SourceNode:     d.SourceNode,
		Date:           d.Date,
		Time:           d.Time,
		BeginTime:      d.BeginTime,
		EndTime:        d.EndTime,
		SpeciesCode:    d.SpeciesCode,
		ScientificName: d.ScientificName,
		CommonName:     d.CommonName,
		Confidence:     d.Confidence,
		Threshold:      d.Threshold,
		Sensitivity:    d.Sensitivity,
		Latitude:       d.Latitude,
		Longitude:      d.Longitude,
		ClipName:       d.ClipName,
		ProcessingTime: d.ProcessingTime,
		// Source is NOT saved (runtime metadata only)
		// Occurrence is NOT saved (calculated field)
		// Verified and Locked are virtual fields populated from relationships
	}
}

// FromDatastore converts a datastore.Note entity to a Detection domain model.
// The AudioSource must be provided separately as it's not persisted in the database.
func (m *Mapper) FromDatastore(note *datastore.Note, source AudioSource) *Detection {
	detection := &Detection{
		ID:             note.ID,
		SourceNode:     note.SourceNode,
		Date:           note.Date,
		Time:           note.Time,
		BeginTime:      note.BeginTime,
		EndTime:        note.EndTime,
		Source:         source, // Injected from context
		SpeciesCode:    note.SpeciesCode,
		ScientificName: note.ScientificName,
		CommonName:     note.CommonName,
		Confidence:     note.Confidence,
		Threshold:      note.Threshold,
		Sensitivity:    note.Sensitivity,
		Latitude:       note.Latitude,
		Longitude:      note.Longitude,
		ClipName:       note.ClipName,
		ProcessingTime: note.ProcessingTime,
		Occurrence:     note.Occurrence, // Copied if present (not persisted)
		Verified:       note.Verified,   // Virtual field from relationship
		Locked:         note.Locked,     // Virtual field from relationship
	}

	// Create Species object if we have the data
	if note.ScientificName != "" {
		detection.Species = &Species{
			SpeciesCode:    note.SpeciesCode,
			ScientificName: note.ScientificName,
			CommonName:     note.CommonName,
		}
	}

	return detection
}

// ToPredictionEntities converts domain Prediction objects to datastore.Results entities.
// The detectionID must be provided as it's the foreign key.
func (m *Mapper) ToPredictionEntities(detectionID uint, predictions []Prediction) []datastore.Results {
	results := make([]datastore.Results, len(predictions))
	for i, p := range predictions {
		// Format species as "ScientificName_CommonName" to match current format
		speciesStr := p.Species.ScientificName + "_" + p.Species.CommonName
		if p.Species.SpeciesCode != "" {
			speciesStr += "_" + p.Species.SpeciesCode
		}

		results[i] = datastore.Results{
			NoteID:     detectionID,
			Species:    speciesStr,
			Confidence: float32(p.Confidence),
		}
	}
	return results
}

// FromPredictionEntities converts datastore.Results entities to domain Prediction objects.
func (m *Mapper) FromPredictionEntities(results []datastore.Results) []Prediction {
	predictions := make([]Prediction, len(results))
	for i, r := range results {
		// Parse species string using observation package parser
		scientificName, commonName, speciesCode := observation.ParseSpeciesString(r.Species)

		predictions[i] = Prediction{
			Species: &Species{
				SpeciesCode:    speciesCode,
				ScientificName: scientificName,
				CommonName:     commonName,
			},
			Confidence: float64(r.Confidence),
			Rank:       i + 1, // 1-indexed rank
		}
	}
	return predictions
}

// FromDatastoreBatch converts multiple datastore.Note entities to Detection domain models.
// This is more efficient than calling FromDatastore in a loop when source is the same.
func (m *Mapper) FromDatastoreBatch(notes []datastore.Note, source AudioSource) []*Detection {
	detections := make([]*Detection, len(notes))
	for i := range notes {
		detections[i] = m.FromDatastore(&notes[i], source)
	}
	return detections
}

// ToDatastoreBatch converts multiple Detection domain models to datastore.Note entities.
func (m *Mapper) ToDatastoreBatch(detections []*Detection) []datastore.Note {
	notes := make([]datastore.Note, len(detections))
	for i, d := range detections {
		notes[i] = m.ToDatastore(d)
	}
	return notes
}

// parseSpeciesString parses a species string in various formats.
// Formats supported:
//   - "ScientificName_CommonName_SpeciesCode"
//   - "ScientificName_CommonName"
//   - "CommonName" (fallback)
//
// This is a helper function that wraps observation.ParseSpeciesString
// but is kept here for potential future enhancements.
func parseSpeciesString(species string) *Species {
	scientificName, commonName, speciesCode := observation.ParseSpeciesString(species)

	// Handle edge cases
	if scientificName == "" && commonName == "" {
		// Fallback: treat entire string as common name
		commonName = species
	}

	return &Species{
		SpeciesCode:    speciesCode,
		ScientificName: scientificName,
		CommonName:     commonName,
	}
}

// normalizeSpeciesString ensures species string is in canonical format.
// This helps with caching and comparison.
func normalizeSpeciesString(scientific, common, code string) string {
	parts := []string{}

	if scientific != "" {
		parts = append(parts, scientific)
	}
	if common != "" {
		parts = append(parts, common)
	}
	if code != "" {
		parts = append(parts, code)
	}

	return strings.Join(parts, "_")
}
