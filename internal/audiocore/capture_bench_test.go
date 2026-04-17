// Package audiocore, capture_bench_test.go.
// Allocation benchmarks that document the churn reduction from routing the
// malgo capture callback's convert output through a pooled byte slice instead
// of allocating a fresh destination buffer on every frame.
package audiocore

import (
	"testing"

	"github.com/gen2brain/malgo"

	"github.com/tphakala/birdnet-go/internal/audiocore/buffer"
)

// benchS32FrameBytes is the size of one representative malgo frame for the
// capture path: 1920 s32 sample values (960 stereo frames) at 4 bytes each,
// the worst case for per-frame churn (convertS32ToS16 drops the width to
// 16-bit and halves the byte count).
const benchS32FrameBytes = 1920 * 4

// BenchmarkConvertS32ToS16_Pooled measures the pool-friendly path used by
// startCapture when a bufMgr is wired. Expected to report a bounded, near-zero
// allocs/op count (the sync.Pool interface-box allocation is the only churn
// left in a warm pool).
func BenchmarkConvertS32ToS16_Pooled(b *testing.B) {
	log := GetLogger()
	bufMgr := buffer.NewManager(log)
	samples := make([]byte, benchS32FrameBytes)
	outSize := s16OutputSize(samples, malgo.FormatS32)
	pool := bufMgr.BytePoolFor(outSize)

	b.ReportAllocs()
	for b.Loop() {
		out := pool.Get()[:outSize]
		convertS32ToS16Into(out, samples)
		pool.Put(out)
	}
}

// BenchmarkConvertS32ToS16_Allocating measures the legacy allocating helper
// kept around as a fallback when bufMgr is nil. Expected to report 1 alloc/op
// of (benchS32FrameBytes/4)*2 bytes.
func BenchmarkConvertS32ToS16_Allocating(b *testing.B) {
	samples := make([]byte, benchS32FrameBytes)

	b.ReportAllocs()
	for b.Loop() {
		_ = convertS32ToS16(samples)
	}
}
