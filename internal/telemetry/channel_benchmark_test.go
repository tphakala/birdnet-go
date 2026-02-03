package telemetry

import (
	"fmt"
	"sync/atomic"
	"testing"
	"time"
)

// ErrorEvent represents an error event for the event bus
type ErrorEvent struct {
	Error     error
	Component string
	Timestamp time.Time
}

// BenchmarkChannelOperations tests performance of channel-based event bus
//
//nolint:gocognit // benchmark requires multiple test scenarios for comprehensive coverage
func BenchmarkChannelOperations(b *testing.B) {
	b.Run("UnbufferedChannel", func(b *testing.B) {
		ch := make(chan ErrorEvent)
		ctx := b.Context()

		// Consumer
		go func() {
			for {
				select {
				case <-ch:
					// Process event
				case <-ctx.Done():
					return
				}
			}
		}()

		// Pre-create error to reduce allocations
		testErr := fmt.Errorf("test error")
		event := ErrorEvent{
			Error:     testErr,
			Component: "test",
		}

		b.ReportAllocs()
		b.ResetTimer()

		for b.Loop() {
			// Only update timestamp to reduce allocations
			event.Timestamp = time.Now()
			ch <- event
		}
	})

	b.Run("BufferedChannel-100", func(b *testing.B) {
		benchmarkBufferedChannel(b, 100)
	})

	b.Run("BufferedChannel-1000", func(b *testing.B) {
		benchmarkBufferedChannel(b, 1000)
	})

	b.Run("BufferedChannel-10000", func(b *testing.B) {
		benchmarkBufferedChannel(b, 10000)
	})

	b.Run("NonBlockingSend", func(b *testing.B) {
		ch := make(chan ErrorEvent, 100)

		// Pre-create error to reduce allocations
		testErr := fmt.Errorf("test error")
		event := ErrorEvent{
			Error:     testErr,
			Component: "test",
		}

		b.ReportAllocs()
		b.ResetTimer()

		for b.Loop() {
			event.Timestamp = time.Now()
			select {
			case ch <- event:
				// Sent successfully
			default:
				// Channel full, drop event
			}
		}
	})

	b.Run("MultipleConsumers", func(b *testing.B) {
		ch := make(chan ErrorEvent, 1000)
		ctx := b.Context()

		// Start multiple consumers
		numConsumers := 4
		for range numConsumers {
			go func() {
				for {
					select {
					case <-ch:
						// Process event
					case <-ctx.Done():
						return
					}
				}
			}()
		}

		// Pre-create error to reduce allocations
		testErr := fmt.Errorf("test error")
		event := ErrorEvent{
			Error:     testErr,
			Component: "test",
		}

		b.ReportAllocs()
		b.ResetTimer()

		for b.Loop() {
			event.Timestamp = time.Now()
			ch <- event
		}
	})
}

func benchmarkBufferedChannel(b *testing.B, bufferSize int) {
	b.Helper()
	ch := make(chan ErrorEvent, bufferSize)
	ctx := b.Context()

	// Consumer
	go func() {
		for {
			select {
			case <-ch:
				// Process event
			case <-ctx.Done():
				return
			}
		}
	}()

	// Pre-create error to reduce allocations
	testErr := fmt.Errorf("test error")
	event := ErrorEvent{
		Error:     testErr,
		Component: "test",
	}

	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		event.Timestamp = time.Now()
		ch <- event
	}
}

// BenchmarkChannelBackpressure tests behavior under high load
func BenchmarkChannelBackpressure(b *testing.B) {
	b.Run("DropOldest", func(b *testing.B) {
		ch := make(chan ErrorEvent, 100)
		dropped := 0

		b.ReportAllocs()
		b.ResetTimer()

		for b.Loop() {
			event := ErrorEvent{
				Error:     fmt.Errorf("test error"),
				Component: "test",
				Timestamp: time.Now(),
			}

			select {
			case ch <- event:
				// Sent successfully
			default:
				// Channel full, drop oldest
				select {
				case <-ch:
					dropped++
					ch <- event
				default:
				}
			}
		}

		b.Logf("Dropped %d events", dropped)
	})

	b.Run("RateLimited", func(b *testing.B) {
		ch := make(chan ErrorEvent, 1000)
		// Use a more realistic rate limit: 1000 events/sec (1ms between events)
		limiter := time.NewTicker(time.Millisecond)
		defer limiter.Stop()

		// Pre-create error to reduce allocations
		testErr := fmt.Errorf("test error")
		event := ErrorEvent{
			Error:     testErr,
			Component: "test",
		}

		b.ReportAllocs()
		b.ResetTimer()

		for b.Loop() {
			<-limiter.C
			event.Timestamp = time.Now()
			ch <- event
		}
	})
}

// BenchmarkConcurrentProducers tests multiple producers sending to the same channel
func BenchmarkConcurrentProducers(b *testing.B) {
	ch := make(chan ErrorEvent, 10000)
	done := make(chan struct{})

	// Consumer that counts events
	var received atomic.Int64
	go func() {
		for event := range ch {
			_ = event // Process event
			received.Add(1)
		}
		close(done)
	}()

	b.ReportAllocs()
	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			ch <- ErrorEvent{
				Error:     fmt.Errorf("test error %d", i),
				Component: "test",
				Timestamp: time.Now(),
			}
			i++
		}
	})

	// Close channel to signal consumer to exit
	close(ch)

	// Wait for consumer to finish processing all events
	<-done

	b.Logf("Received %d events", received.Load())
}
