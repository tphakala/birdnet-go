package telemetry

import (
	"fmt"
	"testing"

	"github.com/getsentry/sentry-go"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/privacy"
)

// BenchmarkTelemetryDisabled measures performance when telemetry is disabled
func BenchmarkTelemetryDisabled(b *testing.B) {
	// Set up disabled telemetry
	settings := &conf.Settings{
		Sentry: conf.SentrySettings{
			Enabled: false,
		},
	}

	// Initialize with disabled telemetry
	if err := InitSentry(settings); err != nil {
		b.Fatalf("Failed to initialize Sentry: %v", err)
	}
	InitializeErrorIntegration()

	b.Run("CaptureError", func(b *testing.B) {
		err := fmt.Errorf("benchmark error")
		b.ReportAllocs()
		b.ResetTimer()

		for b.Loop() {
			CaptureError(err, "benchmark")
		}
	})

	b.Run("CaptureMessage", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()

		for b.Loop() {
			CaptureMessage("benchmark message", sentry.LevelInfo, "benchmark")
		}
	})

	b.Run("EnhancedError", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()

		for b.Loop() {
			err := errors.Newf("benchmark").
				Component("benchmark").
				Category(errors.CategoryNetwork).
				Build()

			// This should be nearly free when telemetry is disabled
			CaptureError(err, err.GetComponent())
		}
	})
}

// BenchmarkTelemetryEnabled measures performance when telemetry is enabled
func BenchmarkTelemetryEnabled(b *testing.B) {
	// Initialize with mock transport
	config, cleanup := InitForTesting(b)
	defer cleanup()

	b.Run("CaptureError", func(b *testing.B) {
		err := fmt.Errorf("benchmark error")
		b.ReportAllocs()
		b.ResetTimer()

		for b.Loop() {
			CaptureError(err, "benchmark")
		}

		b.Logf("Captured %d events", config.MockTransport.GetEventCount())
	})

	b.Run("CaptureMessage", func(b *testing.B) {
		config.MockTransport.Clear()
		b.ReportAllocs()
		b.ResetTimer()

		for b.Loop() {
			CaptureMessage("benchmark message", sentry.LevelInfo, "benchmark")
		}

		b.Logf("Captured %d events", config.MockTransport.GetEventCount())
	})

	b.Run("EnhancedError", func(b *testing.B) {
		config.MockTransport.Clear()
		b.ReportAllocs()
		b.ResetTimer()

		for b.Loop() {
			err := errors.Newf("benchmark").
				Component("benchmark").
				Category(errors.CategoryNetwork).
				Context("key", "value").
				Build()

			CaptureError(err, err.GetComponent())
		}

		b.Logf("Captured %d events", config.MockTransport.GetEventCount())
	})
}

// BenchmarkScrubMessage measures the performance of message scrubbing
func BenchmarkScrubMessage(b *testing.B) {
	testCases := []struct {
		name    string
		message string
	}{
		{
			name:    "NoURL",
			message: "Simple error message without any URLs",
		},
		{
			name:    "SingleURL",
			message: "Failed to connect to https://api.example.com/endpoint",
		},
		{
			name:    "MultipleURLs",
			message: "Error connecting to https://api1.example.com and https://api2.example.com",
		},
		{
			name:    "URLWithCredentials",
			message: "RTSP error: rtsp://admin:password@192.168.1.100:554/stream",
		},
	}

	for _, tc := range testCases {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()

			for b.Loop() {
				_ = privacy.ScrubMessage(tc.message)
			}
		})
	}
}

// BenchmarkMockTransport measures the performance of the mock transport
func BenchmarkMockTransport(b *testing.B) {
	transport := NewMockTransport()

	b.Run("SendEvent", func(b *testing.B) {
		event := &sentry.Event{
			Message: "benchmark event",
			Level:   sentry.LevelInfo,
			Tags: map[string]string{
				"component": "benchmark",
			},
		}

		b.ReportAllocs()
		b.ResetTimer()

		for b.Loop() {
			transport.SendEvent(event)
		}

		b.Logf("Stored %d events", transport.GetEventCount())
	})

	b.Run("GetEvents", func(b *testing.B) {
		// Pre-populate with events
		for i := range 100 {
			transport.SendEvent(&sentry.Event{
				Message: fmt.Sprintf("event %d", i),
			})
		}

		b.ReportAllocs()
		b.ResetTimer()

		for b.Loop() {
			_ = transport.GetEvents()
		}
	})
}

// BenchmarkConcurrentCapture measures performance under concurrent load
func BenchmarkConcurrentCapture(b *testing.B) {
	config, cleanup := InitForTesting(b)
	defer cleanup()

	b.Run("Parallel-8", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()

		b.RunParallel(func(pb *testing.PB) {
			i := 0
			for pb.Next() {
				err := fmt.Errorf("concurrent error %d", i)
				CaptureError(err, "concurrent")
				i++
			}
		})

		b.Logf("Captured %d events", config.MockTransport.GetEventCount())
	})
}
