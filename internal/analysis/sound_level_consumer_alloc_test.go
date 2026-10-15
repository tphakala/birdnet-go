//go:build !race

package analysis

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/audiocore"
	"github.com/tphakala/birdnet-go/internal/audiocore/soundlevel"
)

const (
	allocTestInterval     = 600 // long enough that no interval completes during the runs
	allocTestFrameSamples = 4800
	allocTestRuns         = 100
)

// TestSoundLevelConsumer_WriteDoesNotAllocateWithinInterval pins that Write
// allocates nothing between interval boundaries. It is excluded under -race,
// whose instrumentation allocates.
func TestSoundLevelConsumer_WriteDoesNotAllocateWithinInterval(t *testing.T) {
	proc, err := soundlevel.NewProcessor("src-8", "Source Eight", consumerTestRate, allocTestInterval)
	require.NoError(t, err)

	consumer, _, err := NewSoundLevelConsumer("slc-9", proc, consumerTestRate, 16, 1)
	require.NoError(t, err)

	frame := audiocore.AudioFrame{
		SourceID: "src-8",
		Data:     makeSineWavePCM16(allocTestFrameSamples, 440, consumerTestRate),
	}
	allocs := testing.AllocsPerRun(allocTestRuns, func() {
		_ = consumer.Write(frame)
	})
	assert.Zero(t, allocs)
}
