package onnx

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/errors"
)

// failingEntrySetter rejects every config entry, standing in for an ORT
// session options object whose AddConfigEntry fails.
type failingEntrySetter struct {
	err error
}

func (f *failingEntrySetter) AddSessionConfigEntry(string, string) error { return f.err }

func TestKleidiAIWorkaroundNeeded(t *testing.T) {
	t.Parallel()

	tests := []struct {
		version string
		want    bool
	}{
		{"1.25.1", true},
		{"1.25.0", true},
		{"1.26.0", true},
		{"1.26.99", true},
		// A string compare would order "1.3.0" after "1.27.0".
		{"1.3.0", true},
		{"0.9.0", true},
		{"1.27.0", false},
		{"1.27.0-rc1", false},
		{"1.31.0", false},
		{"2.0.0", false},
		// Unparseable versions get the workaround: the leak is silent and the
		// entry is harmless on a fixed library.
		{"", true},
		{"garbage", true},
		{"1", true},
	}

	for _, tt := range tests {
		t.Run(tt.version, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, KleidiAIWorkaroundNeeded(tt.version))
		})
	}
}

func TestApplyKleidiAIWorkaround_AddsEntryBelowFixedVersion(t *testing.T) {
	t.Parallel()

	opts := &recordingSessionOptions{}
	require.NoError(t, ApplyKleidiAIWorkaround(opts, "1.25.1"))

	assert.Equal(t, map[string]string{"mlas.disable_kleidiai": "1"}, opts.configEntries)
}

func TestApplyKleidiAIWorkaround_LeavesFixedVersionsAlone(t *testing.T) {
	t.Parallel()

	for _, version := range []string{"1.27.0", "1.31.0"} {
		opts := &recordingSessionOptions{}
		require.NoError(t, ApplyKleidiAIWorkaround(opts, version))
		assert.Empty(t, opts.configEntries, "no entry expected for ORT %s", version)
	}
}

func TestApplyKleidiAIWorkaround_PropagatesSetterError(t *testing.T) {
	t.Parallel()

	setErr := errors.NewStd("config entry rejected")
	err := ApplyKleidiAIWorkaround(&failingEntrySetter{err: setErr}, "1.25.1")

	require.Error(t, err)
	assert.ErrorIs(t, err, setErr)
}

// TestKleidiAIFixedVersion_MatchesCompareConstants keeps the log-facing version
// string in step with the numbers the workaround decision compares against.
func TestKleidiAIFixedVersion_MatchesCompareConstants(t *testing.T) {
	t.Parallel()

	major, minor, ok := ParseORTVersion(KleidiAIFixedVersion)
	require.True(t, ok)
	assert.Equal(t, kleidiAILeakFixedMajor, major)
	assert.Equal(t, kleidiAILeakFixedMinor, minor)
	assert.False(t, KleidiAIWorkaroundNeeded(KleidiAIFixedVersion))
}
