package inference

import (
	"bytes"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/errors"
	ov "github.com/tphakala/birdnet-go/internal/inference/openvino"
	"github.com/tphakala/birdnet-go/internal/logger/logtest"
)

// TestNewOpenVINOClassifier_RequiresLabels verifies the labels precondition,
// which holds in every build (the check runs before any OpenVINO call).
func TestNewOpenVINOClassifier_RequiresLabels(t *testing.T) {
	t.Parallel()
	c, err := NewOpenVINOClassifier("/x.onnx", OpenVINOClassifierOptions{})
	require.Error(t, err)
	require.ErrorContains(t, err, "requires labels")
	assert.Nil(t, c)
}

// faultClassifier is an ov.Classifier whose PredictRaw always fails with err.
type faultClassifier struct{ err error }

func (f faultClassifier) PredictRaw([]float32) ([]float32, error) { return nil, f.err }
func (f faultClassifier) NumClasses() int                         { return 1 }
func (f faultClassifier) Close() error                            { return nil }

// ovGPUFaultLogCount counts the GPU fault ERROR records in a captured log.
func ovGPUFaultLogCount(buf *bytes.Buffer) int {
	return strings.Count(buf.String(), "OpenVINO GPU failed")
}

// TestLogOVGPUFaultOnce_FiresOncePerProcess pins that only an error wrapping
// ov.ErrGPUDeviceFault produces the ERROR log, that it is written once however
// many fault errors follow, and that the error is returned unchanged. Not
// parallel: touches the process-wide ovGPUFaultLogged and the global logger.
func TestLogOVGPUFaultOnce_FiresOncePerProcess(t *testing.T) {
	prevLogged := ovGPUFaultLogged.Swap(false)
	t.Cleanup(func() { ovGPUFaultLogged.Store(prevLogged) })
	buf := logtest.CaptureBufferAt(t, slog.LevelError)

	plain := errors.NewStd("some other failure")
	require.Same(t, plain, logOVGPUFaultOnce(plain))
	require.NoError(t, logOVGPUFaultOnce(nil))
	assert.Zero(t, ovGPUFaultLogCount(buf), "an unrelated error is not logged")

	fault := errors.Newf("%w: stopped", ov.ErrGPUDeviceFault).Build()
	require.Same(t, fault, logOVGPUFaultOnce(fault))
	assert.Same(t, fault, logOVGPUFaultOnce(fault))
	assert.Equal(t, 1, ovGPUFaultLogCount(buf), "the fault is logged once, not per call")
}

// TestOpenVINOWrappers_LogGPUFault pins that the classifier and the embedding
// extractor route a latched GPU error through the one-time ERROR log, so a
// model family cannot drop it. Not parallel: touches process-wide state.
func TestOpenVINOWrappers_LogGPUFault(t *testing.T) {
	fault := errors.Newf("%w: stopped", ov.ErrGPUDeviceFault).Build()
	tests := []struct {
		name string
		call func() error
	}{
		{"classifier Predict", func() error {
			_, err := (&openvinoClassifier{c: faultClassifier{err: fault}}).Predict(nil)
			return err
		}},
		{"extractor PredictWithEmbeddings", func() error {
			_, _, err := (&openvinoEmbeddingExtractor{c: faultClassifier{err: fault}}).PredictWithEmbeddings(nil)
			return err
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prevLogged := ovGPUFaultLogged.Swap(false)
			t.Cleanup(func() { ovGPUFaultLogged.Store(prevLogged) })
			buf := logtest.CaptureBufferAt(t, slog.LevelError)
			require.ErrorIs(t, tt.call(), ov.ErrGPUDeviceFault)
			assert.Equal(t, 1, ovGPUFaultLogCount(buf))
		})
	}
}

// TestRegisterOpenVINOGPUFaultObserver pins that the observer runs once across
// repeated fault errors, never for a plain error, and that a stale unregister
// leaves a newer registration in place. Not parallel: touches process-wide state.
func TestRegisterOpenVINOGPUFaultObserver(t *testing.T) {
	prevLogged := ovGPUFaultLogged.Swap(false)
	t.Cleanup(func() { ovGPUFaultLogged.Store(prevLogged) })
	logtest.CaptureBufferAt(t, slog.LevelError)

	var calls atomic.Int32
	unregister := RegisterOpenVINOGPUFaultObserver(func() { calls.Add(1) })
	t.Cleanup(unregister)

	_ = logOVGPUFaultOnce(errors.NewStd("some other failure"))
	assert.Zero(t, calls.Load(), "a plain error does not notify")

	fault := errors.Newf("%w: stopped", ErrOpenVINOGPUFault).Build()
	_ = logOVGPUFaultOnce(fault)
	_ = logOVGPUFaultOnce(fault)
	assert.Equal(t, int32(1), calls.Load(), "the observer runs once per process")

	var newer atomic.Int32
	unregisterNewer := RegisterOpenVINOGPUFaultObserver(func() { newer.Add(1) })
	t.Cleanup(unregisterNewer)
	unregister() // stale: must not clear the newer registration
	ovGPUFaultLogged.Store(false)
	_ = logOVGPUFaultOnce(fault)
	assert.Equal(t, int32(1), newer.Load(), "a stale unregister keeps the newer observer")
	assert.Equal(t, int32(1), calls.Load())

	unregisterNewer()
	ovGPUFaultLogged.Store(false)
	_ = logOVGPUFaultOnce(fault)
	assert.Equal(t, int32(1), newer.Load(), "an unregistered observer is not called")
}
