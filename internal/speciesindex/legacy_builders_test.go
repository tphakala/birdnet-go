package speciesindex

import (
	"strings"

	"golang.org/x/text/unicode/norm"

	"github.com/tphakala/birdnet-go/internal/datastore"
)

// This file freezes the two legacy name-map builders as they existed before the
// speciesindex fold, so the golden tests assert Build reproduces them byte for
// byte. These are verbatim copies of internal/datastore/v2only.buildNameMaps and
// internal/api/v2.buildNameMaps, with their folds inlined to the same expression
// (strings.ToLower(norm.NFC.String(...))) they both used (the api/v2 side reached
// it via apicore.NormalizeForLookup). Do not "simplify" them to call Fold: the
// point of the reference is that it is independent of the code under test.

// legacyMaps is the three-map product both builders returned.
type legacyMaps struct {
	sciToCommon       map[string]string
	sciToCommonFolded map[string]string
	commonToSci       map[string]string
}

// legacyDatastoreBuild is the verbatim internal/datastore/v2only.buildNameMaps.
//
//nolint:dupl // deliberately a verbatim frozen copy; it must stay independent of legacyAPIBuild and of Build so the golden test proves both original sites produced identical maps.
func legacyDatastoreBuild(labels []string, resolver datastore.SpeciesNameResolver) legacyMaps {
	speciesMap := make(map[string]string, len(labels))
	commonMap := make(map[string]string, len(labels))
	commonFoldedMap := make(map[string]string, len(labels))
	ambiguous := make(map[string]struct{})
	for _, sn := range datastore.ResolveLabelNames(labels, resolver) {
		commonMap[sn.Scientific] = sn.Common
		folded := strings.ToLower(norm.NFC.String(sn.Common))
		commonFoldedMap[sn.Scientific] = folded

		if _, seen := ambiguous[folded]; seen {
			continue
		}
		if existing, exists := speciesMap[folded]; exists && existing != sn.Scientific {
			ambiguous[folded] = struct{}{}
			delete(speciesMap, folded)
			continue
		}
		speciesMap[folded] = sn.Scientific
	}
	return legacyMaps{sciToCommon: commonMap, sciToCommonFolded: commonFoldedMap, commonToSci: speciesMap}
}

// legacyAPIBuild is the verbatim internal/api/v2.buildNameMaps, with the fold
// (apicore.NormalizeForLookup) inlined to the expression it evaluated to.
//
//nolint:dupl // deliberately a verbatim frozen copy; it must stay independent of legacyDatastoreBuild and of Build so the golden test proves both original sites produced identical maps.
func legacyAPIBuild(labels []string, resolver datastore.SpeciesNameResolver) legacyMaps {
	sciToCommon := make(map[string]string, len(labels))
	sciToCommonFolded := make(map[string]string, len(labels))
	commonToSci := make(map[string]string, len(labels))
	ambiguous := make(map[string]struct{})
	for _, sn := range datastore.ResolveLabelNames(labels, resolver) {
		sciToCommon[sn.Scientific] = sn.Common

		key := strings.ToLower(norm.NFC.String(sn.Common))
		sciToCommonFolded[sn.Scientific] = key
		if _, seen := ambiguous[key]; seen {
			continue
		}
		if existing, exists := commonToSci[key]; exists && existing != sn.Scientific {
			ambiguous[key] = struct{}{}
			delete(commonToSci, key)
			continue
		}
		commonToSci[key] = sn.Scientific
	}
	return legacyMaps{sciToCommon: sciToCommon, sciToCommonFolded: sciToCommonFolded, commonToSci: commonToSci}
}
