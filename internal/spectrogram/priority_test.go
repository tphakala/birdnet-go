package spectrogram

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/errors"
)

func TestRunAtLowPriority_ReturnsErrorAndPanic(t *testing.T) {
	t.Parallel()

	require.NoError(t, runAtLowPriority(func() error { return nil }))

	sentinel := errors.NewStd("render failed")
	require.ErrorIs(t, runAtLowPriority(func() error { return sentinel }), sentinel)

	err := runAtLowPriority(func() error { panic("boom") })
	require.Error(t, err)
	assert.Contains(t, err.Error(), "boom", "a panic is reported as an error")
}
