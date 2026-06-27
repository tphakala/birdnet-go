package processor

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tphakala/birdnet-go/internal/detection"
	"github.com/tphakala/birdnet-go/internal/openfauna"
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

	det := detectionFor("Spilopelia senegalensis", "Laughing Dove")
	got := pendingKeyForDetection("src", det)
	assert.Equal(t, pendingDetectionKey("src", "spilopelia senegalensis"), got)
}

// TestPendingKeyForDetection_AliasAndCanonicalShareKey verifies the end-to-end
// collapse mechanism: ingestion canonicalizes the model-emitted name (via
// openfauna.CanonicalName) before the pending key is built, so a legacy v2.4 label
// ("Streptopelia senegalensis") and the modern eBird name ("Spilopelia senegalensis")
// for one taxon resolve to the same scientific name and share a pending key. This
// asserts the real alias resolution, not a tautology of two identical strings.
func TestPendingKeyForDetection_AliasAndCanonicalShareKey(t *testing.T) {
	t.Parallel()

	legacy := detectionFor(openfauna.CanonicalName("Streptopelia senegalensis"), "Laughing Dove")
	canonical := detectionFor(openfauna.CanonicalName("Spilopelia senegalensis"), "Palm Dove")
	assert.Equal(t, pendingKeyForDetection("src", legacy), pendingKeyForDetection("src", canonical),
		"alias and canonical names of one taxon must share a pending key (merge)")
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
