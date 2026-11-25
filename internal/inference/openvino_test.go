package inference

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/errors"
	ov "github.com/tphakala/birdnet-go/internal/inference/openvino"
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

// TestLogOVGPUFaultOnce_FiresOncePerProcess pins that only an error wrapping
// ov.ErrGPUDeviceFault arms the once-per-process ERROR log and that the error is
// returned unchanged. Not parallel: touches the process-wide ovGPUFaultLogged.
func TestLogOVGPUFaultOnce_FiresOncePerProcess(t *testing.T) {
	ovGPUFaultLogged.Store(false)
	t.Cleanup(func() { ovGPUFaultLogged.Store(false) })

	plain := errors.NewStd("some other failure")
	require.Same(t, plain, logOVGPUFaultOnce(plain))
	assert.False(t, ovGPUFaultLogged.Load(), "an unrelated error does not arm the log")
	require.NoError(t, logOVGPUFaultOnce(nil))

	fault := errors.Newf("%w: stopped", ov.ErrGPUDeviceFault).Build()
	require.Same(t, fault, logOVGPUFaultOnce(fault))
	assert.True(t, ovGPUFaultLogged.Load(), "the first fault error is logged")
	assert.Same(t, fault, logOVGPUFaultOnce(fault))
}
