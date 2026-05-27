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
