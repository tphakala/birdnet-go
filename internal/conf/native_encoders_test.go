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

func TestNativeOpusEncoderEnabled(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  bool
	}{
		{name: "unset keeps ffmpeg", value: "", want: false},
		{name: "native opts in", value: "native", want: true},
		{name: "uppercase opts in", value: "NATIVE", want: true},
		{name: "ffmpeg stays ffmpeg", value: "ffmpeg", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(EnvNativeOpusEncoder, tt.value)
			assert.Equal(t, tt.want, NativeOpusEncoderEnabled())
		})
	}
}

// The two gates are independent so one codec can be promoted to native while
// the other stays on FFmpeg.
func TestGatesAreIndependent(t *testing.T) {
	t.Setenv(EnvNativeAACEncoder, "native")
	t.Setenv(EnvNativeOpusEncoder, "")
	assert.True(t, NativeAACEncoderEnabled())
	assert.False(t, NativeOpusEncoderEnabled())

	t.Setenv(EnvNativeAACEncoder, "")
	t.Setenv(EnvNativeOpusEncoder, "native")
	assert.False(t, NativeAACEncoderEnabled())
	assert.True(t, NativeOpusEncoderEnabled())
}
