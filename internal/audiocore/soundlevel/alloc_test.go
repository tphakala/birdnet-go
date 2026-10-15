//go:build !race

package soundlevel

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

const (
	allocRuns          = 100
	allocChunkSamples  = 4800
	allocIntervalCount = 600 // long enough that no interval completes during the runs
)

// TestProcess_DoesNotAllocateWithinInterval pins that Process allocates nothing
// between interval boundaries. It is excluded under -race, whose
// instrumentation allocates.
func TestProcess_DoesNotAllocateWithinInterval(t *testing.T) {
	p := newTestProcessor(t, testSampleRate, allocIntervalCount)
	chunk := generateNoise(noiseSigma, allocChunkSamples, 1)
	emit := func(SoundLevelData) {}

	allocs := testing.AllocsPerRun(allocRuns, func() { p.Process(chunk, emit) })
	assert.Zero(t, allocs)
}
