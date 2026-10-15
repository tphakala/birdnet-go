package classifier

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestResults_CopyKeepsAudioTiming(t *testing.T) {
	t.Parallel()
	end := time.Now()
	got := Results{AudioEndAt: end, AudioGap: true, PCMdata: []byte{1}}.Copy()
	assert.True(t, got.AudioEndAt.Equal(end))
	assert.True(t, got.AudioGap)
}
