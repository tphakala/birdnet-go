package engine

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/tphakala/birdnet-go/internal/audiocore"
)

// TestBuildStreamSpec verifies that buildStreamSpec stamps the engine-owned
// Debug field onto a caller-assembled StreamSpec and otherwise returns it
// unchanged, covering both debug settings. Every column uses a distinct value so
// a field the helper accidentally overwrote would be caught.
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
			// The call site assembles every field except Debug, which the engine
			// owns. Start Debug at the opposite of the wanted value so the
			// assertion proves buildStreamSpec actively stamps it, not that it
			// happened to match.
			in := tt.want
			in.Debug = !tt.debug
			got := e.buildStreamSpec(&in)
			assert.Equal(t, &tt.want, got)
		})
	}
}
