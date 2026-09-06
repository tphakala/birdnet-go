package ffmpeg

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/tphakala/birdnet-go/internal/errors"
)

// TestIsSilenceTimeoutError locks the silence-restart classification contract:
// the run loop identifies a silence-watchdog restart by the structured
// operation=silence_timeout context stamped by handleSilenceTimeout, not by a
// substring of the (dynamic) error message. The old substring check
// (strings.Contains(msg, "silence timeout")) never matched because the message
// is "stream stopped producing data for N seconds" (Forgejo #1641).
func TestIsSilenceTimeoutError(t *testing.T) {
	t.Parallel()

	// silenceErr is shaped exactly like the error handleSilenceTimeout builds:
	// the message never contains the words "silence timeout", so only the
	// operation context can classify it.
	silenceErr := errors.Newf("stream stopped producing data for %v seconds", 90.0).
		Category(errors.CategoryRTSP).
		Component("ffmpeg-stream").
		Context("operation", opSilenceTimeout).
		Build()

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "real silence-timeout error is classified",
			err:  silenceErr,
			want: true,
		},
		{
			name: "wrapped silence-timeout error still classified",
			err:  fmt.Errorf("outer wrapper: %w", silenceErr),
			want: true,
		},
		{
			// The silence error nested inside ANOTHER EnhancedError whose own
			// operation differs: a single errors.As would stop at the outer one and
			// miss it, so this proves the chain is walked.
			name: "silence error wrapped in a different enhanced error is still classified",
			err: errors.New(silenceErr).
				Component("ffmpeg-stream").
				Context("operation", "process_ended").
				Build(),
			want: true,
		},
		{
			name: "different operation is not a silence timeout",
			err: errors.Newf("error reading from FFmpeg").
				Component("ffmpeg-stream").
				Context("operation", "read").
				Build(),
			want: false,
		},
		{
			name: "enhanced error without an operation context",
			err: errors.Newf("some other failure").
				Component("ffmpeg-stream").
				Build(),
			want: false,
		},
		{
			name: "plain error is not a silence timeout",
			err:  fmt.Errorf("plain error without structured context"),
			want: false,
		},
		{
			name: "nil error",
			err:  nil,
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, isSilenceTimeoutError(tt.err))
		})
	}
}
