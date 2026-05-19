package analysis

import (
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/tphakala/birdnet-go/internal/audiocore"
	"github.com/tphakala/birdnet-go/internal/audiocore/buffer"
	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/logger"
)

func TestSourceNeedsReconfigure(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		running  *audiocore.AudioSource
		desired  *audiocore.SourceConfig
		expected bool
	}{
		{
			name: "same config, no reconfigure needed",
			running: &audiocore.AudioSource{
				SampleRate: 48000,
				BitDepth:   16,
				Channels:   1,
			},
			desired: &audiocore.SourceConfig{
				SampleRate: 48000,
				BitDepth:   16,
				Channels:   1,
			},
			expected: false,
		},
		{
			name: "sample rate changed",
			running: &audiocore.AudioSource{
				SampleRate: 48000,
				BitDepth:   16,
				Channels:   1,
			},
			desired: &audiocore.SourceConfig{
				SampleRate: 96000,
				BitDepth:   16,
				Channels:   1,
			},
			expected: true,
		},
		{
			name: "bit depth changed",
			running: &audiocore.AudioSource{
				SampleRate: 48000,
				BitDepth:   16,
				Channels:   1,
			},
			desired: &audiocore.SourceConfig{
				SampleRate: 48000,
				BitDepth:   32,
				Channels:   1,
			},
			expected: true,
		},
		{
			name: "channels changed",
			running: &audiocore.AudioSource{
				SampleRate: 48000,
				BitDepth:   16,
				Channels:   1,
			},
			desired: &audiocore.SourceConfig{
				SampleRate: 48000,
				BitDepth:   16,
				Channels:   2,
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := sourceNeedsReconfigure(tt.running, tt.desired)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// newModelTestBufferManager creates a buffer.Manager with analysis buffers allocated
// for the given (sourceID, modelID) pairs. Each buffer is allocated with
// minimal dimensions suitable for testing.
func newModelTestBufferManager(t *testing.T, pairs [][2]string) *buffer.Manager {
	t.Helper()
	mgr := buffer.NewManager(logger.NewSlogLogger(io.Discard, logger.LogLevelError, time.UTC))
	for _, p := range pairs {
		err := mgr.AllocateAnalysis(p[0], p[1], 1024, 0, 512)
		assert.NoError(t, err)
	}
	return mgr
}

func TestSourceModelsChanged(t *testing.T) {
	t.Parallel()

	const (
		src            = "rtsp_abc123"
		birdnetID      = "BirdNET_V2.4"
		perchID        = "Perch_V2"
		primaryModelID = birdnetID
	)

	loaded := map[string]classifier.ModelInfo{
		birdnetID: {ID: birdnetID},
		perchID:   {ID: perchID},
	}

	tests := []struct {
		name             string
		currentModels    [][2]string // (sourceID, modelID) pairs for buffer allocation
		desiredConfigIDs []string
		expected         bool
	}{
		{
			name:             "no change, single model",
			currentModels:    [][2]string{{src, birdnetID}},
			desiredConfigIDs: []string{"birdnet"},
			expected:         false,
		},
		{
			name:             "no change, both models",
			currentModels:    [][2]string{{src, birdnetID}, {src, perchID}},
			desiredConfigIDs: []string{"birdnet", "perch_v2"},
			expected:         false,
		},
		{
			name:             "perch added",
			currentModels:    [][2]string{{src, birdnetID}},
			desiredConfigIDs: []string{"birdnet", "perch_v2"},
			expected:         true,
		},
		{
			name:             "perch removed",
			currentModels:    [][2]string{{src, birdnetID}, {src, perchID}},
			desiredConfigIDs: []string{"birdnet"},
			expected:         true,
		},
		{
			name:             "model swapped",
			currentModels:    [][2]string{{src, birdnetID}},
			desiredConfigIDs: []string{"perch_v2"},
			expected:         true,
		},
		{
			name:             "empty desired falls back to primary, no change",
			currentModels:    [][2]string{{src, birdnetID}},
			desiredConfigIDs: []string{},
			expected:         false,
		},
		{
			name:             "empty desired falls back to primary, perch stale",
			currentModels:    [][2]string{{src, birdnetID}, {src, perchID}},
			desiredConfigIDs: []string{},
			expected:         true,
		},
		{
			name:             "unknown config ID ignored, no effective change",
			currentModels:    [][2]string{{src, birdnetID}},
			desiredConfigIDs: []string{"birdnet", "unknown_model"},
			expected:         false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			mgr := newModelTestBufferManager(t, tt.currentModels)
			result := sourceModelsChanged(mgr, src, tt.desiredConfigIDs, loaded, primaryModelID)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestSourceModelsChanged_UnloadedModelIgnored(t *testing.T) {
	t.Parallel()

	const src = "rtsp_abc123"

	// Only BirdNET is loaded; Perch is not.
	loadedOnlyBirdnet := map[string]classifier.ModelInfo{
		"BirdNET_V2.4": {ID: "BirdNET_V2.4"},
	}

	mgr := newModelTestBufferManager(t, [][2]string{{src, "BirdNET_V2.4"}})

	// Config requests perch_v2 but it's not loaded: should NOT report a
	// change so we avoid a spurious rebuild on every hot-reload tick.
	changed := sourceModelsChanged(mgr, src, []string{"birdnet", "perch_v2"}, loadedOnlyBirdnet, "BirdNET_V2.4")
	assert.False(t, changed, "unloaded model in desired config should be ignored")
}
