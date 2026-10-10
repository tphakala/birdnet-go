package onnx

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recordingSessionOptions records the config entries applySessionDefaults sets,
// standing in for *ort.SessionOptions so the test runs without the ONNX Runtime
// library.
type recordingSessionOptions struct {
	configEntries map[string]string
}

func (r *recordingSessionOptions) SetIntraOpNumThreads(int) error { return nil }

func (r *recordingSessionOptions) SetInterOpNumThreads(int) error { return nil }

func (r *recordingSessionOptions) AddSessionConfigEntry(key, value string) error {
	if r.configEntries == nil {
		r.configEntries = make(map[string]string)
	}
	r.configEntries[key] = value
	return nil
}

// TestApplySessionDefaults_DisablesThreadPoolSpinning guards against ONNX
// Runtime's default spin-waiting. With spinning on, idle pool workers
// busy-wait between ops and after each run, so a ~25-50 ms inference every
// 1.5 s burned ~1.7 s of CPU across four threads on an Intel N97.
func TestApplySessionDefaults_DisablesThreadPoolSpinning(t *testing.T) {
	t.Parallel()

	opts := &recordingSessionOptions{}
	require.NoError(t, applySessionDefaults(opts))

	assert.Equal(t, "0", opts.configEntries["session.intra_op.allow_spinning"],
		"intra-op pool workers must block instead of spin-waiting when idle")
	assert.Equal(t, "0", opts.configEntries["session.inter_op.allow_spinning"],
		"inter-op pool workers must block instead of spin-waiting when idle")
}
