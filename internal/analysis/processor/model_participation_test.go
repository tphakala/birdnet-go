package processor

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/audiocore/buffer"
	"github.com/tphakala/birdnet-go/internal/classifier"
)

func TestActiveModels(t *testing.T) {
	t.Parallel()

	buffers := map[string]*buffer.AnalysisBuffer{
		"BirdNET_V2.4": nil,
		"Perch_V2":     nil,
		"Bat":          nil,
	}
	got := activeModels(buffers, func(modelID string) bool { return modelID != "Bat" })

	assert.ElementsMatch(t, []string{"BirdNET_V2.4", "Perch_V2"}, got)
}

func TestResultModelContributions_AddsZeroHitEntriesForActiveModels(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	hits := map[string]ModelContribution{
		"BirdNET_V2.4": {
			HitCount: 4, MaxConfidence: 0.9, MinConfidence: 0.6, ConfidenceSum: 3.0,
			FirstHitAt: base, LastHitAt: base.Add(3 * time.Second),
		},
		"Perch_V2": {HitCount: 1, MaxConfidence: 0.7},
	}

	got := resultModelContributions(hits, []string{"BirdNET_V2.4", "Perch_V2", "BirdNET_V3.0"})

	require.Len(t, got, 3)

	v24 := got["BirdNET_V2.4"]
	assert.Equal(t, 4, v24.HitCount, "an active model that also hit keeps its hit count")
	assert.InDelta(t, 0.9, v24.MaxConfidence, 1e-9)
	assert.InDelta(t, 0.6, v24.MinConfidence, 1e-9)
	assert.InDelta(t, 3.0, v24.ConfidenceSum, 1e-9)
	assert.True(t, v24.HasHitStats())
	assert.Equal(t, classifier.DetectionModelInfoForID("BirdNET_V2.4"), v24.Model)

	perch := got["Perch_V2"]
	assert.Equal(t, 1, perch.HitCount)
	assert.InDelta(t, 0.7, perch.MaxConfidence, 1e-9)

	v3 := got["BirdNET_V3.0"]
	assert.Zero(t, v3.HitCount)
	assert.Zero(t, v3.MaxConfidence)
	assert.False(t, v3.HasHitStats())
	assert.Equal(t, classifier.DetectionModelInfoForID("BirdNET_V3.0"), v3.Model)
}
