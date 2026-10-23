package datastore

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNote_MediaNameAndSpectrogramOnly(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		note     Note
		wantName string
		wantOnly bool
	}{
		{"audio present", Note{ClipName: "a.wav"}, "a.wav", false},
		{"audio present wins over a stale spectrogram name", Note{ClipName: "a.wav", SpectrogramClipName: "b.wav"}, "a.wav", false},
		{"spectrogram only", Note{SpectrogramClipName: "b.wav"}, "b.wav", true},
		{"neither", Note{}, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.wantName, tt.note.MediaName())
			assert.Equal(t, tt.wantOnly, tt.note.IsSpectrogramOnly())
			assert.Equal(t, tt.wantOnly, IsSpectrogramOnly(tt.note.ClipName, tt.note.SpectrogramClipName))
		})
	}
}
