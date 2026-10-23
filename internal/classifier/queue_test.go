package classifier

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestResults_CopyKeepsAudioTimingAndDeepCopiesPCM(t *testing.T) {
	t.Parallel()
	end := time.Now()
	src := Results{Audio: AudioTiming{EndAt: end, Gap: true, FreshBytes: 7}, PCMdata: []byte{1}, ModelID: "m"}
	got := src.Copy()
	assert.True(t, got.Audio.EndAt.Equal(end))
	assert.True(t, got.Audio.Gap)
	assert.Equal(t, 7, got.Audio.FreshBytes)
	assert.Equal(t, "m", got.ModelID)

	got.PCMdata[0] = 9
	assert.Equal(t, []byte{1}, src.PCMdata, "PCM must be deep copied")
}
