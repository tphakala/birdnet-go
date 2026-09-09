package stream

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOptions_applyDefaults_fillsZeroFields(t *testing.T) {
	got := Options{}
	got.applyDefaults()

	assert.Equal(t, 20*time.Second, got.ReadIdle, "ReadIdle default")
	assert.Equal(t, 4096, got.ChunkBytes, "ChunkBytes default")
	assert.Equal(t, 1*time.Second, got.Backoff.Base, "Backoff.Base native override")
	assert.Equal(t, 2*time.Minute, got.Backoff.Max, "Backoff.Max native override")
	assert.InEpsilon(t, 2.0, got.Backoff.Factor, 1e-9, "Backoff.Factor native override")
	assert.InEpsilon(t, 0.2, got.Backoff.Jitter, 1e-9, "Backoff.Jitter native override")

	require.NotNil(t, got.InsecureTLS, "InsecureTLS defaults to a concrete value")
	assert.True(t, *got.InsecureTLS, "InsecureTLS defaults to true for FFmpeg parity")
	assert.True(t, got.insecureTLS(), "insecureTLS() reads the effective value")
}

func TestOptions_applyDefaults_preservesExplicitValues(t *testing.T) {
	strict := false
	in := Options{
		ReadIdle:    5 * time.Second,
		ChunkBytes:  8192,
		InsecureTLS: &strict,
	}
	in.Backoff.Base = 250 * time.Millisecond
	in.applyDefaults()

	assert.Equal(t, 5*time.Second, in.ReadIdle, "explicit ReadIdle preserved")
	assert.Equal(t, 8192, in.ChunkBytes, "explicit ChunkBytes preserved")
	assert.Equal(t, 250*time.Millisecond, in.Backoff.Base, "explicit Backoff.Base preserved")
	assert.Equal(t, 2*time.Minute, in.Backoff.Max, "unset Backoff.Max still defaulted")
	require.NotNil(t, in.InsecureTLS)
	assert.False(t, *in.InsecureTLS, "explicit InsecureTLS=false preserved")
	assert.False(t, in.insecureTLS(), "insecureTLS() honours an explicit false")
}

func TestOptions_insecureTLS_nilDefaultsTrue(t *testing.T) {
	o := Options{}
	assert.True(t, o.insecureTLS(), "nil InsecureTLS reads as true")
}
