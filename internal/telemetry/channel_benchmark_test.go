package telemetry

import (
	"context"
	"fmt"
	"sync"
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
func BenchmarkChannelOperations(b *testing.B) {
	b.Run("UnbufferedChannel", func(b *testing.B) {
		ch := make(chan ErrorEvent)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		
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
		
		b.ReportAllocs()
		b.ResetTimer()
		
		for b.Loop() {
			ch <- ErrorEvent{
				Error:     fmt.Errorf("test error"),
				Component: "test",
				Timestamp: time.Now(),
			}
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
		
		b.ReportAllocs()
		b.ResetTimer()
		
		for b.Loop() {
			select {
			case ch <- ErrorEvent{
				Error:     fmt.Errorf("test error"),
				Component: "test",
				Timestamp: time.Now(),
			}:
				// Sent successfully
			default:
				// Channel full, drop event
			}
		}
	})
	
	b.Run("MultipleConsumers", func(b *testing.B) {
		ch := make(chan ErrorEvent, 1000)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		
		// Start multiple consumers
		numConsumers := 4
		for i := 0; i < numConsumers; i++ {
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
		
		b.ReportAllocs()
		b.ResetTimer()
		
		for b.Loop() {
			ch <- ErrorEvent{
				Error:     fmt.Errorf("test error"),
				Component: "test",
				Timestamp: time.Now(),
			}
		}
	})
}

func benchmarkBufferedChannel(b *testing.B, bufferSize int) {
	ch := make(chan ErrorEvent, bufferSize)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	
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
	
	b.ReportAllocs()
	b.ResetTimer()
	
	for b.Loop() {
		ch <- ErrorEvent{
			Error:     fmt.Errorf("test error"),
			Component: "test",
			Timestamp: time.Now(),
		}
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
				Error:     fmt.Errorf("test error %d", b.N),
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
		limiter := time.NewTicker(time.Microsecond) // 1M events/sec
		defer limiter.Stop()
		
		b.ReportAllocs()
		b.ResetTimer()
		
		for b.Loop() {
			<-limiter.C
			ch <- ErrorEvent{
				Error:     fmt.Errorf("test error"),
				Component: "test",
				Timestamp: time.Now(),
			}
		}
	})
}

// BenchmarkConcurrentProducers tests multiple producers sending to the same channel
func BenchmarkConcurrentProducers(b *testing.B) {
	ch := make(chan ErrorEvent, 10000)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	
	// Consumer that counts events
	var received int64
	var mu sync.Mutex
	go func() {
		for {
			select {
			case <-ch:
				mu.Lock()
				received++
				mu.Unlock()
			case <-ctx.Done():
				return
			}
		}
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
	
	// Give consumer time to process remaining events
	time.Sleep(10 * time.Millisecond)
	
	mu.Lock()
	b.Logf("Received %d events", received)
	mu.Unlock()
}