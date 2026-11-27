package processor

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/datastore"
)

// TestMergePendingHit_WarmsGuideOnlyWhenPendingEntryIsCreated pins where the
// species guide warm sits inside mergePendingHit: it belongs to the branch that
// creates a pending entry, because the point is to warm the guide for a species
// that was just detected. Every later hit of the same window merges into that
// entry, so warming there would re-request the same guide for the whole
// detection window.
//
// The call has no other coverage, and it lives in the middle of a block that has
// already been refactored around it once, so this test exists to make losing it
// a failure rather than a silent regression.
func TestMergePendingHit_WarmsGuideOnlyWhenPendingEntryIsCreated(t *testing.T) {
	base := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)

	p := mergeTestProcessor(t)

	// firePreFetchGuide invokes the callback synchronously, so recording into a
	// plain slice is deterministic here.
	var warmed []string
	p.SetGuidePreFetch(func(_ context.Context, scientificName string) {
		warmed = append(warmed, scientificName)
	})

	item := classifier.Results{
		Source:    datastore.AudioSource{ID: mergeTestSource},
		ModelID:   classifier.RegistryIDBirdNETV24,
		StartTime: base,
	}
	det := detectionFor(mergeTestScientific, mergeTestCommon)
	det.Result.Confidence = 0.8

	p.mergePendingHit(&item, det, base.Add(mergeTestClip), mergeTestWindow)
	require.Equal(t, []string{mergeTestScientific}, warmed,
		"creating the pending entry must warm the guide for the newly detected species")

	// A second hit for the same source and species merges into the existing
	// entry, so the species is no longer new.
	p.mergePendingHit(&item, det, base.Add(2*mergeTestClip), mergeTestWindow)
	assert.Equal(t, []string{mergeTestScientific}, warmed,
		"merging into an existing pending entry must not warm the guide again")
}

// TestMergePendingHit_WithoutGuidePreFetchDoesNothing pins that a processor with
// no pre-fetch callback (the species guide feature or its pre-fetch is off, which
// is the default) merges a hit normally instead of panicking on the nil callback.
func TestMergePendingHit_WithoutGuidePreFetchDoesNothing(t *testing.T) {
	base := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)

	p := mergeTestProcessor(t)
	require.Nil(t, p.guidePreFetch, "the pre-fetch callback is unset by default")

	item := classifier.Results{
		Source:    datastore.AudioSource{ID: mergeTestSource},
		ModelID:   classifier.RegistryIDBirdNETV24,
		StartTime: base,
	}
	det := detectionFor(mergeTestScientific, mergeTestCommon)
	det.Result.Confidence = 0.8

	require.NotPanics(t, func() {
		p.mergePendingHit(&item, det, base.Add(mergeTestClip), mergeTestWindow)
	})
	assert.Equal(t, 1, onlyPending(t, p).Count, "the hit is still merged")
}
