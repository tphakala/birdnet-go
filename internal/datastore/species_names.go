package datastore

import "strings"

// SpeciesName is one resolved model label: its scientific name and the localized
// common name to display and search for it.
type SpeciesName struct {
	Scientific string
	Common     string
}

// batchLocalizer is the optional cold-path capability used to localize the
// scientific-only labels (bats, Perch-unique species) that ResolveLocal misses.
// SpeciesNameResolver implementations may also satisfy this to make those labels
// searchable; the production *openfauna.Resolver does. Kept separate from
// SpeciesNameResolver so adding it does not force a change on every implementer or a
// mock regeneration.
type batchLocalizer interface {
	ResolveLocalizedBatch(scientificNames []string) map[string]string
}

// ResolveLabelNames splits each "Scientific_Common" (or scientific-only) label and
// resolves a localized common name for it. Scientific-only secondary-model labels
// that the working-set resolver (ResolveLocal) does not cover are batched into a
// single cold-path dataset pass via batchLocalizer, so they become searchable without
// a per-label scan. The returned slice preserves label order; labels with no
// resolvable common name are omitted. This is the single source the api/v2 and
// v2only name maps build on, so their reverse search and forward display stay in
// lockstep.
func ResolveLabelNames(labels []string, resolver SpeciesNameResolver) []SpeciesName {
	useResolver := !IsNilResolver(resolver)

	type parsed struct {
		sci    string
		common string // "" means: needs the batch pass
	}
	items := make([]parsed, 0, len(labels))
	var needBatch []string
	for _, label := range labels {
		// A scientific-only label (no separator, for example Perch v2 / bat labels)
		// has no embedded common name; treat the whole label as the scientific name.
		sci, common, found := strings.Cut(label, "_")
		if !found {
			sci, common = label, ""
		}
		sci = strings.TrimSpace(sci)
		common = strings.TrimSpace(common)
		if sci == "" {
			continue
		}
		if useResolver {
			if r, ok := resolver.ResolveLocal(sci); ok {
				common = r
			}
		}
		if common == "" && useResolver {
			needBatch = append(needBatch, sci)
		}
		items = append(items, parsed{sci: sci, common: common})
	}

	var batch map[string]string
	if len(needBatch) > 0 {
		if bl, ok := resolver.(batchLocalizer); ok {
			batch = bl.ResolveLocalizedBatch(needBatch)
		}
	}

	out := make([]SpeciesName, 0, len(items))
	for _, it := range items {
		common := it.common
		if common == "" {
			common = batch[it.sci] // nil-map read is "", safe
		}
		if common == "" {
			continue
		}
		out = append(out, SpeciesName{Scientific: it.sci, Common: common})
	}
	return out
}
