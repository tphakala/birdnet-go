package ffmpeg

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/tphakala/birdnet-go/internal/audiocore"
)

// engineRecorder is a StreamMetrics stub that records the SetStreamEngine call so
// a test can assert the FFmpeg producer stamps its ingest engine at construction.
type engineRecorder struct {
	sourceID string
	engine   string
	called   bool
}

func (e *engineRecorder) IncStreamErrors(string)         {}
func (e *engineRecorder) SetStreamHealth(string, bool)   {}
func (e *engineRecorder) RecordDataRate(string, float64) {}
func (e *engineRecorder) RecordWireRate(string, float64) {}
func (e *engineRecorder) SetStreamEngine(sourceID, engine string) {
	e.sourceID, e.engine, e.called = sourceID, engine, true
}
func (e *engineRecorder) DeleteStream(string) {}

// TestNewStream_RecordsFFmpegEngine locks that the FFmpeg producer records its
// ingest engine to the metrics collector at construction, so the
// audio_stream_engine gauge is populated for the default FFmpeg path (Forgejo
// #1646). The native producer emits EngineNative in its own constructor; this
// closes the sibling gap where FFmpeg previously never called SetStreamEngine.
func TestNewStream_RecordsFFmpegEngine(t *testing.T) {
	t.Parallel()

	rec := &engineRecorder{}
	_ = NewStream(&StreamConfig{URL: "rtsp://camera.local:554/stream", SourceID: "src-1"}, nil, nil, rec, nil)

	assert.True(t, rec.called, "NewStream must record the ingest engine")
	assert.Equal(t, "src-1", rec.sourceID)
	assert.Equal(t, audiocore.EngineFFmpeg, rec.engine)
}

// TestNewStream_NilMetricsSafe ensures construction with a nil metrics collector
// does not panic (the engine emission is nil-guarded).
func TestNewStream_NilMetricsSafe(t *testing.T) {
	t.Parallel()

	assert.NotPanics(t, func() {
		_ = NewStream(&StreamConfig{URL: "rtsp://camera.local:554/stream", SourceID: "src-2"}, nil, nil, nil, nil)
	})
}
