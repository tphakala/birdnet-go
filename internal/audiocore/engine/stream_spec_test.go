package engine

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/tphakala/birdnet-go/internal/audiocore"
)

// TestBuildStreamSpec verifies that buildStreamSpec maps its parameters onto the
// StreamSpec field-for-field, reproducing the three former ffmpeg.StreamConfig
// literals in AddSource, the reconfigure path, and the quiet-hours restart path.
// Every column uses a distinct value so a transposed positional argument in the
// twelve-parameter helper is caught. Debug is read from the engine, not passed.
func TestBuildStreamSpec(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		debug bool
		want  audiocore.StreamSpec
	}{
		{
			name:  "rtsp downmix auto tcp, debug on",
			debug: true,
			want: audiocore.StreamSpec{
				SourceID:         "src-a",
				SourceName:       "Camera A",
				URL:              "rtsp://a.example/stream",
				Type:             audiocore.SourceTypeRTSP,
				SampleRate:       48000,
				SourceSampleRate: 44100,
				BitDepth:         16,
				Channels:         1,
				SourceChannels:   2,
				ChannelMode:      "downmix",
				MediaMode:        "auto",
				Transport:        "tcp",
				Debug:            true,
			},
		},
		{
			name:  "http left audio-only udp, debug off",
			debug: false,
			want: audiocore.StreamSpec{
				SourceID:         "src-b",
				SourceName:       "Restreamer B",
				URL:              "http://b.example/stream.mp3",
				Type:             audiocore.SourceTypeHTTP,
				SampleRate:       96000,
				SourceSampleRate: 0,
				BitDepth:         24,
				Channels:         1,
				SourceChannels:   0,
				ChannelMode:      "left",
				MediaMode:        "audio-only",
				Transport:        "udp",
				Debug:            false,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			e := &AudioEngine{debug: tt.debug}
			got := e.buildStreamSpec(
				tt.want.SourceID,
				tt.want.SourceName,
				tt.want.URL,
				tt.want.Type,
				tt.want.SampleRate,
				tt.want.SourceSampleRate,
				tt.want.BitDepth,
				tt.want.Channels,
				tt.want.SourceChannels,
				tt.want.ChannelMode,
				tt.want.MediaMode,
				tt.want.Transport,
			)
			assert.Equal(t, &tt.want, got)
		})
	}
}
