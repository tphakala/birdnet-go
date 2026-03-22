package audiocore

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestAudioFrame_ZeroValue(t *testing.T) {
	t.Parallel()
	var frame AudioFrame
	assert.Empty(t, frame.SourceID)
	assert.Empty(t, frame.SourceName)
	assert.Nil(t, frame.Data)
	assert.Zero(t, frame.SampleRate)
	assert.Zero(t, frame.BitDepth)
	assert.Zero(t, frame.Channels)
	assert.True(t, frame.Timestamp.IsZero())
}

func TestAudioFrame_Construction(t *testing.T) {
	t.Parallel()
	now := time.Now()
	frame := AudioFrame{
		SourceID:   "rtsp_001",
		SourceName: "Frontyard birdfeeder",
		Data:       []byte{0x01, 0x02, 0x03},
		SampleRate: 48000,
		BitDepth:   16,
		Channels:   1,
		Timestamp:  now,
	}
	assert.Equal(t, "rtsp_001", frame.SourceID)
	assert.Equal(t, "Frontyard birdfeeder", frame.SourceName)
	assert.Equal(t, []byte{0x01, 0x02, 0x03}, frame.Data)
	assert.Equal(t, 48000, frame.SampleRate)
	assert.Equal(t, 16, frame.BitDepth)
	assert.Equal(t, 1, frame.Channels)
	assert.Equal(t, now, frame.Timestamp)
}
