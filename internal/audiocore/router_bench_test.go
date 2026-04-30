package audiocore

import (
	"fmt"
	"sync"
	"testing"

	"github.com/tphakala/birdnet-go/internal/audiocore/buffer"
)

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
					SourceID:   "bench",
					Consumer:   mc,
					gainLinear: 2.0, // triggers applyProcessing
					inbox:      make(chan AudioFrame, 1),
					done:       make(chan struct{}),
					stopped:    make(chan struct{}),
				}
			}

			// Warm pools so steady-state reuse is established.
			for _, route := range routes {
				frame := AudioFrame{
					SourceID: "bench", SourceName: "bench",
					Data: frameData, SampleRate: 48000,
					BitDepth: 16, Channels: 1,
				}
				res, err := r.applyProcessing(frame, route, nil)
				if err != nil {
					b.Fatal(err)
				}
				res.release()
			}

			b.ReportAllocs()
			b.ResetTimer()

			var wg sync.WaitGroup
			perGoroutine := b.N / routeCount
			for _, route := range routes {
				wg.Add(1)
				go func(rt *Route) {
					defer wg.Done()
					for range perGoroutine {
						frame := AudioFrame{
							SourceID: "bench", SourceName: "bench",
							Data: frameData, SampleRate: 48000,
							BitDepth: 16, Channels: 1,
						}
						r.handleRouteFrame(frame, rt)
						// Drain the mock consumer's buffered channel.
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
