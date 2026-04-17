// Package ffmpeg, stream_bench_test.go.
// Allocation benchmarks documenting the churn reduction from reading ffmpeg
// stdout into a pooled byte slice instead of `make([]byte, ffmpegBufferSize)`
// on every read loop iteration.
package ffmpeg

import (
	"testing"

	"github.com/tphakala/birdnet-go/internal/audiocore"
	"github.com/tphakala/birdnet-go/internal/audiocore/buffer"
)

// benchReadDispatchSize approximates the typical sub-slice size handed to the
// dispatcher on each stdout read: much smaller than ffmpegBufferSize because
// ffmpeg rarely fills the full 32 KB on one Read.
const benchReadDispatchSize = 1024

// benchDispatchedSlice is a package-level sink that captures the most recent
// dispatched sub-slice. Writing through an exported pointer forces escape
// analysis to place the slice's backing array on the heap, matching the
// real-world behaviour where the dispatched slice is handed to the router
// goroutine and outlives the reader frame. Without this, the compiler can
// stack-allocate `make([]byte, ffmpegBufferSize)` and report 0 allocs/op,
// making the allocating path look deceptively free.
var benchDispatchedSlice []byte

// benchDispatchSink mimics the handoff from the stdout reader to the router:
// the slice escapes via a package-level write. Marked noinline so the
// assignment is not folded back into the caller's frame.
//
//go:noinline
func benchDispatchSink(buf []byte) {
	benchDispatchedSlice = buf
}

// BenchmarkStream_ReadIntoPool models the pool-friendly stdout read pattern:
// Get a 32 KB slice from the buffer manager, simulate consuming part of it via
// a sub-slice dispatch, and Put it back. Expected to report a small bounded
// allocs/op count (the sync.Pool interface-box allocation dominates).
func BenchmarkStream_ReadIntoPool(b *testing.B) {
	log := audiocore.GetLogger()
	bufMgr := buffer.NewManager(log)
	pool := bufMgr.BytePoolFor(ffmpegBufferSize)

	b.ReportAllocs()
	for b.Loop() {
		buf := pool.Get()[:ffmpegBufferSize]
		// Simulate the "read into buf, dispatch sub-slice" pattern. The
		// dispatch path hands the sub-slice off to the router goroutine;
		// route it through a noinline package-level sink so the backing
		// array is forced onto the heap.
		benchDispatchSink(buf[:benchReadDispatchSize])
		pool.Put(buf)
	}
}

// BenchmarkStream_ReadAllocating models the legacy allocating path used when
// no bufMgr is wired. Expected to report 1 alloc/op of 32 KB once the slice
// is forced to escape through the package-level dispatch sink.
func BenchmarkStream_ReadAllocating(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		buf := make([]byte, ffmpegBufferSize)
		benchDispatchSink(buf[:benchReadDispatchSize])
	}
}
