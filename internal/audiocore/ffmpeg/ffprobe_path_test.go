package ffmpeg_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tphakala/birdnet-go/internal/audiocore/ffmpeg"
)

func TestSetAndGetFFprobePath(t *testing.T) {
	original := ffmpeg.GetFFprobePath()
	t.Cleanup(func() { ffmpeg.SetFFprobePath(original) })

	ffmpeg.SetFFprobePath("")
	assert.Empty(t, ffmpeg.GetFFprobePath())

	ffmpeg.SetFFprobePath("/custom/path/to/ffprobe")
	assert.Equal(t, "/custom/path/to/ffprobe", ffmpeg.GetFFprobePath())
}
