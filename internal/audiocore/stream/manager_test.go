package stream

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/tphakala/birdnet-go/internal/audiocore"
)

// unreachableRTSP points at a closed local port so the supervisor fails to
// connect and backs off, exercising the lifecycle without a live server.
const unreachableRTSP = "rtsp://127.0.0.1:1/none"

func rtspSpec(id string) *audiocore.StreamSpec {
	return &audiocore.StreamSpec{
		SourceID:   id,
		SourceName: id,
		URL:        unreachableRTSP,
		Type:       audiocore.SourceTypeRTSP,
		SampleRate: 48000,
		Channels:   1,
		BitDepth:   16,
	}
}

func newTestManager(t *testing.T) *Manager {
	t.Helper()
	m := NewManager(t.Context(), func(audiocore.AudioFrame) {}, nil, nil, nil, nil)
	t.Cleanup(func() { assert.NoError(t, m.Shutdown()) })
	return m
}

func TestManager_StartStream_rejectsNonRTSP(t *testing.T) {
	m := newTestManager(t)
	err := m.StartStream(&audiocore.StreamSpec{SourceID: "h1", URL: "http://host/stream", Type: audiocore.SourceTypeHTTP})
	require.ErrorIs(t, err, ErrUnsupportedType)
	assert.Empty(t, m.GetActiveStreamIDs(), "a rejected stream is not tracked")
}

func TestManager_StartStream_rejectsDuplicate(t *testing.T) {
	m := newTestManager(t)
	require.NoError(t, m.StartStream(rtspSpec("s1")))
	err := m.StartStream(rtspSpec("s1"))
	require.Error(t, err, "a duplicate source ID is rejected")
	assert.Equal(t, []string{"s1"}, m.GetActiveStreamIDs())
}

func TestManager_StopStream_unknownReportsActiveCount(t *testing.T) {
	m := newTestManager(t)
	require.NoError(t, m.StartStream(rtspSpec("s1")))
	err := m.StopStream("missing")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "1 active")
}

func TestManager_StreamHealth_unknownErrors(t *testing.T) {
	m := newTestManager(t)
	_, err := m.StreamHealth("missing")
	require.Error(t, err)
	assert.ErrorIs(t, err, audiocore.ErrSourceNotFound)
}

func TestManager_lifecycle_tracksHealthAndShutsDownCleanly(t *testing.T) {
	// Snapshot the goroutines that exist before the test (deferred args evaluate
	// now, at the defer statement), so the check flags only NEW goroutines such
	// as a leaked supervisor or reader, rather than filtering by top-of-stack
	// function, which can hide a parked leaked goroutine.
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())
	m := NewManager(t.Context(), func(audiocore.AudioFrame) {}, nil, nil, nil, nil)

	require.NoError(t, m.StartStream(rtspSpec("s1")))
	require.NoError(t, m.StartStream(rtspSpec("s2")))
	assert.ElementsMatch(t, []string{"s1", "s2"}, m.GetActiveStreamIDs())

	h, err := m.StreamHealth("s1")
	require.NoError(t, err)
	require.NotNil(t, h)
	assert.Contains(t, []audiocore.StreamState{audiocore.StreamStateStarting, audiocore.StreamStateReconnecting}, h.State)

	all := m.AllStreamHealth()
	assert.Len(t, all, 2)

	require.NoError(t, m.StopStream("s1"))
	assert.Equal(t, []string{"s2"}, m.GetActiveStreamIDs())

	require.NoError(t, m.Shutdown())
	assert.Empty(t, m.GetActiveStreamIDs())
	// Shutdown is idempotent.
	require.NoError(t, m.Shutdown())
}

func TestManager_SetOnStreamReset_firesOnStart(t *testing.T) {
	m := newTestManager(t)
	// A callback set after construction must reach a later StartStream, and the
	// reset must fire once with the started source's ID (synchronously, matching
	// the FFmpeg producer and the shared contract's caseOnResetFires).
	var gotID string
	m.SetOnStreamReset(func(id string) { gotID = id })
	require.NoError(t, m.StartStream(rtspSpec("s1")))
	assert.Equal(t, "s1", gotID)
}
