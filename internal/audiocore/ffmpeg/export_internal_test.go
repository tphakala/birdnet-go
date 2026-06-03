package ffmpeg

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestExportPhaseTimeout_MinimumForShortClip(t *testing.T) {
	t.Parallel()

	opts := &ExportOptions{
		PCMData:    make([]byte, 48000*2),
		SampleRate: 48000,
		Channels:   1,
		BitDepth:   16,
	}

	assert.Equal(t, minExportPhaseTimeout, exportPhaseTimeout(opts))
}

func TestExportPhaseTimeout_ScalesWithPCMDuration(t *testing.T) {
	t.Parallel()

	const durationSeconds = 120
	opts := &ExportOptions{
		PCMData:    make([]byte, 48000*2*durationSeconds),
		SampleRate: 48000,
		Channels:   1,
		BitDepth:   16,
	}

	assert.Equal(t, time.Duration(durationSeconds)*time.Second+exportPhaseTimeoutMargin, exportPhaseTimeout(opts))
}

func TestLoudnormGateFallbackOffset_NilStats(t *testing.T) {
	t.Parallel()

	offsetDB, ok := loudnormGateFallbackOffset(nil, ExportNormalization{TruePeak: -2})

	assert.False(t, ok)
	assert.Zero(t, offsetDB)
}

func TestBuildTwoPassLoudnormFilter(t *testing.T) {
	t.Parallel()

	norm := ExportNormalization{
		Enabled:       true,
		TargetLUFS:    -23.0,
		TruePeak:      -2.0,
		LoudnessRange: 7.0,
	}

	stats := &LoudnessStats{
		InputI:       "-15.0",
		InputTP:      "-1.0",
		InputLRA:     "5.0",
		InputThresh:  "-25.0",
		TargetOffset: "0.5",
	}

	filter := buildTwoPassLoudnormFilter(norm, stats)
	assert.Contains(t, filter, "linear=true")
	assert.Contains(t, filter, "measured_I=-15.0")
	assert.Contains(t, filter, "measured_TP=-1.0")
	assert.Contains(t, filter, "measured_LRA=5.0")
	assert.Contains(t, filter, "measured_thresh=-25.0")
	assert.Contains(t, filter, "offset=0.5")
}
