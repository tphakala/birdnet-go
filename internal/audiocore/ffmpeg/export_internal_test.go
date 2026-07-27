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

// The export filter chain is gain-only: loudness normalisation is planned in Go
// and arrives as a resolved GainDB, so nothing here may emit a loudnorm filter.
func TestBuildExportAudioFilter(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		gainDB float64
		want   string
	}{
		{name: "no gain yields no filter", gainDB: 0, want: ""},
		{name: "positive gain is signed", gainDB: 6, want: "volume=+6.000000dB"},
		{name: "negative gain carries its own sign", gainDB: -3.5, want: "volume=-3.500000dB"},
		{
			// A measured EBU R128 gain is never a round number. Printing it at the
			// old single decimal would put the FFmpeg formats up to 0.05 dB off the
			// native encoders, which apply the exact float64.
			name:   "measured gain keeps sub-decibel precision",
			gainDB: 12.3456789,
			want:   "volume=+12.345679dB",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			filter := buildExportAudioFilter(&ExportOptions{GainDB: tt.gainDB})

			assert.Equal(t, tt.want, filter)
			assert.NotContains(t, filter, "loudnorm", "the export path must never build a loudnorm filter")
		})
	}
}
