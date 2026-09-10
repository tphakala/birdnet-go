package logtest_test

import (
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/logger/logtest"
)

// TestCapture verifies that Capture routes what the process-global logger emits
// during fn into the returned string.
func TestCapture(t *testing.T) {
	// Not parallel: swaps the process-global logger.
	out := logtest.Capture(t, func() {
		logger.Global().Module("logtest").Info("hello from logtest", logger.String("marker", "abc123"))
	})
	assert.Contains(t, out, "hello from logtest")
	assert.Contains(t, out, "marker=abc123")
}

// TestCaptureBuffer verifies the buffer form captures logs emitted after the
// swap and that the returned buffer is readable by the caller.
func TestCaptureBuffer(t *testing.T) {
	// Not parallel: swaps the process-global logger.
	buf := logtest.CaptureBuffer(t)
	logger.Global().Module("logtest").Warn("buffered line", logger.Int("n", 7))
	out := buf.String()
	assert.Contains(t, out, "level=WARN")
	assert.Contains(t, out, "buffered line")
	assert.Contains(t, out, "n=7")
}

// TestCaptureBufferAtLevel verifies that a level-scoped capture drops records
// below the requested level. This is the property the birdweather attribution
// test relies on: an Info capture must NOT see a Debug line, so a field
// accidentally downgraded to Debug fails the test instead of slipping through.
func TestCaptureBufferAtLevel(t *testing.T) {
	// Not parallel: swaps the process-global logger.
	buf := logtest.CaptureBufferAt(t, slog.LevelInfo)
	log := logger.Global().Module("logtest")
	log.Debug("debug-line-must-be-dropped")
	log.Info("info-line-must-be-kept")
	out := buf.String()
	assert.NotContains(t, out, "debug-line-must-be-dropped",
		"an Info-level capture must filter out Debug records")
	assert.Contains(t, out, "info-line-must-be-kept")
}

// TestCaptureRestoresGlobal verifies the previous global logger is actually
// restored after a capturing subtest's cleanup runs, by pointer identity: a
// prior version of this test asserted only that a later capture did not contain
// the earlier line, which holds by construction (each capture gets a fresh
// buffer) and so proved nothing about restoration.
func TestCaptureRestoresGlobal(t *testing.T) {
	// Not parallel: swaps the process-global logger.
	orig := logger.Global()

	t.Run("inner", func(t *testing.T) {
		buf := logtest.CaptureBuffer(t)
		assert.NotSame(t, orig, logger.Global(),
			"the global logger must be swapped for the capture logger inside the subtest")
		logger.Global().Module("logtest").Info("inner-only")
		assert.Contains(t, buf.String(), "inner-only")
	})

	// The inner subtest has returned, so its Cleanup has run. If SetGlobal(prev)
	// were not registered, this would observe the un-restored capture logger.
	assert.Same(t, orig, logger.Global(),
		"the previous global logger must be restored after the capturing subtest")
}
