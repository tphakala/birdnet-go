//go:build !race

package soundlevel

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

const (
	allocRuns          = 100
	allocChunkSamples  = testSampleRate // one full period per run, so every run crosses a period boundary
	allocIntervalCount = 600            // long enough that no interval completes during the runs (101 periods)
)

// TestProcess_DoesNotAllocateWithinInterval pins that Process allocates nothing
// between interval boundaries, including the per-period work at each one-second
// boundary. It is excluded under -race, whose instrumentation allocates.
func TestProcess_DoesNotAllocateWithinInterval(t *testing.T) {
	p := newTestProcessor(t, testSampleRate, allocIntervalCount)
	chunk := generateNoise(noiseSigma, allocChunkSamples, 1)
	emit := func(SoundLevelData) {}

	allocs := testing.AllocsPerRun(allocRuns, func() { p.Process(chunk, emit) })
	assert.Zero(t, allocs)
}
