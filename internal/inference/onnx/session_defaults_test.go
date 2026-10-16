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
	// A fixed ORT version keeps this test about spinning only.
	require.NoError(t, applySessionDefaults(opts, "1.31.0"))

	assert.Equal(t, "0", opts.configEntries["session.intra_op.allow_spinning"],
		"intra-op pool workers must block instead of spin-waiting when idle")
	assert.Equal(t, "0", opts.configEntries["session.inter_op.allow_spinning"],
		"inter-op pool workers must block instead of spin-waiting when idle")
}

// TestApplySessionDefaults_DisablesKleidiAIOnLeakingORT guards the session
// config entry that keeps ORT releases before 1.27 from growing memory on Arm
// SME CPUs, next to the spinning entries.
func TestApplySessionDefaults_DisablesKleidiAIOnLeakingORT(t *testing.T) {
	t.Parallel()

	opts := &recordingSessionOptions{}
	require.NoError(t, applySessionDefaults(opts, "1.25.1"))

	assert.Equal(t, "1", opts.configEntries["mlas.disable_kleidiai"])
	assert.Equal(t, "0", opts.configEntries["session.intra_op.allow_spinning"])
	assert.Equal(t, "0", opts.configEntries["session.inter_op.allow_spinning"])
}

// TestApplySessionDefaults_KeepsKleidiAIOnFixedORT guards that sessions on an
// ORT release with the fix keep the KleidiAI kernels.
func TestApplySessionDefaults_KeepsKleidiAIOnFixedORT(t *testing.T) {
	t.Parallel()

	opts := &recordingSessionOptions{}
	require.NoError(t, applySessionDefaults(opts, "1.31.0"))

	assert.NotContains(t, opts.configEntries, "mlas.disable_kleidiai")
}
