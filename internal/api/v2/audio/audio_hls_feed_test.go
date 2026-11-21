package audio

import (
	"encoding/binary"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/audiocore"
)

// Frame sizes the HLS consumer sees in the field. They differ by two orders of
// magnitude, which is the whole reason the feed queue is bounded by bytes
// rather than by chunk count.
const (
	// rtspFrameBytes mirrors ffmpegBufferSize in internal/audiocore/ffmpeg,
	// the read size of the FFmpeg ingest that feeds every RTSP source.
	rtspFrameBytes = 32768

	// soundCardFrameBytes is one miniaudio default low-latency period (10 ms)
	// at 48 kHz mono 16-bit, which is what a directly captured USB microphone
	// delivers: internal/audiocore/capture.go builds its device from
	// malgo.DefaultDeviceConfig and never sets an explicit period size.
	soundCardFrameBytes = 960
)

// newTestFeedConsumer builds an hlsConsumer over a feed with the given byte
// budget, wired the way setupAudioCallback wires the real one.
func newTestFeedConsumer(maxBytes int64) (*hlsConsumer, *audioFeed) {
	feed := &audioFeed{
		ch:       make(chan audioChunk, defaultReadBufferSize),
		maxBytes: maxBytes,
	}
	return &hlsConsumer{
		id:       "hls_test",
		sourceID: "rtsp://user:pass@example.invalid/stream",
		feed:     feed,
		rate:     nativeHLSSampleRate,
		depth:    16,
		channels: hlsConsumerChannels,
	}, feed
}

// writeFrame writes one frame of the given size, tagging its first 8 bytes with
// seq so a drained chunk can be traced back to the write that produced it.
func writeFrame(tb testing.TB, h *hlsConsumer, size int, seq uint64) {
	tb.Helper()
	require.GreaterOrEqual(tb, size, 8, "frames must be wide enough to carry the sequence tag")

	data := make([]byte, size)
	binary.BigEndian.PutUint64(data, seq)
	require.NoError(tb, h.Write(audiocore.AudioFrame{
		SourceID:   "src",
		Data:       data,
		SampleRate: nativeHLSSampleRate,
		BitDepth:   16,
		Channels:   hlsConsumerChannels,
		Timestamp:  time.Unix(0, int64(seq)),
	}))
}

// drainFeed empties the queue, reporting the sequence tags in order and the
// total PCM the queue was holding.
func drainFeed(feed *audioFeed) (seqs []uint64, totalBytes int64) {
	for {
		select {
		case chunk, ok := <-feed.ch:
			if !ok {
				return seqs, totalBytes
			}
			feed.release(len(chunk.data))
			seqs = append(seqs, binary.BigEndian.Uint64(chunk.data))
			totalBytes += int64(len(chunk.data))
		default:
			return seqs, totalBytes
		}
	}
}

// TestAudioFeedBoundsQueuedPCM is the regression test for the reason the byte
// budget exists: a 1024-slot channel of 32 KiB RTSP frames can hold 33.5 MB of
// PCM per stream, which is 349 seconds of audio nobody will ever play back.
func TestAudioFeedBoundsQueuedPCM(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		frameSize int
		frames    int
	}{
		{
			// The case from the report: 1024 of these used to queue 33.5 MB.
			name:      "rtsp frames",
			frameSize: rtspFrameBytes,
			frames:    2 * defaultReadBufferSize,
		},
		{
			// Small frames used to be bounded by the slot count long before
			// they reached the byte budget. They must be bounded by bytes now.
			name:      "sound card frames",
			frameSize: soundCardFrameBytes,
			frames:    2 * defaultReadBufferSize,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			h, feed := newTestFeedConsumer(nativeHLSFeedQueueBytes)
			for seq := range tc.frames {
				writeFrame(t, h, tc.frameSize, uint64(seq))
			}

			assert.LessOrEqual(t, feed.queued.Load(), int64(nativeHLSFeedQueueBytes),
				"the producer's byte accounting must stay inside the budget")

			_, totalBytes := drainFeed(feed)
			assert.LessOrEqual(t, totalBytes, int64(nativeHLSFeedQueueBytes),
				"the PCM actually queued must stay inside the budget")
			assert.Equal(t, int64(0), feed.queued.Load(),
				"a fully drained queue must account for zero bytes")
		})
	}
}

