package hlsmux

import (
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	m4a "github.com/tphakala/go-m4a"
)

const (
	testRate     = 48000
	testChannels = 1
	testBitrate  = 128
	aacFrame     = 1024
)

// testEpoch is a fixed wall-clock origin so PDT assertions are exact.
var testEpoch = time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)

// newTestStream builds a stream over a fixed-frame fake codec.
func newTestStream(t *testing.T, opts *fakeCodecOptions) *Stream {
	t.Helper()
	s, err := New(&Config{
		Codec:       newFakeCodec(opts),
		SampleRate:  testRate,
		Channels:    testChannels,
		BitrateKbps: testBitrate,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// writeSamples feeds n samples timed from the stream's own sample clock, which
// is what a well-behaved source does.
func writeSamples(t *testing.T, s *Stream, n int, at time.Time) {
	t.Helper()
	require.NoError(t, s.Write(silence(n, testChannels), at))
}

// sampleTime returns the wall-clock time of sample n after testEpoch.
func sampleTime(n int) time.Time {
	return testEpoch.Add(time.Duration(n) * time.Second / testRate)
}

func TestNewValidatesConfig(t *testing.T) {
	t.Parallel()

	valid := Config{
		Codec:       newFakeCodec(&fakeCodecOptions{}),
		SampleRate:  testRate,
		Channels:    testChannels,
		BitrateKbps: testBitrate,
	}

	tests := []struct {
		name   string
		mutate func(*Config)
		errMsg string
	}{
		{"zero codec", func(c *Config) { c.Codec = Codec{} }, "codec is not set"},
		{"zero sample rate", func(c *Config) { c.SampleRate = 0 }, "sample rate must be positive"},
		{"negative sample rate", func(c *Config) { c.SampleRate = -1 }, "sample rate must be positive"},
		{"three channels", func(c *Config) { c.Channels = 3 }, "channel count must be 1 or 2"},
		{"zero channels", func(c *Config) { c.Channels = 0 }, "channel count must be 1 or 2"},
		{"negative bitrate", func(c *Config) { c.BitrateKbps = -1 }, "bitrate must not be negative"},
		{"negative segment duration", func(c *Config) { c.SegmentDuration = -time.Second }, "segment duration must not be negative"},
		{"negative window", func(c *Config) { c.WindowSize = -1 }, "window size must not be negative"},
		{"negative stall gap", func(c *Config) { c.MaxStallGap = -time.Second }, "max stall gap must not be negative"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := valid
			tt.mutate(&cfg)
			s, err := New(&cfg)
			require.Error(t, err)
			assert.Nil(t, s)
			assert.Contains(t, err.Error(), tt.errMsg)
		})
	}
}

func TestNewClosesEncoderWhenContainerSetupFails(t *testing.T) {
	t.Parallel()

	var enc *fakeEncoder
	codec := newFakeCodec(&fakeCodecOptions{captured: &enc})
	// Fail the container configuration, which happens after the encoder has
	// already been constructed and so is the path that would leak it.
	codec.writerConfig = func(_ EncoderConfig, _ FrameEncoder) (m4a.WriterConfig, error) {
		return m4a.WriterConfig{}, errFakeEncoder
	}

	_, err := New(&Config{Codec: codec, SampleRate: testRate, Channels: testChannels, BitrateKbps: testBitrate})
	require.Error(t, err)
	require.NotNil(t, enc)
	assert.True(t, enc.closed, "encoder must be closed when the stream cannot be built")
}

func TestSegmentsCutOnAccessUnitBoundaries(t *testing.T) {
	t.Parallel()

	s := newTestStream(t, &fakeCodecOptions{frameSizes: []int{aacFrame}, declaredFrameSamples: aacFrame})

	// Six seconds of audio at the default two-second target.
	const total = testRate * 6
	writeSamples(t, s, total, testEpoch)

	segs := s.segments.window()
	require.NotEmpty(t, segs)

	target := testRate * 2
	for i := range segs {
		// A segment is cut on the first access unit that reaches the target,
		// so it is never shorter than the target and never longer than the
		// target plus one access unit.
		assert.GreaterOrEqual(t, segs[i].Samples, target,
			"segment %d is short of the target", segs[i].Seq)
		assert.Less(t, segs[i].Samples, target+aacFrame,
			"segment %d overshot by more than one access unit", segs[i].Seq)
		assert.Zero(t, segs[i].Samples%aacFrame,
			"segment %d does not land on an access-unit boundary", segs[i].Seq)
	}
}

func TestDurationsAccumulateWithoutDrift(t *testing.T) {
	t.Parallel()

	// A window large enough to retain every segment produced below, so the
	// sum can be checked against the total sample count.
	s := newTestStream(t, &fakeCodecOptions{
		frameSizes:           []int{aacFrame},
		declaredFrameSamples: aacFrame,
	})
	s.segments = newRing(1000)

	const total = testRate * 600 // ten minutes
	writeSamples(t, s, total, testEpoch)

	var sumSamples int
	var sumDuration time.Duration
	for _, seg := range s.segments.window() {
		sumSamples += seg.Samples
		sumDuration += seg.Duration
	}

	// The exact duration of the samples actually segmented. Any per-segment
	// truncation would make the sum fall short of this.
	want := time.Duration(sumSamples) * time.Second / testRate
	assert.Equal(t, want, sumDuration,
		"segment durations must sum to the exact duration of their samples")
}

func TestProgramDateTimeTracksTheSampleTimeline(t *testing.T) {
	t.Parallel()

	s := newTestStream(t, &fakeCodecOptions{frameSizes: []int{aacFrame}, declaredFrameSamples: aacFrame})
	s.segments = newRing(1000)

	writeSamples(t, s, testRate*10, testEpoch)

	segs := s.segments.window()
	require.Greater(t, len(segs), 1)

	assert.Equal(t, testEpoch, segs[0].PDT, "the first segment starts at the epoch")

	var elapsed time.Duration
	for _, seg := range segs {
		assert.Equal(t, testEpoch.Add(elapsed), seg.PDT,
			"segment %d PDT must equal the epoch plus every preceding duration", seg.Seq)
		elapsed += seg.Duration
	}
}

func TestSourceStallDeclaresDiscontinuityAndReanchorsPDT(t *testing.T) {
	t.Parallel()

	s := newTestStream(t, &fakeCodecOptions{frameSizes: []int{aacFrame}, declaredFrameSamples: aacFrame})
	s.segments = newRing(1000)

	// Four seconds of well-behaved audio.
	const before = testRate * 4
	writeSamples(t, s, before, testEpoch)

	// The source drops out for five seconds, then resumes. Its frames carry
	// real capture times, so the resume timestamp is five seconds beyond
	// where the sample clock expected it.
	resume := sampleTime(before).Add(5 * time.Second)
	writeSamples(t, s, testRate*4, resume)

	segs := s.segments.window()
	var breakIdx = -1
	for i := range segs {
		if segs[i].Discontinuity {
			breakIdx = i
			break
		}
	}
	require.NotEqual(t, -1, breakIdx, "a stall longer than MaxStallGap must mark a discontinuity")

	assert.Equal(t, resume, segs[breakIdx].PDT,
		"the segment after a stall must be re-anchored to the resume time, not to the sample clock")
	assert.Equal(t, uint64(0), segs[breakIdx].DiscontinuitySeq,
		"the first discontinuity is preceded by none")
	if breakIdx+1 < len(segs) {
		assert.Equal(t, uint64(1), segs[breakIdx+1].DiscontinuitySeq,
			"segments after the break count it")
	}

	assert.Contains(t, s.Playlist(), "#EXT-X-DISCONTINUITY\n")
}

func TestJitterBelowThresholdIsAbsorbed(t *testing.T) {
	t.Parallel()

	s := newTestStream(t, &fakeCodecOptions{frameSizes: []int{aacFrame}, declaredFrameSamples: aacFrame})
	s.segments = newRing(1000)

	// Feed in chunks whose timestamps wobble by a few milliseconds, well
	// inside the default one-second threshold. None of it should break the
	// timeline.
	const chunk = testRate / 10
	wobble := []time.Duration{0, 3 * time.Millisecond, -2 * time.Millisecond, 5 * time.Millisecond}
	for i := range 100 {
		at := sampleTime(i * chunk).Add(wobble[i%len(wobble)])
		writeSamples(t, s, chunk, at)
	}

	for _, seg := range s.segments.window() {
		assert.False(t, seg.Discontinuity,
			"ordinary jitter must not break the timeline (segment %d)", seg.Seq)
	}
	assert.NotContains(t, s.Playlist(), "#EXT-X-DISCONTINUITY")
}

func TestRingEvictsOldestAndAdvancesMediaSequence(t *testing.T) {
	t.Parallel()

	s := newTestStream(t, &fakeCodecOptions{frameSizes: []int{aacFrame}, declaredFrameSamples: aacFrame})

	// Twenty seconds at two seconds per segment produces far more than the
	// default six-segment window.
	writeSamples(t, s, testRate*20, testEpoch)

	segs := s.segments.window()
	require.Len(t, segs, DefaultWindowSize)

	oldest := segs[0].Seq
	assert.Positive(t, oldest, "early segments must have been evicted")
	assert.Equal(t, oldest, s.segments.mediaSequence())

	_, ok := s.Segment(oldest - 1)
	assert.False(t, ok, "an evicted segment must not be servable")

	_, ok = s.Segment(oldest)
	assert.True(t, ok, "the oldest retained segment must be servable")

	assert.Contains(t, s.Playlist(), "#EXT-X-MEDIA-SEQUENCE:"+strconv.FormatUint(oldest, 10))
}

func TestPartialAccessUnitIsBufferedNotEmitted(t *testing.T) {
	t.Parallel()

	s := newTestStream(t, &fakeCodecOptions{frameSizes: []int{aacFrame}, declaredFrameSamples: aacFrame})

	// Less than one access unit of audio.
	writeSamples(t, s, aacFrame-1, testEpoch)

	assert.True(t, s.segments.empty(), "a partial access unit must not produce a segment")
	assert.False(t, s.Ready())
	assert.Zero(t, s.pendingSamples)
}

func TestWriteRejectsMisalignedPCM(t *testing.T) {
	t.Parallel()

	s := newTestStream(t, &fakeCodecOptions{})

	err := s.Write(make([]byte, 3), testEpoch) // 3 bytes is 1.5 mono samples
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a multiple")
}

func TestWriteAfterCloseFails(t *testing.T) {
	t.Parallel()

	s := newTestStream(t, &fakeCodecOptions{})
	require.NoError(t, s.Close())

	err := s.Write(silence(aacFrame, testChannels), testEpoch)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "closed stream")
}

func TestCloseFlushesTailAndEndsPlaylist(t *testing.T) {
	t.Parallel()

	var enc *fakeEncoder
	s := newTestStream(t, &fakeCodecOptions{
		frameSizes:           []int{aacFrame},
		declaredFrameSamples: aacFrame,
		tailSamples:          aacFrame,
		captured:             &enc,
	})

	// Half a segment's worth, so nothing has been cut yet.
	writeSamples(t, s, testRate, testEpoch)
	require.True(t, s.segments.empty())

	require.NoError(t, s.Close())

	assert.False(t, s.segments.empty(), "Close must publish the buffered audio as a final segment")
	require.NotNil(t, enc)
	assert.True(t, enc.closed, "Close must release the encoder")

	playlist := s.Playlist()
	assert.Contains(t, playlist, "#EXT-X-ENDLIST", "a closed stream must stop clients polling")
}

func TestCloseIsIdempotent(t *testing.T) {
	t.Parallel()

	s := newTestStream(t, &fakeCodecOptions{})
	require.NoError(t, s.Close())
	require.NoError(t, s.Close(), "closing twice must not error")
}

func TestEncoderFailurePropagates(t *testing.T) {
	t.Parallel()

	s := newTestStream(t, &fakeCodecOptions{frameSizes: []int{aacFrame}, failAfter: 2})

	err := s.Write(silence(testRate, testChannels), testEpoch)
	require.Error(t, err)
	assert.ErrorIs(t, err, errFakeEncoder)
}

// TestCodecNeutrality is the structural check that nothing in the muxer
// assumes AAC-LC's shape. The codec here has variable frame sizes that are not
// 1024 and no priming delay at all, which is exactly what a hardcoded frame
// size or an assumed non-zero encoder delay would trip over.
func TestCodecNeutrality(t *testing.T) {
	t.Parallel()

	s := newTestStream(t, &fakeCodecOptions{
		name:       "variable",
		frameSizes: []int{4096, 2048, 512, 4096},
		delay:      0,
		// Declared as variable: the muxer must record each unit's own
		// duration rather than a fragment-wide default.
		declaredFrameSamples: 0,
	})
	s.segments = newRing(1000)

	writeSamples(t, s, testRate*10, testEpoch)

	segs := s.segments.window()
	require.NotEmpty(t, segs)

	target := testRate * 2
	var sumSamples int
	for _, seg := range segs {
		assert.GreaterOrEqual(t, seg.Samples, target)
		// The largest frame in the cycle bounds the overshoot.
		assert.Less(t, seg.Samples, target+4096)
		sumSamples += seg.Samples
	}

	// The timeline must still be exact with irregular frame sizes.
	var sumDuration time.Duration
	for _, seg := range segs {
		sumDuration += seg.Duration
	}
	assert.Equal(t, time.Duration(sumSamples)*time.Second/testRate, sumDuration)
}

func TestInitSegmentIsStableAndNonEmpty(t *testing.T) {
	t.Parallel()

	s := newTestStream(t, &fakeCodecOptions{})

	init := s.InitSegment()
	require.NotEmpty(t, init, "the init segment must carry the moov the player needs")
	assert.Same(t, &init[0], &s.InitSegment()[0], "the init segment is fixed for the life of the stream")

	// It must be a real ISO-BMFF init segment: ftyp first, then moov.
	assert.Contains(t, string(init[:16]), "ftyp")
	assert.Contains(t, string(init), "moov")
}

func TestAccessUnitsAreCopiedNotAliased(t *testing.T) {
	t.Parallel()

	// The fake encoder reuses one scratch buffer and stamps each access unit
	// with a distinct byte. If the muxer retained the borrowed slice instead
	// of relying on go-m4a to copy, every unit in the segment would carry the
	// last unit's stamp and the distinct values would be missing.
	s := newTestStream(t, &fakeCodecOptions{frameSizes: []int{aacFrame}, declaredFrameSamples: aacFrame})

	writeSamples(t, s, testRate*2+aacFrame, testEpoch)

	segs := s.segments.window()
	require.NotEmpty(t, segs)

	data := segs[0].Data
	distinct := map[byte]struct{}{}
	for _, b := range data {
		distinct[b] = struct{}{}
	}
	assert.Greater(t, len(distinct), 2,
		"segment payload must contain the distinct per-unit stamps, not one repeated value")
}

func TestConcurrentWriteAndServe(t *testing.T) {
	t.Parallel()

	s := newTestStream(t, &fakeCodecOptions{frameSizes: []int{aacFrame}, declaredFrameSamples: aacFrame})

	var wg sync.WaitGroup
	const chunks = 200
	const chunk = testRate / 10

	wg.Go(func() {
		for i := range chunks {
			if err := s.Write(silence(chunk, testChannels), sampleTime(i*chunk)); err != nil {
				t.Errorf("write: %v", err)
				return
			}
		}
	})

	for range 4 {
		wg.Go(func() {
			for range chunks {
				_ = s.Playlist()
				_ = s.InitSegment()
				_, _ = s.Segment(0)
				_ = s.Ready()
			}
		})
	}

	wg.Wait()
}

func TestPlaylistBeforeAnySegment(t *testing.T) {
	t.Parallel()

	s := newTestStream(t, &fakeCodecOptions{})
	playlist := s.Playlist()

	// A player polling during the first segment must still learn the init
	// segment, and must not be told the stream has ended.
	assert.Contains(t, playlist, "#EXTM3U")
	assert.Contains(t, playlist, "#EXT-X-VERSION:7")
	assert.Contains(t, playlist, `#EXT-X-MAP:URI="init.mp4"`)
	assert.Contains(t, playlist, "#EXT-X-TARGETDURATION:")
	assert.NotContains(t, playlist, "#EXTINF")
	assert.NotContains(t, playlist, "#EXT-X-ENDLIST")

	// A zero target duration is rejected outright by players.
	assert.NotContains(t, playlist, "#EXT-X-TARGETDURATION:0")
}

func TestPlaylistHasNoCodecsAttribute(t *testing.T) {
	t.Parallel()

	// CODECS belongs to EXT-X-STREAM-INF in a master playlist. RFC 8216 gives
	// it no place in a media playlist, and emitting it anyway is the kind of
	// thing strict players reject.
	s := newTestStream(t, &fakeCodecOptions{frameSizes: []int{aacFrame}})
	writeSamples(t, s, testRate*4, testEpoch)

	playlist := s.Playlist()
	assert.NotContains(t, playlist, "CODECS")
	assert.NotContains(t, playlist, "EXT-X-STREAM-INF")
}
