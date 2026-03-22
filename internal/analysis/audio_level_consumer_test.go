package analysis

import (
	"encoding/binary"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/audiocore"
)

func TestAudioLevelConsumer_NewAndAccessors(t *testing.T) {
	t.Parallel()

	consumer, ch := NewAudioLevelConsumer("alc-1", 48000, 16, 1)
	require.NotNil(t, consumer)
	require.NotNil(t, ch)

	assert.Equal(t, "alc-1", consumer.ID())
	assert.Equal(t, 48000, consumer.SampleRate())
	assert.Equal(t, 16, consumer.BitDepth())
	assert.Equal(t, 1, consumer.Channels())
}

func TestAudioLevelConsumer_WritePublishesLevel(t *testing.T) {
	t.Parallel()

	consumer, ch := NewAudioLevelConsumer("alc-2", 48000, 16, 1)

	// Build a non-silent frame so we get a level > 0.
	const amplitude int16 = 10000
	numSamples := 480
	data := make([]byte, numSamples*2)
	for i := range numSamples {
		binary.LittleEndian.PutUint16(data[i*2:], uint16(amplitude)) //nolint:gosec // test-only
	}

	frame := audiocore.AudioFrame{
		SourceID:   "src-1",
		SourceName: "Source One",
		Data:       data,
		SampleRate: 48000,
		BitDepth:   16,
		Channels:   1,
		Timestamp:  time.Now(),
	}

	err := consumer.Write(frame)
	require.NoError(t, err)

	select {
	case level := <-ch:
		assert.Equal(t, "src-1", level.Source)
		assert.Equal(t, "Source One", level.Name)
		assert.Positive(t, level.Level)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for audio level data on channel")
	}
}

func TestAudioLevelConsumer_WriteSilencePublishesZero(t *testing.T) {
	t.Parallel()

	consumer, ch := NewAudioLevelConsumer("alc-3", 48000, 16, 1)

	// 100 samples of silence.
	frame := audiocore.AudioFrame{
		SourceID:   "src-2",
		SourceName: "Silent",
		Data:       make([]byte, 200),
		SampleRate: 48000,
		BitDepth:   16,
		Channels:   1,
		Timestamp:  time.Now(),
	}

	err := consumer.Write(frame)
	require.NoError(t, err)

	select {
	case level := <-ch:
		assert.Equal(t, 0, level.Level)
		assert.False(t, level.Clipping)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for audio level data on channel")
	}
}

func TestAudioLevelConsumer_DropsWhenChannelFull(t *testing.T) {
	t.Parallel()

	consumer, ch := NewAudioLevelConsumer("alc-4", 48000, 16, 1)

	frame := audiocore.AudioFrame{
		SourceID:   "src-3",
		SourceName: "Flood",
		Data:       make([]byte, 200),
		SampleRate: 48000,
		BitDepth:   16,
		Channels:   1,
		Timestamp:  time.Now(),
	}

	// Fill the channel to capacity.
	for range audioLevelChanSize {
		err := consumer.Write(frame)
		require.NoError(t, err)
	}

	// This write should succeed (drop silently) even though channel is full.
	err := consumer.Write(frame)
	require.NoError(t, err)

	// Channel should still have exactly audioLevelChanSize items.
	assert.Len(t, ch, audioLevelChanSize)
}

func TestAudioLevelConsumer_CloseRejectsWrites(t *testing.T) {
	t.Parallel()

	consumer, _ := NewAudioLevelConsumer("alc-5", 48000, 16, 1)
	require.NoError(t, consumer.Close())

	frame := audiocore.AudioFrame{
		SourceID:   "src-4",
		SourceName: "Closed",
		Data:       make([]byte, 200),
		SampleRate: 48000,
		BitDepth:   16,
		Channels:   1,
		Timestamp:  time.Now(),
	}

	err := consumer.Write(frame)
	assert.ErrorIs(t, err, audiocore.ErrConsumerClosed)
}
