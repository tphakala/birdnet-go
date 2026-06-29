package classifier

import (
	"strings"

	"github.com/tphakala/birdnet-go/internal/detection"
	"github.com/tphakala/birdnet-go/internal/inference"
	"github.com/tphakala/birdnet-go/internal/openfauna"
)

// mappedRangeFilter wraps an inference.RangeFilter whose output indices correspond
// to a different label set (the geomodel's own labels) and remaps the scores to
// align with the classifier's label order. This enables the v3.0 geomodel (12K
// species) to work with any classifier (BirdNET v2.4, v3.0, Perch v2) without
// changing predictFilter() or any downstream code.
type mappedRangeFilter struct {
	inner           inference.RangeFilter
	classifierToGeo []int          // classifierIndex -> geomodelIndex; -1 means no match
	numClassifier   int            // len(classifierLabels)
	mappedCount     int            // number of classifier species with a geomodel match
	unmappedScore   float32        // score for classifier species absent from geomodel
	geomodelLabels  []string       // geomodel label set in geomodel output order
	geomodelIndex   map[string]int // label -> index for O(1) lookup
}

// canonicalSpeciesKey returns the match key for a model label: its scientific
// name (the part before the first underscore in "ScientificName_CommonName"),
// resolved through the OpenFauna taxonomic alias map and lowercased. Normalizing
// both the geomodel and the classifier sides to the canonical name lets a legacy
// classifier label (e.g. BirdNET v2.4 "Streptopelia senegalensis") match the
// geomodel's current name for the same taxon ("Spilopelia senegalensis"), instead
// of being treated as unmapped and silently filtered out when the geomodel uses a
// newer taxonomy than the classifier. A non-aliased name resolves to itself, so
// this is a no-op for species without a reclassification.
func canonicalSpeciesKey(label string) string {
	sci := detection.ExtractScientificName(label)
	return strings.ToLower(openfauna.CanonicalName(sci))
}

// buildSpeciesMapping creates the classifier-to-geomodel index mapping by
// matching scientific names. Matching is case-insensitive and alias-aware (see
// canonicalSpeciesKey).
func buildSpeciesMapping(classifierLabels, geomodelLabels []string) []int {
	geoIndex := make(map[string]int, len(geomodelLabels))
	for i, label := range geomodelLabels {
		geoIndex[canonicalSpeciesKey(label)] = i
	}

	mapping := make([]int, len(classifierLabels))
	for i, label := range classifierLabels {
		if idx, ok := geoIndex[canonicalSpeciesKey(label)]; ok {
			mapping[i] = idx
		} else {
			mapping[i] = -1
		}
	}
	return mapping
}

// ComputeGeomodelCoverage counts how many classifier species have a matching
// scientific name in the geomodel label set. Returns (withRangeData, withoutRangeData).
func ComputeGeomodelCoverage(classifierLabels, geomodelLabels []string) (withRangeData, withoutRangeData int) {
	mapping := buildSpeciesMapping(classifierLabels, geomodelLabels)
	for _, idx := range mapping {
		if idx >= 0 {
			withRangeData++
		} else {
			withoutRangeData++
		}
	}
	return withRangeData, withoutRangeData
}

// newMappedRangeFilter wraps inner with a species mapping layer.
// classifierLabels is the label set used by the active classifier model.
// geomodelLabels is the label set matching the geomodel's output order.
// unmappedScore is the score assigned to classifier species that have no
// match in the geomodel (0.0 = filter out, 1.0 = pass through).
func newMappedRangeFilter(inner inference.RangeFilter, classifierLabels, geomodelLabels []string, unmappedScore float32) *mappedRangeFilter {
	mapping := buildSpeciesMapping(classifierLabels, geomodelLabels)
	mapped := 0
	for _, idx := range mapping {
		if idx >= 0 {
			mapped++
		}
	}
	geoIdx := make(map[string]int, len(geomodelLabels))
	for i, label := range geomodelLabels {
		geoIdx[label] = i
	}

	return &mappedRangeFilter{
		inner:           inner,
		classifierToGeo: mapping,
		numClassifier:   len(classifierLabels),
		mappedCount:     mapped,
		unmappedScore:   unmappedScore,
		geomodelLabels:  geomodelLabels,
		geomodelIndex:   geoIdx,
	}
}

// Predict returns species scores aligned to the classifier's label order.
func (m *mappedRangeFilter) Predict(latitude, longitude, week float32) ([]float32, error) {
	geoScores, err := m.inner.Predict(latitude, longitude, week)
	if err != nil {
		return nil, err
	}

	scores := make([]float32, m.numClassifier)
	for i, geoIdx := range m.classifierToGeo {
		if geoIdx >= 0 && geoIdx < len(geoScores) {
			scores[i] = geoScores[geoIdx]
		} else {
			scores[i] = m.unmappedScore
		}
	}
	return scores, nil
}

// PredictSpeciesScores returns geomodel species whose predicted score meets or
// exceeds threshold, together with their raw scores. The returned labels come
// from the geomodel's own label set (not the classifier's labels), so the
// result covers all species the geomodel knows about regardless of which
// classifier is active.
func (m *mappedRangeFilter) PredictSpeciesScores(lat, lon, week, threshold float32) ([]SpeciesScore, error) {
	geoScores, err := m.inner.Predict(lat, lon, week)
	if err != nil {
		return nil, err
	}
	result := make([]SpeciesScore, 0, len(m.geomodelLabels)/4)
	for i, score := range geoScores {
		if score >= threshold && i < len(m.geomodelLabels) {
			result = append(result, SpeciesScore{Score: float64(score), Label: m.geomodelLabels[i]})
		}
	}
	return result, nil
}

// GeomodelLabels returns the full geomodel label set for use in species
// override matching, where the caller needs to search all known species
// (not just those passing the range filter threshold).
func (m *mappedRangeFilter) GeomodelLabels() []string {
	return m.geomodelLabels
}

// NumSpecies returns the number of classifier labels (not geomodel labels).
func (m *mappedRangeFilter) NumSpecies() int {
	return m.numClassifier
}

// Close releases resources held by the underlying range filter.
func (m *mappedRangeFilter) Close() {
	m.inner.Close()
}
