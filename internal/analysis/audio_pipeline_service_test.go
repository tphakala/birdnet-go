package analysis

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/app"
	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// Compile-time interface compliance check.
var _ app.Service = (*AudioPipelineService)(nil)

func TestAudioPipelineService_Name(t *testing.T) {
	t.Parallel()

	svc := NewAudioPipelineService(&conf.Settings{}, nil, nil, nil, nil)
	assert.Equal(t, "audio-pipeline", svc.Name())
}

func TestAudioPipelineService_Stop_NilSafe(t *testing.T) {
	t.Parallel()

	svc := NewAudioPipelineService(&conf.Settings{}, nil, nil, nil, nil)
	// Stop before Start should not panic and should return nil.
	assert.NotPanics(t, func() {
		err := svc.Stop(t.Context())
		assert.NoError(t, err)
	})
}

func TestResolveModelTargets_EmptyInput(t *testing.T) {
	t.Parallel()

	loaded := map[string]classifier.ModelInfo{
		"BirdNET_GLOBAL_6K_V2.4": {ID: "BirdNET_GLOBAL_6K_V2.4", Spec: classifier.ModelSpec{SampleRate: 48000}},
	}
	targets := resolveModelTargets(nil, loaded)
	assert.Empty(t, targets, "nil config IDs should return nil")

	targets = resolveModelTargets([]string{}, loaded)
	assert.Empty(t, targets, "empty config IDs should return empty")
}

func TestResolveModelTargets_SingleModel(t *testing.T) {
	t.Parallel()

	loaded := map[string]classifier.ModelInfo{
		"BirdNET_GLOBAL_6K_V2.4": {
			ID:   "BirdNET_GLOBAL_6K_V2.4",
			Name: "BirdNET",
			Spec: classifier.ModelSpec{SampleRate: 48000, ClipLength: 3 * time.Second},
		},
	}

	targets := resolveModelTargets([]string{"birdnet"}, loaded)
	require.Len(t, targets, 1)
	assert.Equal(t, "BirdNET_GLOBAL_6K_V2.4", targets[0].ID)
	assert.Equal(t, 48000, targets[0].Spec.SampleRate)
}

func TestResolveModelTargets_MultiModel(t *testing.T) {
	t.Parallel()

	loaded := map[string]classifier.ModelInfo{
		"BirdNET_GLOBAL_6K_V2.4": {
			ID:   "BirdNET_GLOBAL_6K_V2.4",
			Spec: classifier.ModelSpec{SampleRate: 48000, ClipLength: 3 * time.Second},
		},
		"Perch_V2": {
			ID:   "Perch_V2",
			Spec: classifier.ModelSpec{SampleRate: 32000, ClipLength: 5 * time.Second},
		},
	}

	targets := resolveModelTargets([]string{"birdnet", "perch_v2"}, loaded)
	require.Len(t, targets, 2)

	// Collect results into a map for order-independent assertion.
	targetMap := make(map[string]int, len(targets))
	for _, tgt := range targets {
		targetMap[tgt.ID] = tgt.Spec.SampleRate
	}
	assert.Equal(t, 48000, targetMap["BirdNET_GLOBAL_6K_V2.4"])
	assert.Equal(t, 32000, targetMap["Perch_V2"])
}

func TestResolveModelTargets_UnknownConfigID(t *testing.T) {
	t.Parallel()

	loaded := map[string]classifier.ModelInfo{
		"BirdNET_GLOBAL_6K_V2.4": {
			ID:   "BirdNET_GLOBAL_6K_V2.4",
			Spec: classifier.ModelSpec{SampleRate: 48000},
		},
	}

	// "unknown_model" is not in configToRegistryID, so it should be skipped.
	targets := resolveModelTargets([]string{"unknown_model"}, loaded)
	assert.Empty(t, targets)
}

func TestResolveModelTargets_KnownButNotLoaded(t *testing.T) {
	t.Parallel()

	// perch_v2 is a known config ID but is not in the loaded models map.
	loaded := map[string]classifier.ModelInfo{
		"BirdNET_GLOBAL_6K_V2.4": {
			ID:   "BirdNET_GLOBAL_6K_V2.4",
			Spec: classifier.ModelSpec{SampleRate: 48000},
		},
	}

	targets := resolveModelTargets([]string{"birdnet", "perch_v2"}, loaded)
	require.Len(t, targets, 1, "only birdnet should resolve, perch_v2 is not loaded")
	assert.Equal(t, "BirdNET_GLOBAL_6K_V2.4", targets[0].ID)
}
