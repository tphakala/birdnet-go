package conf

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNativeAACEncoderEnabled(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  bool
	}{
		{name: "unset keeps ffmpeg", value: "", want: false},
		{name: "native opts in", value: "native", want: true},
		{name: "uppercase opts in", value: "NATIVE", want: true},
		{name: "mixed case opts in", value: "Native", want: true},
		{name: "surrounding whitespace opts in", value: "  native ", want: true},
		{name: "ffmpeg stays ffmpeg", value: "ffmpeg", want: false},
		{name: "truthy value is not enough", value: "1", want: false},
		{name: "typo stays ffmpeg", value: "nativ", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(EnvNativeAACEncoder, tt.value)
			assert.Equal(t, tt.want, NativeAACEncoderEnabled())
		})
	}
}

// AAC is now the only gated native encoder: go-opus (Opus) and go-hls (HLS) are
// both unconditional defaults, so their gates have been removed.
