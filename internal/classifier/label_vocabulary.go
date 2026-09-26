package classifier

// LabelVocabulary is an immutable label set with its canonical-key memo, built
// once when a range-filter view is constructed so no request path calls
// openfauna.CanonicalName per geomodel label. A nil *LabelVocabulary is a valid
// "no vocabulary" value: CanonicalKey still computes, HasCanonical reports false.
//
// The maps are never mutated after NewLabelVocabulary returns, so a
// *LabelVocabulary is safe to share across goroutines (it rides on the
// immutable range-filter snapshot the same way the label slices already do).
type LabelVocabulary struct {
	// Labels is the label set in its original (geomodel output) order.
	Labels []string
	// CanonicalByLabel maps each label to its canonical species key
	// (canonicalSpeciesKey), memoized so the request path never recomputes it.
	CanonicalByLabel map[string]string
	// LabelsByCanonical maps a canonical key to every label sharing it, in input
	// order, so a coverage check is an O(1) map lookup.
	LabelsByCanonical map[string][]string
}

// NewLabelVocabulary builds a vocabulary from labels, computing canonicalSpeciesKey
// once per label. It never returns nil; an empty input yields an empty (non-nil)
// vocabulary. The Labels slice is retained by reference (callers pass the range
// filter's immutable slice), matching how the label set already flows.
func NewLabelVocabulary(labels []string) *LabelVocabulary {
	v := &LabelVocabulary{
		Labels:            labels,
		CanonicalByLabel:  make(map[string]string, len(labels)),
		LabelsByCanonical: make(map[string][]string, len(labels)),
	}
	for _, label := range labels {
		key := canonicalSpeciesKey(label)
		v.CanonicalByLabel[label] = key
		v.LabelsByCanonical[key] = append(v.LabelsByCanonical[key], label)
	}
	return v
}

// CanonicalKey returns the canonical species key for a label, hitting the memo
// when the label is in the vocabulary and computing canonicalSpeciesKey
// otherwise. A nil receiver computes, so it is safe on a "no vocabulary" value.
func (v *LabelVocabulary) CanonicalKey(label string) string {
	if v != nil {
		if key, ok := v.CanonicalByLabel[label]; ok {
			return key
		}
	}
	return canonicalSpeciesKey(label)
}

// HasCanonical reports whether any label in the vocabulary has the given
// canonical key. A nil receiver reports false.
func (v *LabelVocabulary) HasCanonical(key string) bool {
	if v == nil {
		return false
	}
	_, ok := v.LabelsByCanonical[key]
	return ok
}