// TestAudioFeedKeepsDepthForSmallFrames guards the reason the bound is in bytes
// and not in chunks. A chunk count tight enough to bound 32 KiB RTSP frames
// (8 to 16) would leave a sound card with a sixth of a second of slack; the
// byte budget gives both producers the same multi-second depth.
func TestAudioFeedKeepsDepthForSmallFrames(t *testing.T) {
	t.Parallel()

	h, feed := newTestFeedConsumer(nativeHLSFeedQueueBytes)
	for seq := range defaultReadBufferSize {
		writeFrame(t, h, soundCardFrameBytes, uint64(seq))
	}

	seqs, totalBytes := drainFeed(feed)

	// Depth in time is what actually matters, so assert on that rather than on
	// a chunk count that means something different for every producer.
	const bytesPerSecond = nativeHLSSampleRate * nativeHLSChannels * nativeHLSBytesPerSample
	bufferedSeconds := float64(totalBytes) / bytesPerSecond
	assert.Greater(t, bufferedSeconds, 4.0,
		"the queue must hold more than the ~4s a live listener sits behind the edge")
	assert.Greater(t, len(seqs), 16,
		"a 16-chunk bound would have kept 16 small frames; the byte budget keeps far more")
}

// TestAudioFeedDropsOldest verifies the eviction policy. On a live stream the
// oldest queued audio is the least useful: it is furthest behind the edge and
// closest to the point no player will request it.
func TestAudioFeedDropsOldest(t *testing.T) {
	t.Parallel()

	// A budget of exactly four frames, so the arithmetic is exact.
	const frameSize = 1024
	const capacity = 4
	h, feed := newTestFeedConsumer(frameSize * capacity)

	const written = capacity + 3
	for seq := range uint64(written) {
		writeFrame(t, h, frameSize, seq)
	}

	seqs, totalBytes := drainFeed(feed)

	require.Len(t, seqs, capacity, "the queue must hold exactly the budget")
	assert.Equal(t, int64(frameSize*capacity), totalBytes)
	assert.Equal(t, []uint64{3, 4, 5, 6}, seqs,
		"the newest frames must survive and the oldest must be evicted")
}

// TestAudioFeedAdmitsOversizedChunk covers the makeRoom early exit. A chunk
// larger than the entire budget can never be made to fit, and refusing it would
// silence the stream permanently rather than briefly, so it is admitted and the
// budget is exceeded by exactly that one chunk.
func TestAudioFeedAdmitsOversizedChunk(t *testing.T) {
	t.Parallel()

	const budget = 4096
	h, feed := newTestFeedConsumer(budget)

	writeFrame(t, h, 1024, 0)
	writeFrame(t, h, budget*2, 1)

	seqs, totalBytes := drainFeed(feed)

	assert.Equal(t, []uint64{1}, seqs,
		"the oversized chunk must be admitted and the queue emptied to take it")
	assert.Equal(t, int64(budget*2), totalBytes)
}

// TestAudioFeedSlotExhaustionDropsOldest covers Write's slot-exhaustion arm: the
// path taken when the channel's slots fill before the byte budget bites (an
// unbounded feed, or any budget large enough that more than defaultReadBufferSize
// chunks fit under it). makeRoom does nothing there, so the drop-oldest and the
// release accounting are Write's own, and this is the only test that reaches
// them. It exercises the audioFeed queue primitive directly; it does not drive
// any encoder.
func TestAudioFeedSlotExhaustionDropsOldest(t *testing.T) {
	t.Parallel()

	// Disabled budget (maxBytes 0) so the slot count, not the byte budget, is what
	// bounds the queue. Small frames keep the intent legible.
	h, feed := newTestFeedConsumer(0)

	const overflow = 3
	const written = defaultReadBufferSize + overflow
	for seq := range uint64(written) {
		writeFrame(t, h, soundCardFrameBytes, seq)
	}

	seqs, totalBytes := drainFeed(feed)

	require.Len(t, seqs, defaultReadBufferSize,
		"the channel holds exactly its slot count once it overflows")
	assert.Equal(t, int64(defaultReadBufferSize)*soundCardFrameBytes, totalBytes)
	assert.Equal(t, uint64(overflow), seqs[0],
		"the oldest `overflow` frames must be evicted, leaving the newest slot-count worth")
	assert.Equal(t, uint64(written-1), seqs[len(seqs)-1],
		"the most recent frame must survive")
	assert.Equal(t, int64(0), feed.queued.Load(),
		"release accounting through the slot-exhaustion arm must net to zero after a drain")
}

