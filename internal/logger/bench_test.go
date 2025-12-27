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
		for i := 0; i < b.N; i++ {
			_ = String("cve_id", "CVE-2024-1234")
		}
	})

	b.Run("Int", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = Int("count", 42)
		}
	})

	b.Run("Int64", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = Int64("size", 1234567890)
		}
	})

	b.Run("Bool", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = Bool("active", true)
		}
	})

	b.Run("Duration", func(b *testing.B) {
		b.ReportAllocs()
		d := 5 * time.Second
		for i := 0; i < b.N; i++ {
			_ = Duration("elapsed", d)
		}
	})

	b.Run("Error", func(b *testing.B) {
		b.ReportAllocs()
		err := io.EOF
		for i := 0; i < b.N; i++ {
			_ = Error(err)
		}
	})

	b.Run("Any", func(b *testing.B) {
		b.ReportAllocs()
		data := map[string]int{"a": 1, "b": 2}
		for i := 0; i < b.N; i++ {
			_ = Any("data", data)
		}
	})
}

// BenchmarkRepeatedKeyInterning benchmarks key interning for repeated keys
func BenchmarkRepeatedKeyInterning(b *testing.B) {
	b.Run("SameKey1000Times", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			for range 1000 {
				_ = String("cve_id", "CVE-2024-1234")
			}
		}
	})

	b.Run("DifferentKeys", func(b *testing.B) {
		b.ReportAllocs()
		keys := []string{"cve_id", "client_id", "task_id", "request_id", "error", "duration"}
		for i := 0; i < b.N; i++ {
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
		for i := 0; i < b.N; i++ {
			logger.Info("test message")
		}
	})

	b.Run("OneField", func(b *testing.B) {
		logger := NewSlogLogger(io.Discard, LogLevelInfo, nil)
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			logger.Info("test message", String("cve_id", "CVE-2024-1234"))
		}
	})

	b.Run("ThreeFields", func(b *testing.B) {
		logger := NewSlogLogger(io.Discard, LogLevelInfo, nil)
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
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
		for i := 0; i < b.N; i++ {
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
		logger := NewSlogLogger(io.Discard, LogLevelInfo, nil).Module("analyzer")
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			logger.Info("analyzing CVE",
				String("cve_id", "CVE-2024-1234"),
				Int("score", 75))
		}
	})

	b.Run("NestedModule", func(b *testing.B) {
		logger := NewSlogLogger(io.Discard, LogLevelInfo, nil).Module("analyzer").Module("ai")
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			logger.Info("AI analysis complete",
				String("cve_id", "CVE-2024-1234"),
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
		for i := 0; i < b.N; i++ {
			l := logger.With(String("request_id", "req-123"))
			l.Info("processing")
		}
	})

	b.Run("WithThreeFields", func(b *testing.B) {
		logger := NewSlogLogger(io.Discard, LogLevelInfo, nil)
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
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
		for i := 0; i < b.N; i++ {
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
		for i := 0; i < b.N; i++ {
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
		for i := 0; i < b.N; i++ {
			attrs := getAttrs()
			putAttrs(attrs)
		}
	})

	b.Run("GetUseAndPut", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
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
		for i := 0; i < b.N; i++ {
			logger.Debug("filtered message", String("key", "value")) // Debug < Warn, filtered
		}
	})

	b.Run("NotFiltered", func(b *testing.B) {
		logger := NewSlogLogger(io.Discard, LogLevelDebug, nil) // Debug level
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			logger.Debug("not filtered", String("key", "value"))
		}
	})
}
