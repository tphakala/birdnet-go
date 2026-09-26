package classifier

import (
	"fmt"

	"github.com/tphakala/birdnet-go/internal/errors"
)

// maxMissingSpeciesLogged caps how many missing-taxonomy species are listed
// individually in the debug diagnostics before switching to a "... and N more"
// summary, so a large gap does not flood the log.
const maxMissingSpeciesLogged = 10

// taxonomyService owns the eBird taxonomy the orchestrator answers species-code
// lookups from. It is built once in NewOrchestrator, before any model loads, from
// the same embedded data BirdNET used to load, and is read-only afterwards, so it
// needs no lock. Moving taxonomy ownership off the primary model is the first step
// of the model de-privilege epic: a code lookup no longer depends on which acoustic
// model is loaded.
type taxonomyService struct {
	taxonomyMap TaxonomyMap
	sciIndex    ScientificNameIndex
}

// newTaxonomyService loads the taxonomy through LoadTaxonomyData, exactly as
// NewBirdNET did, wrapping a failure in the same error category so the constructor
// fails identically when the embedded (or a custom) taxonomy cannot be parsed.
// customPath is "" for the embedded taxonomy.
func newTaxonomyService(customPath string) (*taxonomyService, error) {
	taxonomyMap, sciIndex, err := LoadTaxonomyData(customPath)
	if err != nil {
		return nil, errors.New(err).
			Component("birdnet").
			Category(errors.CategoryModelInit).
			Context("operation", "load_taxonomy").
			Context("taxonomy_path", customPath).
			Build()
	}
	return &taxonomyService{taxonomyMap: taxonomyMap, sciIndex: sciIndex}, nil
}

// speciesCode returns the eBird species code for a species name, matching
// GetSpeciesCodeFromName exactly (a generated placeholder code and false when the
// species is absent from the taxonomy).
func (t *taxonomyService) speciesCode(speciesName string) (string, bool) {
	return GetSpeciesCodeFromName(t.taxonomyMap, t.sciIndex, speciesName)
}

// nameFromCode returns the "Scientific_Common" name for an eBird code, matching
// GetSpeciesNameFromCode exactly.
func (t *taxonomyService) nameFromCode(code string) (string, bool) {
	return GetSpeciesNameFromCode(t.taxonomyMap, code)
}

// logMissingCodes logs, at debug level, the labels absent from the taxonomy. The
// caller supplies the two facts BirdNET.logMissingTaxonomyCodes read off the model
// (whether a custom model or label file is configured, and whether debug logging is
// enabled) so the emitted lines match the pre-move behavior, where every line went
// through bn.Debug and so appeared only with debug enabled. The debug gate is
// checked first so the taxonomy scan is skipped entirely when debug is off (the
// default).
func (t *taxonomyService) logMissingCodes(labels []string, customModelOrLabels, debug bool) {
	if !debug {
		return
	}
	complete, missing := IsTaxonomyComplete(t.taxonomyMap, labels)
	if complete {
		return
	}
	debugf := func(format string, v ...any) {
		GetLogger().Debug(fmt.Sprintf(format, v...))
	}
	if customModelOrLabels {
		debugf("Custom model/labels detected: %d species are missing from the taxonomy data", len(missing))
		debugf("Placeholder taxonomy codes will be generated for these species")
	} else {
		debugf("Warning: %d species are missing from the taxonomy data", len(missing))
	}
	for i, species := range missing {
		if i < maxMissingSpeciesLogged { // Only show the first N to avoid flooding logs
			code := GeneratePlaceholderCode(species)
			scientific, common := SplitSpeciesName(species)
			debugf("Missing taxonomy for '%s' (Sci: '%s', Common: '%s') - using placeholder code: %s",
				species, scientific, common, code)
		} else if i == maxMissingSpeciesLogged {
			debugf("... and %d more", len(missing)-maxMissingSpeciesLogged)
			break
		}
	}
}
