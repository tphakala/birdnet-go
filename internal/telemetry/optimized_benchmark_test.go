package telemetry

import (
	"fmt"
	"testing"

	"github.com/getsentry/sentry-go"
)

// BenchmarkOptimizedTelemetryDisabled measures optimized performance when disabled
func BenchmarkOptimizedTelemetryDisabled(b *testing.B) {
	// Ensure telemetry is disabled
	telemetryEnabled.Store(false)

	b.Run("FastCaptureError", func(b *testing.B) {
		err := fmt.Errorf("benchmark error")
		b.ReportAllocs()
		b.ResetTimer()
		
		for b.Loop() {
			FastCaptureError(err, "benchmark")
		}
	})

	b.Run("FastCaptureMessage", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		
		for b.Loop() {
			FastCaptureMessage("benchmark message", sentry.LevelInfo, "benchmark")
		}
	})

	b.Run("AtomicCheck", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		
		for b.Loop() {
			_ = IsTelemetryEnabled()
		}
	})
}

// BenchmarkInlinedCheck measures the cost of inlined telemetry checks
func BenchmarkInlinedCheck(b *testing.B) {
	// Ensure telemetry is disabled
	telemetryEnabled.Store(false)
	
	b.Run("DirectAtomicLoad", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		
		for b.Loop() {
			if telemetryEnabled.Load() {
				// This branch should never execute
				b.Fatal("telemetry should be disabled")
			}
		}
	})

	b.Run("ErrorWithCheck", func(b *testing.B) {
		err := fmt.Errorf("benchmark error")
		b.ReportAllocs()
		b.ResetTimer()
		
		for b.Loop() {
			// This is what production code should do
			if IsTelemetryEnabled() {
				CaptureError(err, "benchmark")
			}
		}
	})
}

// BenchmarkMemoryPressure tests behavior under memory pressure
//
//nolint:gocognit // benchmark requires multiple test scenarios for comprehensive coverage
func BenchmarkMemoryPressure(b *testing.B) {
	telemetryEnabled.Store(false)
	
	b.Run("LargeErrorMessage", func(b *testing.B) {
		// Create a large error message
		largeMsg := make([]byte, 1024)
		for i := range largeMsg {
			largeMsg[i] = 'x'
		}
		err := fmt.Errorf("%s", largeMsg)
		
		b.ReportAllocs()
		b.ResetTimer()
		
		for b.Loop() {
			if IsTelemetryEnabled() {
				CaptureError(err, "benchmark")
			}
		}
	})

	b.Run("ManySmallErrors", func(b *testing.B) {
		errors := make([]error, 100)
		for i := range errors {
			errors[i] = fmt.Errorf("error %d", i)
		}
		
		b.ReportAllocs()
		b.ResetTimer()
		
		for b.Loop() {
			for _, err := range errors {
				if IsTelemetryEnabled() {
					CaptureError(err, "benchmark")
				}
			}
		}
	})
}