package logger

import (
	"io"
	"testing"
	"time"
)

// BenchmarkFieldCreation benchmarks field constructor performance
func BenchmarkFieldCreation(b *testing.B) {
	b.Run("String", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_ = String("cve_id", "CVE-2024-1234")
		}
	})

	b.Run("Int", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_ = Int("count", 42)
		}
	})

	b.Run("Int64", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_ = Int64("size", 1234567890)
		}
	})

	b.Run("Bool", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_ = Bool("active", true)
		}
	})

	b.Run("Duration", func(b *testing.B) {
		b.ReportAllocs()
		d := 5 * time.Second
		for b.Loop() {
			_ = Duration("elapsed", d)
		}
	})

	b.Run("Error", func(b *testing.B) {
		b.ReportAllocs()
		err := io.EOF
		for b.Loop() {
			_ = Error(err)
		}
	})

	b.Run("Any", func(b *testing.B) {
		b.ReportAllocs()
		data := map[string]int{"a": 1, "b": 2}
		for b.Loop() {
			_ = Any("data", data)
		}
	})
}

// BenchmarkRepeatedKeyInterning benchmarks key interning for repeated keys
func BenchmarkRepeatedKeyInterning(b *testing.B) {
	b.Run("SameKey1000Times", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			for range 1000 {
				_ = String("cve_id", "CVE-2024-1234")
			}
		}
	})

	b.Run("DifferentKeys", func(b *testing.B) {
		b.ReportAllocs()
		keys := []string{"cve_id", "client_id", "task_id", "request_id", "error", "duration"}
		for i := 0; i < b.N; i++ { //nolint:gocritic // setup loop for benchmark data, not benchmark iteration
			for _, key := range keys {
				_ = String(key, "value")
			}
		}
	})
}

// BenchmarkLogInfo benchmarks logging an info message
func BenchmarkLogInfo(b *testing.B) {
	b.Run("NoFields", func(b *testing.B) {
		logger := NewSlogLogger(io.Discard, LogLevelInfo, nil)
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ { //nolint:gocritic // setup loop for benchmark data, not benchmark iteration
			logger.Info("test message")
		}
	})

	b.Run("OneField", func(b *testing.B) {
		logger := NewSlogLogger(io.Discard, LogLevelInfo, nil)
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ { //nolint:gocritic // setup loop for benchmark data, not benchmark iteration
			logger.Info("test message", String("cve_id", "CVE-2024-1234"))
		}
	})

	b.Run("ThreeFields", func(b *testing.B) {
		logger := NewSlogLogger(io.Discard, LogLevelInfo, nil)
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ { //nolint:gocritic // setup loop for benchmark data, not benchmark iteration
			logger.Info("test message",
				String("cve_id", "CVE-2024-1234"),
				Int("count", 42),
				Bool("critical", true))
		}
	})

	b.Run("SixFields", func(b *testing.B) {
		logger := NewSlogLogger(io.Discard, LogLevelInfo, nil)
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ { //nolint:gocritic // setup loop for benchmark data, not benchmark iteration
			logger.Info("test message",
				String("cve_id", "CVE-2024-1234"),
				String("client_id", "client-123"),
				String("task_id", "task-456"),
				Int("count", 42),
				Bool("critical", true),
				Duration("elapsed", 5*time.Second))
		}
	})
}

// BenchmarkLogWithModule benchmarks logging with module scoping
func BenchmarkLogWithModule(b *testing.B) {
	b.Run("ModuleLogger", func(b *testing.B) {
		logger := NewSlogLogger(io.Discard, LogLevelInfo, nil).Module("analysis")
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ { //nolint:gocritic // setup loop for benchmark data, not benchmark iteration
			logger.Info("analyzing audio",
				String("species", "Turdus merula"),
				Int("confidence", 75))
		}
	})

	b.Run("NestedModule", func(b *testing.B) {
		logger := NewSlogLogger(io.Discard, LogLevelInfo, nil).Module("analysis").Module("processor")
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ { //nolint:gocritic // setup loop for benchmark data, not benchmark iteration
			logger.Info("processing complete",
				String("species", "Turdus merula"),
				Duration("elapsed", 2*time.Second))
		}
	})
}

// BenchmarkLoggerWith benchmarks the With method for accumulated fields
func BenchmarkLoggerWith(b *testing.B) {
	b.Run("WithOneField", func(b *testing.B) {
		logger := NewSlogLogger(io.Discard, LogLevelInfo, nil)
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ { //nolint:gocritic // setup loop for benchmark data, not benchmark iteration
			l := logger.With(String("request_id", "req-123"))
			l.Info("processing")
		}
	})

	b.Run("WithThreeFields", func(b *testing.B) {
		logger := NewSlogLogger(io.Discard, LogLevelInfo, nil)
		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			l := logger.With(
				String("request_id", "req-123"),
				String("client_id", "client-456"),
				String("user_id", "user-789"))
			l.Info("processing")
		}
	})
}

// BenchmarkTextHandler benchmarks the text handler output
func BenchmarkTextHandler(b *testing.B) {
	tz := time.UTC

	b.Run("SimpleMessage", func(b *testing.B) {
		handler := newTextHandler(io.Discard, 0, tz)
		logger := &SlogLogger{
			handler:  handler,
			level:    0,
			module:   "test",
			timezone: tz,
			fields:   make([]Field, 0),
		}
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ { //nolint:gocritic // setup loop for benchmark data, not benchmark iteration
			logger.Info("test message")
		}
	})

	b.Run("WithFields", func(b *testing.B) {
		handler := newTextHandler(io.Discard, 0, tz)
		logger := &SlogLogger{
			handler:  handler,
			level:    0,
			module:   "test",
			timezone: tz,
			fields:   make([]Field, 0),
		}
		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			logger.Info("test message",
				String("cve_id", "CVE-2024-1234"),
				Int("count", 42),
				Bool("critical", true))
		}
	})
}

// BenchmarkAttrPool benchmarks the attribute slice pooling
func BenchmarkAttrPool(b *testing.B) {
	b.Run("GetPut", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			attrs := getAttrs()
			putAttrs(attrs)
		}
	})

	b.Run("GetUseAndPut", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ { //nolint:gocritic // setup loop for benchmark data, not benchmark iteration
			attrs := getAttrs()
			*attrs = append(*attrs,
				fieldToAttr(String("key", "value")),
				fieldToAttr(Int("count", 42)))
			putAttrs(attrs)
		}
	})
}

// BenchmarkLevelFiltering benchmarks level filtering (no-op for filtered levels)
func BenchmarkLevelFiltering(b *testing.B) {
	b.Run("FilteredOut", func(b *testing.B) {
		logger := NewSlogLogger(io.Discard, LogLevelWarn, nil) // Warn level
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ { //nolint:gocritic // setup loop for benchmark data, not benchmark iteration
			logger.Debug("filtered message", String("key", "value")) // Debug < Warn, filtered
		}
	})

	b.Run("NotFiltered", func(b *testing.B) {
		logger := NewSlogLogger(io.Discard, LogLevelDebug, nil) // Debug level
		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			logger.Debug("not filtered", String("key", "value"))
		}
	})
}
