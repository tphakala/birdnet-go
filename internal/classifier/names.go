// names.go provides common name resolution for multi-model support.
package classifier

import "strings"

// BirdNETLabelResolver resolves scientific names to common names using
// BirdNET's loaded label list. BirdNET labels use "ScientificName_CommonName"
// format. The resolver builds an index at construction time for O(1) lookups.
//
// Implements NameResolver.
type BirdNETLabelResolver struct {
	// index maps lowercase scientific name → common name
	index map[string]string
}

// NewBirdNETLabelResolver creates a resolver from BirdNET's label list.
// Labels must be in "ScientificName_CommonName" format.
func NewBirdNETLabelResolver(labels []string) *BirdNETLabelResolver {
	index := make(map[string]string, len(labels))
	for _, label := range labels {
		scientific, common := SplitSpeciesName(label)
		if scientific != "" && common != "" {
			index[strings.ToLower(scientific)] = common
		}
	}
	return &BirdNETLabelResolver{index: index}
}

// Resolve returns the common name for a scientific name.
// The locale parameter is accepted for interface compliance but currently unused
// (BirdNET labels are locale-specific at load time, not at resolve time).
// Returns empty string if the species is not found.
func (r *BirdNETLabelResolver) Resolve(scientificName, _ string) string {
	if r.index == nil {
		return ""
	}
	return r.index[strings.ToLower(scientificName)]
}
