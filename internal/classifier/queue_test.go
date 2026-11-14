package classifier

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestResults_CopyKeepsAudioTimingAndDeepCopiesPCMAndEmbedding(t *testing.T) {
	t.Parallel()
	end := time.Now()
	src := Results{Audio: AudioTiming{EndAt: end, Gap: true, FreshBytes: 7}, PCMdata: []byte{1}, ModelID: "m", Embedding: []float32{0.5}, EmbeddingSpace: "space"}
	got := src.Copy()
	assert.True(t, got.Audio.EndAt.Equal(end))
	assert.True(t, got.Audio.Gap)
	assert.Equal(t, 7, got.Audio.FreshBytes)
	assert.Equal(t, "m", got.ModelID)

	got.PCMdata[0] = 9
	assert.Equal(t, []byte{1}, src.PCMdata, "PCM must be deep copied")

	assert.Equal(t, "space", got.EmbeddingSpace)
	got.Embedding[0] = 9
	assert.Equal(t, []float32{0.5}, src.Embedding, "the embedding must be deep copied")
}
