package speciesindex

import (
	"slices"
	"strings"

	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/detection"
	"github.com/tphakala/birdnet-go/internal/openfauna"
)

// Snapshot is an immutable set of species-name lookup maps built once from a
// label list and a resolver. All maps are non-nil; callers index them without
// guards. A Snapshot is never mutated after Build returns, so it is safe to
// share across goroutines behind an atomic.Pointer.
type Snapshot struct {
	// SciToCommon maps scientific name -> localized common name (display).
	SciToCommon map[string]string
	// SciToCommonFolded maps scientific name -> Fold(common) for allocation-free
	// substring matching on the search hot path.
	SciToCommonFolded map[string]string
	// CommonToSci maps Fold(common) -> scientific name (reverse lookup). A folded
	// common name shared by two or more distinct scientific names is ambiguous and
	// is absent from this map, so search falls through to substring matching
	// (which returns all matches) rather than routing to an arbitrary species.
	CommonToSci map[string]string
	// CanonicalByLabel maps every label -> its canonical key
	// (Fold-of-CanonicalName over the extracted scientific name), memoized so the
	// request path never recomputes it per label.
	CanonicalByLabel map[string]string
	// LabelsByCanonical maps canonical key -> every label sharing it, so
	// taxonomic aliases (a legacy name and its canonical name) group together.
	LabelsByCanonical map[string][]string
	// LabelBySci maps scientific name -> the full label it came from, so
	// ResolveLabel can return the original label for a scientific-only entry that
	// has no common part to rebuild it from.
	LabelBySci map[string]string
	// Labels is the working set of labels, deduplicated in first-occurrence order.
	Labels []string
	// Locale is the locale the maps were built for ("" when unknown).
	Locale string
}

// emptySnapshot is the shared zero snapshot with non-nil maps returned by Empty.
// It is never mutated (Snapshots are immutable), so sharing one instance is safe.
var emptySnapshot = &Snapshot{
	SciToCommon:       map[string]string{},
	SciToCommonFolded: map[string]string{},
	CommonToSci:       map[string]string{},
	CanonicalByLabel:  map[string]string{},
	LabelsByCanonical: map[string][]string{},
	LabelBySci:        map[string]string{},
	Labels:            []string{},
}

// Empty returns the shared zero snapshot with non-nil maps.
func Empty() *Snapshot {
	return emptySnapshot
}

// Build constructs an immutable snapshot for the given labels, resolver and
// locale. The three name maps are produced by exactly the algorithm the
// datastore and api/v2 builders used: ResolveLabelNames, then fold, then the
// ambiguity drop. The memo maps are computed once here so no request path ever
// calls openfauna.CanonicalName per label.
func Build(labels []string, resolver datastore.SpeciesNameResolver, locale string) *Snapshot {
	s := &Snapshot{
		SciToCommon:       make(map[string]string, len(labels)),
		SciToCommonFolded: make(map[string]string, len(labels)),
		CommonToSci:       make(map[string]string, len(labels)),
		CanonicalByLabel:  make(map[string]string, len(labels)),
		LabelsByCanonical: make(map[string][]string, len(labels)),
		LabelBySci:        make(map[string]string, len(labels)),
		Labels:            make([]string, 0, len(labels)),
		Locale:            locale,
	}

	// Name maps: byte-identical to both legacy builders. Ambiguous reverse keys
	// are deleted, not last-writer-wins, so an ambiguous common name falls through
	// to substring search rather than routing to an arbitrary species.
	ambiguous := make(map[string]struct{})
	for _, sn := range datastore.ResolveLabelNames(labels, resolver) {
		s.SciToCommon[sn.Scientific] = sn.Common
		folded := Fold(sn.Common)
		s.SciToCommonFolded[sn.Scientific] = folded

		if _, seen := ambiguous[folded]; seen {
			continue
		}
		if existing, exists := s.CommonToSci[folded]; exists && existing != sn.Scientific {
			ambiguous[folded] = struct{}{}
			delete(s.CommonToSci, folded)
			continue
		}
		s.CommonToSci[folded] = sn.Scientific
	}

	// Memo maps: additive, over every label (regardless of the resolver), so
	// scientific-only labels the name maps drop are still resolvable and the
	// canonical key is precomputed.
	seen := make(map[string]struct{}, len(labels))
	for _, label := range labels {
		if _, dup := seen[label]; dup {
			continue
		}
		seen[label] = struct{}{}

		sci := detection.ExtractScientificName(label)
		if sci == "" {
			continue
		}
		s.Labels = append(s.Labels, label)
		if _, ok := s.LabelBySci[sci]; !ok {
			s.LabelBySci[sci] = label
		}
		ck := computeCanonicalKey(label)
		s.CanonicalByLabel[label] = ck
		s.LabelsByCanonical[ck] = append(s.LabelsByCanonical[ck], label)
	}
	return s
}

// computeCanonicalKey is the canonical lookup key for a label: the extracted
// scientific name run through the openfauna alias map, lower-cased. It is
// identical to classifier.canonicalSpeciesKey, pinned by a classifier-side test.
func computeCanonicalKey(label string) string {
	return strings.ToLower(openfauna.CanonicalName(detection.ExtractScientificName(label)))
}

// CanonicalKey returns the canonical key for a label, hitting the memo when the
// label was part of the built set and computing it otherwise.
func (s *Snapshot) CanonicalKey(label string) string {
	if ck, ok := s.CanonicalByLabel[label]; ok {
		return ck
	}
	return computeCanonicalKey(label)
}

// ResolveLabel returns the full label and localized common name for a scientific
// name. An exact scientific-name hit wins; failing that, a taxonomic-alias hit
// resolves only when exactly one label shares the canonical key (an ambiguous
// alias returns ok=false so the caller does not pick arbitrarily). The common
// name may be empty for a scientific-only label with no resolver coverage.
func (s *Snapshot) ResolveLabel(sci string) (label, common string, ok bool) {
	sci = strings.TrimSpace(sci)
	if sci == "" {
		return "", "", false
	}
	if lbl, found := s.LabelBySci[sci]; found {
		return lbl, s.SciToCommon[sci], true
	}
	ck := strings.ToLower(openfauna.CanonicalName(detection.ExtractScientificName(sci)))
	if lbls := s.LabelsByCanonical[ck]; len(lbls) == 1 {
		lbl := lbls[0]
		return lbl, s.SciToCommon[detection.ExtractScientificName(lbl)], true
	}
	return "", "", false
}

// Search returns the scientific names whose folded common name contains the
// given already-folded query, sorted. An empty query returns nil.
func (s *Snapshot) Search(folded string) []string {
	if folded == "" {
		return nil
	}
	var out []string
	for sci, fc := range s.SciToCommonFolded {
		if strings.Contains(fc, folded) {
			out = append(out, sci)
		}
	}
	slices.Sort(out)
	return out
}