// TestAudioFeedWriteRejectsAfterClose keeps the closed-consumer contract intact
// across the queue change: a closed consumer must not keep queueing PCM.
func TestAudioFeedWriteRejectsAfterClose(t *testing.T) {
	t.Parallel()

	h, feed := newTestFeedConsumer(nativeHLSFeedQueueBytes)
	require.NoError(t, h.Close())

	err := h.Write(audiocore.AudioFrame{
		SourceID:   "src",
		Data:       make([]byte, rtspFrameBytes),
		SampleRate: nativeHLSSampleRate,
		BitDepth:   16,
		Channels:   hlsConsumerChannels,
	})

	require.ErrorIs(t, err, audiocore.ErrConsumerClosed)
	assert.Empty(t, feed.ch, "a closed consumer must not queue anything")
	assert.Equal(t, int64(0), feed.queued.Load())
}

// requireFeedClosed fails unless feed.ch is closed and drained. It never blocks:
// an open empty channel falls through to the default arm.
func requireFeedClosed(t *testing.T, feed *audioFeed, msg string) {
	t.Helper()
	select {
	case _, ok := <-feed.ch:
		require.False(t, ok, msg)
	default:
		require.FailNow(t, msg)
	}
}

// TestAudioFeedCloseClosesChannel verifies that Close closes feed.ch after any
// buffered chunks drain, and is idempotent on repeated calls.
func TestAudioFeedCloseClosesChannel(t *testing.T) {
	t.Parallel()

	t.Run("drains buffered chunks then observes closed channel", func(t *testing.T) {
		t.Parallel()

		h, feed := newTestFeedConsumer(nativeHLSFeedQueueBytes)
		writeFrame(t, h, soundCardFrameBytes, 1)
		writeFrame(t, h, soundCardFrameBytes, 2)

		require.NoError(t, h.Close())

		seqs, totalBytes := drainFeed(feed)
		assert.Equal(t, []uint64{1, 2}, seqs, "buffered chunks must remain drainable after Close")
		assert.Equal(t, int64(2*soundCardFrameBytes), totalBytes)
		assert.Equal(t, int64(0), feed.queued.Load())
		requireFeedClosed(t, feed, "feed.ch must be closed after Close and draining")
	})

	t.Run("idempotent on repeated calls", func(t *testing.T) {
		t.Parallel()

		h, feed := newTestFeedConsumer(nativeHLSFeedQueueBytes)
		assert.NotPanics(t, func() {
			assert.NoError(t, h.Close())
			assert.NoError(t, h.Close())
		})
		requireFeedClosed(t, feed, "feed.ch must be closed after repeated Close calls")
	})
}

// TestAudioFeedEvictOldestOnClosedChannel verifies that evictOldest reports
// nothing to evict on a closed empty channel, which is what lets makeRoom stop
// instead of looping on zero-value receives.
func TestAudioFeedEvictOldestOnClosedChannel(t *testing.T) {
	t.Parallel()

	ch := make(chan audioChunk, 4)
	close(ch)
	feed := &audioFeed{ch: ch, maxBytes: 1024}
	feed.queued.Store(2048) // advisory count above the budget, so makeRoom would keep evicting

	// require, not assert: continuing into makeRoom after a failure would spin.
	require.False(t, feed.evictOldest(), "evictOldest must return false on a closed empty channel")
	assert.Equal(t, int64(2048), feed.queued.Load())
	assert.Equal(t, 0, feed.makeRoom(512), "makeRoom must exit cleanly when the channel is closed and empty")
}

// TestAudioFeedWriteConcurrentWithClose verifies that a Write racing Close
// returns normally instead of sending on the closed feed channel, whatever the
// caller's ordering. Writers keep running while Close lands, so a Write that
// has passed the closed check when Close closes the channel would panic. The
// window is narrow, so the race is repeated over fresh consumers.
func TestAudioFeedWriteConcurrentWithClose(t *testing.T) {
	t.Parallel()

	const (
		rounds  = 300
		writers = 4
	)
	frame := audiocore.AudioFrame{Data: make([]byte, soundCardFrameBytes)}

	for range rounds {
		h, feed := newTestFeedConsumer(nativeHLSFeedQueueBytes)

		var started, finished sync.WaitGroup
		started.Add(writers)
		finished.Add(writers)
		for range writers {
			go func() {
				defer finished.Done()
				started.Done()
				for {
					if err := h.Write(frame); err != nil {
						assert.ErrorIs(t, err, audiocore.ErrConsumerClosed)
						return
					}
				}
			}()
		}
		started.Wait()

		require.NoError(t, h.Close())
		finished.Wait()
		drainFeed(feed)
		requireFeedClosed(t, feed, "feed.ch must be closed once the writers have stopped")
	}
}
