package analysis

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tphakala/birdnet-go/internal/audiocore"
)

func TestSourceNeedsReconfigure(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		running  *audiocore.AudioSource
		desired  *audiocore.SourceConfig
		expected bool
	}{
		{
			name: "same config, no reconfigure needed",
			running: &audiocore.AudioSource{
				SampleRate: 48000,
				BitDepth:   16,
				Channels:   1,
			},
			desired: &audiocore.SourceConfig{
				SampleRate: 48000,
				BitDepth:   16,
				Channels:   1,
			},
			expected: false,
		},
		{
			name: "sample rate changed",
			running: &audiocore.AudioSource{
				SampleRate: 48000,
				BitDepth:   16,
				Channels:   1,
			},
			desired: &audiocore.SourceConfig{
				SampleRate: 96000,
				BitDepth:   16,
				Channels:   1,
			},
			expected: true,
		},
		{
			name: "bit depth changed",
			running: &audiocore.AudioSource{
				SampleRate: 48000,
				BitDepth:   16,
				Channels:   1,
			},
			desired: &audiocore.SourceConfig{
				SampleRate: 48000,
				BitDepth:   32,
				Channels:   1,
			},
			expected: true,
		},
		{
			name: "channels changed",
			running: &audiocore.AudioSource{
				SampleRate: 48000,
				BitDepth:   16,
				Channels:   1,
			},
			desired: &audiocore.SourceConfig{
				SampleRate: 48000,
				BitDepth:   16,
				Channels:   2,
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := sourceNeedsReconfigure(tt.running, tt.desired)
			assert.Equal(t, tt.expected, result)
		})
	}
}
