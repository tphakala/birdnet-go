package nativeenc

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAACEnabled(t *testing.T) {
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
			t.Setenv(EnvAACEncoder, tt.value)
			assert.Equal(t, tt.want, AACEnabled())
		})
	}
}

func TestOpusEnabled(t *testing.T) {
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
			t.Setenv(EnvOpusEncoder, tt.value)
			assert.Equal(t, tt.want, OpusEnabled())
		})
	}
}

// The two gates are independent so one codec can be promoted to native while
// the other stays on FFmpeg.
func TestGatesAreIndependent(t *testing.T) {
	t.Setenv(EnvAACEncoder, "native")
	t.Setenv(EnvOpusEncoder, "")
	assert.True(t, AACEnabled())
	assert.False(t, OpusEnabled())

	t.Setenv(EnvAACEncoder, "")
	t.Setenv(EnvOpusEncoder, "native")
	assert.False(t, AACEnabled())
	assert.True(t, OpusEnabled())
}
