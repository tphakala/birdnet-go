// Package logtest provides shared test-only helpers for capturing what the
// process-global logger emits during a test. It lives in its own importable
// package (rather than as a _test.go helper) because a _test.go file cannot be
// imported by other packages; several packages previously each carried a
// near-identical copy of this swap-global-logger dance. Production code must
// never import this package: it pulls in "testing" and swaps global state.
//
// It mirrors the conftest/apitest pattern already used elsewhere in the tree.
package logtest

import (
	"bytes"
	"log/slog"
	"testing"

	"github.com/tphakala/birdnet-go/internal/logger"
)

// levelName maps a slog.Level to the CentralLogger DefaultLevel string, so the
// logger's own level gate is set consistently with the capture handler's level
// (otherwise the logger could pre-drop records the handler was asked to keep).
func levelName(level slog.Level) string {
	switch {
	case level <= slog.LevelDebug:
		return "debug"
	case level <= slog.LevelInfo:
		return "info"
	case level <= slog.LevelWarn:
		return "warn"
	default:
		return "error"
	}
}

// CaptureBufferAt swaps in a process-global logger that writes to a fresh buffer
// at the given minimum level, registers cleanups to restore the previous global
// and close the capture logger, and returns the buffer. Use it when a test must
// prove a field is emitted at a specific level: capturing at slog.LevelInfo
// drops Debug records, so a regression that moves a field to Debug-only fails
// the test (a Debug-level capture would still see it and hide the regression).
//
// It mutates process-wide global state, so a test using it must NOT call
// t.Parallel().
func CaptureBufferAt(tb testing.TB, level slog.Level) *bytes.Buffer {
	tb.Helper()

	var buf bytes.Buffer
	handler := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: level})
	cl, err := logger.NewCentralLogger(&logger.LoggingConfig{
		DefaultLevel: levelName(level),
		Console:      &logger.ConsoleOutput{Enabled: false},
		FileOutput:   &logger.FileOutput{Enabled: false},
	}, handler)
	if err != nil {
		tb.Fatalf("logtest: failed to create capture logger: %v", err)
	}
	// Registered before the global swap so it runs last (LIFO): the global is
	// restored first, then the capture logger is closed.
	tb.Cleanup(func() { _ = cl.Close() })

	prev := logger.Global()
	logger.SetGlobal(cl)
	tb.Cleanup(func() { logger.SetGlobal(prev) })

	return &buf
}

// CaptureBuffer is CaptureBufferAt at debug level: it captures everything the
// process-global logger emits. The caller runs whatever emits logs and then
// reads buf.String(); everything routed through logger.Global() (including any
// Module derived from it) is captured.
//
// It mutates process-wide global state, so a test using it must NOT call
// t.Parallel().
func CaptureBuffer(tb testing.TB) *bytes.Buffer {
	tb.Helper()
	return CaptureBufferAt(tb, slog.LevelDebug)
}

// Capture runs fn with the process-global logger swapped for a debug-level
// buffer-backed one and returns everything fn logged. It is the convenience
// form of CaptureBuffer for the common "run this, assert on the output" shape.
//
// Like CaptureBuffer it mutates global state, so callers must NOT use
// t.Parallel().
func Capture(tb testing.TB, fn func()) string {
	tb.Helper()
	buf := CaptureBuffer(tb)
	fn()
	return buf.String()
}
