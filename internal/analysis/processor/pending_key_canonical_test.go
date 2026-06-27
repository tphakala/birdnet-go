package processor

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tphakala/birdnet-go/internal/detection"
)

func detectionFor(scientific, common string) *Detections {
	return &Detections{
		Result: detection.Result{
			Species: detection.Species{
				ScientificName: scientific,
				CommonName:     common,
			},
		},
	}
}

// TestPendingKeyForDetection_KeysOnScientificName verifies the pending-merge key is
// derived from the (canonical) scientific name, not the common name.
func TestPendingKeyForDetection_KeysOnScientificName(t *testing.T) {
	t.Parallel()

	det := detectionFor(testSciCanonical, testCommonDove)
	got := pendingKeyForDetection("src", det)
	assert.Equal(t, pendingDetectionKey("src", "spilopelia senegalensis"), got)
}

// TestPendingKeyForDetection_CanonicalizationDrivesMerge composes the real ingestion
// steps that run before a pending key is built: canonicalizeSpecies collapses a
// legacy alias (testSciLegacy) to the canonical scientific name, then
// pendingKeyForDetection keys on that name. A legacy-labelled and a canonical-labelled
// detection of one taxon therefore land on the same pending key and merge, even though
// their model-emitted scientific names and common names differ. This exercises the
// production canonicalization (not a tautology of two pre-canonicalized strings).
func TestPendingKeyForDetection_CanonicalizationDrivesMerge(t *testing.T) {
	t.Parallel()

	// The resolver stands in for the taxonomy/OpenFauna chain; only the alias branch
	// of canonicalizeSpecies consults it.
	resolver := fakeTaxonomyResolver{fn: func(string) (string, string, string) {
		return testSciCanonical, testCommonDove, testCodeDove
	}}

	legacySci, _, _, _ := canonicalizeSpecies(resolver, testSciLegacy, testCommonDove, testCodeLegacy)
	canonicalSci, _, _, _ := canonicalizeSpecies(resolver, testSciCanonical, testCommonDove, testCodeDove)

	legacy := detectionFor(legacySci, testCommonDove)
	canonical := detectionFor(canonicalSci, "Palm Dove") // intentionally different common name
	assert.Equal(t, pendingKeyForDetection("src", legacy), pendingKeyForDetection("src", canonical),
		"a taxon detected under a legacy and a canonical name must share a pending key (merge)")
}

// TestPendingKeyForDetection_DistinctSpeciesSharingCommonNameDoNotMerge verifies
// that two genuinely different species which share a localized common name keep
// separate pending keys, removing the latent bug where common-name keying merged
// distinct taxa.
func TestPendingKeyForDetection_DistinctSpeciesSharingCommonNameDoNotMerge(t *testing.T) {
	t.Parallel()

	a := detectionFor("Larus argentatus", "Herring Gull")
	b := detectionFor("Larus smithsonianus", "Herring Gull")
	assert.NotEqual(t, pendingKeyForDetection("src", a), pendingKeyForDetection("src", b),
		"distinct species sharing a common name must not merge")
}
