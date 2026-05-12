package classifier

import (
	"strings"

	"github.com/tphakala/birdnet-go/internal/inference"
)

// mappedRangeFilter wraps an inference.RangeFilter whose output indices correspond
// to a different label set (the geomodel's own labels) and remaps the scores to
// align with the classifier's label order. This enables the v3.0 geomodel (12K
// species) to work with any classifier (BirdNET v2.4, v3.0, Perch v2) without
// changing predictFilter() or any downstream code.
type mappedRangeFilter struct {
	inner           inference.RangeFilter
	classifierToGeo []int   // classifierIndex -> geomodelIndex; -1 means no match
	numClassifier   int     // len(classifierLabels)
	mappedCount     int     // number of classifier species with a geomodel match
	unmappedScore   float32 // score for classifier species absent from geomodel
}

// buildSpeciesMapping creates the classifier-to-geomodel index mapping by
// matching scientific names (the part before the first underscore in
// "ScientificName_CommonName" labels). The match is case-insensitive.
func buildSpeciesMapping(classifierLabels, geomodelLabels []string) []int {
	geoIndex := make(map[string]int, len(geomodelLabels))
	for i, label := range geomodelLabels {
		sci := extractScientificName(label)
		geoIndex[strings.ToLower(sci)] = i
	}

	mapping := make([]int, len(classifierLabels))
	for i, label := range classifierLabels {
		sci := extractScientificName(label)
		if idx, ok := geoIndex[strings.ToLower(sci)]; ok {
			mapping[i] = idx
		} else {
			mapping[i] = -1
		}
	}
	return mapping
}

// extractScientificName returns the scientific name portion from a
// "ScientificName_CommonName" label string.
func extractScientificName(label string) string {
	if idx := strings.IndexByte(label, '_'); idx >= 0 {
		return label[:idx]
	}
	return label
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
	return &mappedRangeFilter{
		inner:           inner,
		classifierToGeo: mapping,
		numClassifier:   len(classifierLabels),
		mappedCount:     mapped,
		unmappedScore:   unmappedScore,
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

// NumSpecies returns the number of classifier labels (not geomodel labels).
func (m *mappedRangeFilter) NumSpecies() int {
	return m.numClassifier
}

// Close releases resources held by the underlying range filter.
func (m *mappedRangeFilter) Close() {
	m.inner.Close()
}
