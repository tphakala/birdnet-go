package spectrogram

import (
	"context"
	"runtime"

	"golang.org/x/sync/semaphore"

	"github.com/tphakala/birdnet-go/internal/audiocore/transcode"
	"github.com/tphakala/birdnet-go/internal/mempolicy"
)

// The native renderer holds a whole decoded clip in memory, up to
// transcode.MaxDecodedPCMBytes, so the number of renders in flight is bounded by
// a byte budget on the Generator rather than by a count: ordinary clips of a few
// megabytes run together, while renders of the longest clips take turns.
const (
	// nativeRenderBudgetDivisor is the share of detected memory, as 1/n, that
	// in-flight native renders may account for.
	nativeRenderBudgetDivisor = 8

	// nativeDecodeGrowthFactor scales the decoded size to the peak heap one render
	// needs. Measured on a 1200 s, 48 kHz mono clip (115.2 MB decoded): WAV, MP3
	// and M4A peak at 1.0x, FLAC at 1.6x (the decode buffer doubles from a
	// file-size estimate, so the old and new buffers coexist) and Opus at 2.0x.
	nativeDecodeGrowthFactor = 2

	// nativeRenderFixedBytes covers what a render holds besides the decoded clip:
	// the canvas (one byte per pixel, about 2 MB at the widest request), the PNG
	// encoder, the analysis state and the resampler. A 15 s clip measured about
	// 3.3 MB over its 1.4 MB of audio.
	nativeRenderFixedBytes = 8 << 20
)

// nativeRenderWeight returns the budget bytes a render of a clip that decodes to
// at most decodedBytes holds. The bound is capped before it is scaled, so a
// corrupt header cannot overflow the product.
func nativeRenderWeight(decodedBytes int64) int64 {
	decodedBytes = min(max(decodedBytes, 0), transcode.MaxDecodedPCMBytes)
	return decodedBytes*nativeDecodeGrowthFactor + nativeRenderFixedBytes
}

// nativeRenderBudget returns the size of the render budget for a host with
// totalBytes of memory (zero or less when unknown) and numCPU processors. It is
// 1/nativeRenderBudgetDivisor of the memory, never below one worst-case render,
// so the longest accepted clip can always be rendered, and never above numCPU
// worst-case renders, since the caller's own slots allow no more at once.
func nativeRenderBudget(totalBytes int64, numCPU int) int64 {
	worst := nativeRenderWeight(transcode.MaxDecodedPCMBytes)
	ceiling := int64(max(numCPU, 1)) * worst
	if totalBytes <= 0 {
		return ceiling
	}
	return min(max(totalBytes/nativeRenderBudgetDivisor, worst), ceiling)
}

// setRenderBudget sizes the render budget of g.
func (g *Generator) setRenderBudget(size int64) {
	g.renderBudget = semaphore.NewWeighted(size)
	g.renderBudgetBytes = size
}

// initRenderBudget sizes the render budget from the detected host memory.
func (g *Generator) initRenderBudget() {
	total, _ := mempolicy.DetectTotalMemory()
	g.setRenderBudget(nativeRenderBudget(total, runtime.NumCPU()))
}

// acquireRenderBudget waits for weight bytes of the render budget, clamped to
// [1, budget size] so a request larger than the whole budget still runs, alone.
// The returned function releases exactly what was acquired. A render takes its
// weight once, up front, and only after the caller's own slot, so two renders
// can never each hold part of the budget while waiting for the rest.
func (g *Generator) acquireRenderBudget(ctx context.Context, weight int64) (release func(), err error) {
	if g.renderBudget == nil {
		return func() {}, nil
	}
	weight = min(max(weight, 1), g.renderBudgetBytes)
	if err := g.renderBudget.Acquire(ctx, weight); err != nil {
		return nil, err
	}
	return func() { g.renderBudget.Release(weight) }, nil
}
