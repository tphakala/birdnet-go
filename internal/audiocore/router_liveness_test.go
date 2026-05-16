package audiocore

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRouter_LastDispatchTime_NoDispatch(t *testing.T) {
	r := NewAudioRouter(GetLogger(), nil)
	defer r.Close()

	ts := r.LastDispatchTime("nonexistent")
	assert.True(t, ts.IsZero(), "expected zero time for unknown source")
}

func TestRouter_LastDispatchTime_AfterDispatch(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := NewAudioRouter(GetLogger(), nil)
		defer r.Close()

		frame := AudioFrame{SourceID: "test-src", Data: []byte{0, 0}}
		r.Dispatch(frame)

		ts := r.LastDispatchTime("test-src")
		require.False(t, ts.IsZero(), "expected non-zero time after dispatch")
		assert.WithinDuration(t, time.Now(), ts, time.Second)
	})
}

func TestRouter_ResetDispatchTime(t *testing.T) {
	r := NewAudioRouter(GetLogger(), nil)
	defer r.Close()

	r.ResetDispatchTime("src-1")
	ts := r.LastDispatchTime("src-1")
	require.False(t, ts.IsZero())
	assert.WithinDuration(t, time.Now(), ts, time.Second)
}

func TestRouter_ClearDispatchTime(t *testing.T) {
	r := NewAudioRouter(GetLogger(), nil)
	defer r.Close()

	r.ResetDispatchTime("src-1")
	require.False(t, r.LastDispatchTime("src-1").IsZero())

	r.ClearDispatchTime("src-1")
	assert.True(t, r.LastDispatchTime("src-1").IsZero())
}

func TestRouter_ActiveSourceIDs(t *testing.T) {
	r := NewAudioRouter(GetLogger(), nil)
	defer r.Close()

	assert.Empty(t, r.ActiveSourceIDs())
}
