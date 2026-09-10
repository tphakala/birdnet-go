package ffmpeg

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestStreamConfig_effectiveSilenceTimeout pins the accessor that lets the
// silence watchdog run in seconds under test without mutating the package-level
// silenceTimeout constant: an unset field falls back to the production default,
// a set field is honoured verbatim.
func TestStreamConfig_effectiveSilenceTimeout(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		set  time.Duration
		want time.Duration
	}{
		{name: "zero falls back to default", set: 0, want: silenceTimeout},
		{name: "explicit value honoured", set: 5 * time.Second, want: 5 * time.Second},
		{name: "negative falls back to default", set: -1, want: silenceTimeout},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := &StreamConfig{SilenceTimeout: tt.set}
			assert.Equal(t, tt.want, cfg.effectiveSilenceTimeout())
		})
	}
}

// TestManager_withManagerDefaults verifies that the manager-level SilenceTimeout
// option is applied to a per-stream config only when the config leaves it unset,
// and that the caller's config is never mutated in the process.
func TestManager_withManagerDefaults(t *testing.T) {
	t.Parallel()

	t.Run("applies option when config leaves it unset", func(t *testing.T) {
		t.Parallel()
		mgr := NewManagerWithOptions(t.Context(), nil, nil, nil, nil, Options{SilenceTimeout: 5 * time.Second})
		in := &StreamConfig{SourceID: "s1"}
		out := mgr.withManagerDefaults(in)
		assert.Equal(t, 5*time.Second, out.SilenceTimeout)
		assert.Zero(t, in.SilenceTimeout, "caller config must not be mutated")
	})

	t.Run("per-stream value wins over manager option", func(t *testing.T) {
		t.Parallel()
		mgr := NewManagerWithOptions(t.Context(), nil, nil, nil, nil, Options{SilenceTimeout: 5 * time.Second})
		in := &StreamConfig{SourceID: "s1", SilenceTimeout: 42 * time.Second}
		out := mgr.withManagerDefaults(in)
		assert.Equal(t, 42*time.Second, out.SilenceTimeout)
	})

	t.Run("no option leaves config untouched", func(t *testing.T) {
		t.Parallel()
		mgr := NewManagerWithOptions(t.Context(), nil, nil, nil, nil, Options{})
		in := &StreamConfig{SourceID: "s1"}
		out := mgr.withManagerDefaults(in)
		assert.Zero(t, out.SilenceTimeout)
	})

	t.Run("NewManager delegates with zero options", func(t *testing.T) {
		t.Parallel()
		mgr := NewManager(t.Context(), nil, nil, nil, nil)
		require.NotNil(t, mgr)
		in := &StreamConfig{SourceID: "s1"}
		out := mgr.withManagerDefaults(in)
		assert.Zero(t, out.SilenceTimeout, "NewManager must not inject a silence timeout")
	})

	t.Run("negative config value is treated as unset", func(t *testing.T) {
		t.Parallel()
		mgr := NewManagerWithOptions(t.Context(), nil, nil, nil, nil, Options{SilenceTimeout: 5 * time.Second})
		in := &StreamConfig{SourceID: "s1", SilenceTimeout: -1}
		out := mgr.withManagerDefaults(in)
		assert.Equal(t, 5*time.Second, out.SilenceTimeout, "a non-positive config value is unset, so the manager option applies")
	})
}

// TestStream_handleSilenceTimeout pins the seam's point of use: the silence
// watchdog must restart at the configured SilenceTimeout, and a zero value must
// keep the 90 s default. This is the one production behaviour the integration
// SilenceRestart case cannot isolate, because a stopped publisher makes MediaMTX
// tear the reader session down (an EOF-driven restart) before the silence
// watchdog would fire.
func TestStream_handleSilenceTimeout(t *testing.T) {
	t.Parallel()

	const (
		shortTimeout = 5 * time.Second
		beyondShort  = 30 * time.Second // older than shortTimeout, younger than 90 s
		withinShort  = 1 * time.Second
	)

	tests := []struct {
		name           string
		silenceTimeout time.Duration
		lastDataAge    time.Duration
		wantRestart    bool
		wantMsgSeconds string
	}{
		{name: "fires at the shortened timeout", silenceTimeout: shortTimeout, lastDataAge: beyondShort, wantRestart: true, wantMsgSeconds: "5 seconds"},
		{name: "quiet within the shortened timeout does not fire", silenceTimeout: shortTimeout, lastDataAge: withinShort, wantRestart: false},
		{name: "zero keeps the 90s default so 30s of silence does not fire", silenceTimeout: 0, lastDataAge: beyondShort, wantRestart: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s := NewStream(&StreamConfig{
				SourceID:       "silence-unit",
				URL:            "rtsp://example.test/stream",
				SilenceTimeout: tt.silenceTimeout,
			}, nil, nil, nil, nil)

			s.lastDataMu.Lock()
			s.lastDataTime = time.Now().Add(-tt.lastDataAge)
			s.lastDataMu.Unlock()

			err := s.handleSilenceTimeout(time.Now())
			if tt.wantRestart {
				require.Error(t, err, "silence past the effective timeout should trigger a restart")
				assert.Contains(t, err.Error(), tt.wantMsgSeconds, "restart error should name the effective timeout")
			} else {
				assert.NoError(t, err, "silence within the effective timeout should not restart")
			}
		})
	}
}
