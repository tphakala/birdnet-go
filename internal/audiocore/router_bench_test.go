package audiocore

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/tphakala/birdnet-go/internal/audiocore/buffer"
	"github.com/tphakala/birdnet-go/internal/audiocore/resample"
)

// BenchmarkHandleRouteFrame_Resample measures handleRouteFrame throughput
// through the resampling path (48kHz source → 32kHz consumer) with pooled
// buffers, exercising the zero-copy ResampleTo integration.
func BenchmarkHandleRouteFrame_Resample(b *testing.B) {
	for _, tc := range []struct {
		name     string
		fromRate int
		toRate   int
	}{
		{"48k_to_32k", 48000, 32000},
		{"256k_to_48k", 256000, 48000},
	} {
		b.Run(tc.name, func(b *testing.B) {
			mgr := buffer.NewManager(GetLogger())
			r := NewAudioRouter(GetLogger(), mgr)
			b.Cleanup(r.Close)

			// Build input frame: 100ms of mono 16-bit PCM at source rate.
			inputSamples := tc.fromRate / 10
			frameData := make([]byte, inputSamples*2)
			for i := range inputSamples {
				v := int16((i % 65536) - 32768)
				frameData[i*2] = byte(v)
				frameData[i*2+1] = byte(v >> 8)
			}

			rs, err := resample.NewResampler(tc.fromRate, tc.toRate)
			if err != nil {
				b.Fatal(err)
			}
			b.Cleanup(func() { _ = rs.Close() })

			mc := newMockConsumer("bench-resample")
			mc.sampleRate = tc.toRate

			route := &Route{
				SourceID:         "bench",
				Consumer:         mc,
				resampler:        rs,
				sourceSampleRate: tc.fromRate,
				inbox:            make(chan AudioFrame, 1),
				done:             make(chan struct{}),
				stopped:          make(chan struct{}),
			}

			// Warm up to establish pool steady state.
			for range 4 {
				frame := AudioFrame{
					SourceID: "bench", SourceName: "bench",
					Data: frameData, SampleRate: tc.fromRate,
					BitDepth: 16, Channels: 1,
				}
				r.handleRouteFrame(frame, route)
				select {
				case <-mc.frames:
				default:
				}
			}

			b.ReportAllocs()
			b.ResetTimer()

			for b.Loop() {
				frame := AudioFrame{
					SourceID: "bench", SourceName: "bench",
					Data: frameData, SampleRate: tc.fromRate,
					BitDepth: 16, Channels: 1,
				}
				r.handleRouteFrame(frame, route)
				select {
				case <-mc.frames:
				default:
				}
			}
		})
	}
}

// BenchmarkHandleRouteFrame_Contention measures handleRouteFrame throughput
// with multiple goroutines sharing a single buffer.Manager, simulating the
// contention pattern that causes sustained frame drops in production.
func BenchmarkHandleRouteFrame_Contention(b *testing.B) {
	for _, routeCount := range []int{1, 4, 8, 16} {
		b.Run(fmt.Sprintf("%d_routes", routeCount), func(b *testing.B) {
			mgr := buffer.NewManager(GetLogger())
			r := NewAudioRouter(GetLogger(), mgr)
			defer r.Close()

			// 2880 bytes = 1440 samples = 30ms of 48kHz mono 16-bit PCM.
			frameData := make([]byte, 2880)

			routes := make([]*Route, routeCount)
			for i := range routeCount {
				mc := newMockConsumer(fmt.Sprintf("bench-%d", i))
				routes[i] = &Route{
					SourceID: "bench",
					Consumer: mc,
					inbox:    make(chan AudioFrame, 1),
					done:     make(chan struct{}),
					stopped:  make(chan struct{}),
				}
				routes[i].gain.Store(testGain(b, 6.020599913279624)) // triggers applyProcessing
			}

			// Warm pools so steady-state reuse is established.
			for _, route := range routes {
				frame := AudioFrame{
					SourceID: "bench", SourceName: "bench",
					Data: frameData, SampleRate: 48000,
					BitDepth: 16, Channels: 1,
				}
				res, err := r.applyProcessing(frame, route, nil, route.gain.Load())
				if err != nil {
					b.Fatal(err)
				}
				res.release()
			}

			b.ReportAllocs()
			b.ResetTimer()

			var wg sync.WaitGroup
			var counter atomic.Int64
			target := int64(b.N)
			for _, route := range routes {
				wg.Add(1)
				go func(rt *Route) {
					defer wg.Done()
					for counter.Add(1) <= target {
						frame := AudioFrame{
							SourceID: "bench", SourceName: "bench",
							Data: frameData, SampleRate: 48000,
							BitDepth: 16, Channels: 1,
						}
						r.handleRouteFrame(frame, rt)
						select {
						case <-rt.Consumer.(*mockConsumer).frames:
						default:
						}
					}
				}(route)
			}
			wg.Wait()
		})
	}
}

// BenchmarkHandleRouteFrame_ResampleEQ measures handleRouteFrame through the
// resample-then-process path (256 kHz source to 48 kHz consumer, three-band EQ
// plus gain), which hands the resampler's float32 output to the processing
// stage.
func BenchmarkHandleRouteFrame_ResampleEQ(b *testing.B) {
	const (
		srcRate = 256000
		dstRate = 48000
	)
	mgr := buffer.NewManager(GetLogger())
	r := NewAudioRouter(GetLogger(), mgr)
	b.Cleanup(r.Close)

	rs, err := resample.NewResampler(srcRate, dstRate)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = rs.Close() })

	mc := newMockConsumer("bench-resample-eq")
	mc.sampleRate = dstRate
	route := &Route{
		SourceID:         "bench",
		Consumer:         mc,
		resampler:        rs,
		sourceSampleRate: srcRate,
		inbox:            make(chan AudioFrame, 1),
		done:             make(chan struct{}),
		stopped:          make(chan struct{}),
	}
	route.gain.Store(testGain(b, 6.020599913279624))
	route.filterChain.Store(testChain(b, dstRate, benchEQBands...))

	frame := AudioFrame{
		SourceID: "bench", SourceName: "bench",
		Data: int16sToBytes(noiseAndTone(srcRate/10, srcRate)), SampleRate: srcRate,
		BitDepth: 16, Channels: 1,
	}

	for range 4 {
		r.handleRouteFrame(frame, route)
		<-mc.frames
	}

	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		r.handleRouteFrame(frame, route)
		<-mc.frames
	}
}
