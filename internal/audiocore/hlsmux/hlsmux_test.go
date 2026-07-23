package hlsmux

import (
	"bytes"
	"errors"
	"strconv"
	"strings"
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

	// testWideWindow is a playlist window nothing scrolls out of, for tests that
	// assert over every segment a run produced.
	testWideWindow = 1000
)

// testEpoch is a fixed wall-clock origin so PDT assertions are exact.
var testEpoch = time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)

// newTestStream builds a stream over a fixed-frame fake codec, with the default
// playlist window.
func newTestStream(t *testing.T, opts *fakeCodecOptions) *Stream {
	t.Helper()
	return newTestStreamWithWindow(t, opts, 0)
}

// newTestStreamWithWindow builds a stream whose playlist window holds
// windowSize segments; zero selects DefaultWindowSize.
//
// It exists so that a test needing a window nothing scrolls out of configures
// one, rather than replacing s.segments after New. Replacing it works today but
// swaps the window a snapshot was already published from without republishing,
// which is exactly the unpublished-mutation shape
// TestPlaylistSnapshotNeverGoesStale exists to forbid.
func newTestStreamWithWindow(t *testing.T, opts *fakeCodecOptions, windowSize int) *Stream {
	t.Helper()
	s, err := New(&Config{
		Codec:       newFakeCodec(opts),
		SampleRate:  testRate,
		Channels:    testChannels,
		BitrateKbps: testBitrate,
		WindowSize:  windowSize,
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
		{"negative segment duration", func(c *Config) { c.SegmentDuration = -time.Second }, "below the minimum"},
		{"segment duration below the floor", func(c *Config) { c.SegmentDuration = time.Millisecond }, "below the minimum"},
		{"segment duration above the ceiling", func(c *Config) { c.SegmentDuration = time.Hour }, "exceeds the maximum"},
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

	s := newTestStream(t, &fakeCodecOptions{frameSizes: []int{aacFrame}})

	// Six seconds of audio at the default two-second target.
	const total = testRate * 6
	writeSamples(t, s, total, testEpoch)

	segs := s.segments.retained()
	require.NotEmpty(t, segs)

	target := s.targetSamples
	for i := range segs {
		// A segment is cut before the next access unit would take it past the
		// target, so it never exceeds the target and is never more than one
		// access unit short of it. Never exceeding matters: the playlist
		// advertises the target as EXT-X-TARGETDURATION, and Apple's
		// validator treats that as an absolute ceiling.
		assert.LessOrEqual(t, segs[i].Samples, target,
			"segment %d exceeds the advertised target duration", segs[i].Seq)
		assert.Greater(t, segs[i].Samples, target-aacFrame,
			"segment %d is more than one access unit short", segs[i].Seq)
		assert.Zero(t, segs[i].Samples%aacFrame,
			"segment %d does not land on an access-unit boundary", segs[i].Seq)
	}
}

func TestDurationsAccumulateWithoutDrift(t *testing.T) {
	t.Parallel()

	// A window large enough to retain every segment produced below, so the
	// sum can be checked against the total sample count.
	s := newTestStreamWithWindow(t, &fakeCodecOptions{
		frameSizes: []int{aacFrame},
	}, testWideWindow)

	const total = testRate * 600 // ten minutes
	writeSamples(t, s, total, testEpoch)

	var sumSamples int
	var sumDuration time.Duration
	for _, seg := range s.segments.retained() {
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

	s := newTestStreamWithWindow(t, &fakeCodecOptions{frameSizes: []int{aacFrame}}, testWideWindow)

	writeSamples(t, s, testRate*10, testEpoch)

	segs := s.segments.retained()
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

	s := newTestStreamWithWindow(t, &fakeCodecOptions{frameSizes: []int{aacFrame}}, testWideWindow)

	// Four seconds of well-behaved audio.
	const before = testRate * 4
	writeSamples(t, s, before, testEpoch)

	// The source drops out for five seconds, then resumes. Its frames carry
	// real capture times, so the resume timestamp is five seconds beyond
	// where the sample clock expected it.
	resume := sampleTime(before).Add(5 * time.Second)
	writeSamples(t, s, testRate*4, resume)

	segs := s.segments.retained()
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

	// RFC 8216 section 6.2.1: a segment preceded by EXT-X-DISCONTINUITY has
	// the predecessor's discontinuity sequence number plus one. Numbering it
	// with the predecessor's value would make the number a client computes for
	// this segment change once the earlier ones scroll out of the window.
	require.Positive(t, breakIdx, "the break must not be the first retained segment for this check")
	assert.Equal(t, uint64(0), segs[breakIdx-1].DiscontinuitySeq,
		"segments before the break carry zero")
	assert.Equal(t, uint64(1), segs[breakIdx].DiscontinuitySeq,
		"the segment carrying the break counts it")
	require.Less(t, breakIdx+1, len(segs), "expected a segment after the break")
	assert.Equal(t, uint64(1), segs[breakIdx+1].DiscontinuitySeq,
		"later segments keep the same number until the next break")

	assert.Contains(t, s.Playlist(), "#EXT-X-DISCONTINUITY\n")
}

func TestJitterBelowThresholdIsAbsorbed(t *testing.T) {
	t.Parallel()

	s := newTestStreamWithWindow(t, &fakeCodecOptions{frameSizes: []int{aacFrame}}, testWideWindow)

	// Feed in chunks whose timestamps wobble by a few milliseconds, well
	// inside the default one-second threshold. None of it should break the
	// timeline.
	const chunk = testRate / 10
	wobble := []time.Duration{0, 3 * time.Millisecond, -2 * time.Millisecond, 5 * time.Millisecond}
	for i := range 100 {
		at := sampleTime(i * chunk).Add(wobble[i%len(wobble)])
		writeSamples(t, s, chunk, at)
	}

	for _, seg := range s.segments.retained() {
		assert.False(t, seg.Discontinuity,
			"ordinary jitter must not break the timeline (segment %d)", seg.Seq)
	}
	assert.NotContains(t, s.Playlist(), "#EXT-X-DISCONTINUITY")
}

func TestSegmentWindowEvictsOldestAndAdvancesMediaSequence(t *testing.T) {
	t.Parallel()

	s := newTestStream(t, &fakeCodecOptions{frameSizes: []int{aacFrame}})

	// Twenty seconds at two seconds per segment produces far more than the
	// default six-segment window.
	writeSamples(t, s, testRate*20, testEpoch)

	segs := s.segments.retained()
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

	s := newTestStream(t, &fakeCodecOptions{frameSizes: []int{aacFrame}})

	// Less than one access unit of audio.
	writeSamples(t, s, aacFrame-1, testEpoch)

	assert.Empty(t, s.segments.retained(), "a partial access unit must not produce a segment")
	assert.False(t, s.Ready(1))
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
		frameSizes:  []int{aacFrame},
		tailSamples: aacFrame,
		captured:    &enc,
	})

	// Half a segment's worth, so nothing has been cut yet.
	writeSamples(t, s, testRate, testEpoch)
	require.Empty(t, s.segments.retained())

	require.NoError(t, s.Close())

	assert.NotEmpty(t, s.segments.retained(), "Close must publish the buffered audio as a final segment")
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

	s := newTestStreamWithWindow(t, &fakeCodecOptions{
		name:       "variable",
		frameSizes: []int{4096, 2048, 512, 4096},
		delay:      0,
		// Declared as variable: the muxer must record each unit's own
		// duration rather than a fragment-wide default.
	}, testWideWindow)

	writeSamples(t, s, testRate*10, testEpoch)

	segs := s.segments.retained()
	require.NotEmpty(t, segs)

	const largestFrame = 4096
	target := s.targetSamples
	var sumSamples int
	for _, seg := range segs {
		// Same invariant as the fixed-frame case, and it must hold with
		// irregular frame sizes too: never over the advertised target, and
		// never more than the largest frame short of it.
		assert.LessOrEqual(t, seg.Samples, target)
		assert.Greater(t, seg.Samples, target-largestFrame)
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

	// The fake encoder reuses one scratch buffer and stamps every sample of
	// each access unit with a distinct byte, recording the stamps it used. If
	// the muxer retained the borrowed slice instead of relying on go-m4a to
	// copy it, every unit in the segment would carry the LAST unit's stamp.
	//
	// Counting distinct bytes would not detect that: the styp/moof/trun
	// headers alone contribute dozens of distinct values, so any threshold low
	// enough to state is far below the container's own noise. The check has to
	// be against what the encoder actually emitted, per unit.
	var enc *fakeEncoder
	s := newTestStream(t, &fakeCodecOptions{frameSizes: []int{aacFrame}, captured: &enc})

	writeSamples(t, s, testRate*2+aacFrame, testEpoch)

	segs := s.segments.retained()
	require.NotEmpty(t, segs)
	require.NotNil(t, enc)

	seg := segs[0]
	unitsInSegment := seg.Samples / aacFrame
	require.GreaterOrEqual(t, len(enc.stamps), unitsInSegment)

	// Each access unit occupies aacFrame*fakeAUBytesPerSample bytes of mdat,
	// laid end to end. Find the payload by locating the first unit's run, then
	// verify every subsequent unit carries its OWN stamp at its own offset.
	unitBytes := aacFrame * fakeAUBytesPerSample
	first := bytes.Repeat([]byte{enc.stamps[0]}, unitBytes)
	start := bytes.Index(seg.Data, first)
	require.GreaterOrEqual(t, start, 0, "first access unit not found intact in the segment payload")

	for i := range unitsInSegment {
		want := bytes.Repeat([]byte{enc.stamps[i]}, unitBytes)
		got := seg.Data[start+i*unitBytes : start+(i+1)*unitBytes]
		assert.Equal(t, want, got,
			"access unit %d carries the wrong bytes; the muxer aliased the encoder's scratch buffer", i)
	}
}

// TestConcurrentWriteAndServe runs the readers against a writer under -race,
// which is the only mechanism that can catch a FUTURE write into a published
// window; the unit tests can only pin the implementation as it stands.
//
// Two details make it able to detect that, and both were established by
// reverting push to its old in-place shift and confirming this test then fails.
// The readers run until the writer is done rather than a fixed count, so they
// cannot retire early and leave the interesting cuts unobserved; and the window
// is small enough that every cut evicts, since the in-place shift only wrote
// into the published array on eviction. With a fixed reader count and the
// default window, the same revert survived twenty runs.
func TestConcurrentWriteAndServe(t *testing.T) {
	t.Parallel()

	// A window that evicts on every cut after the second.
	s := newTestStreamWithWindow(t, &fakeCodecOptions{frameSizes: []int{aacFrame}}, 2)

	var wg sync.WaitGroup
	const chunks = 200
	const chunk = testRate / 10
	done := make(chan struct{})

	wg.Go(func() {
		defer close(done)
		for i := range chunks {
			if err := s.Write(silence(chunk, testChannels), sampleTime(i*chunk)); err != nil {
				t.Errorf("write: %v", err)
				return
			}
		}
	})

	for range 4 {
		wg.Go(func() {
			for {
				select {
				case <-done:
					return
				default:
				}
				// Read the segment payloads, not just the counts: Playlist and
				// Ready never touch a window element, so Segment is the only
				// call here that a mutated header could be observed through.
				stats := s.Stats()
				for seq := range stats.Segments {
					if seg, ok := s.Segment(seq); ok {
						_ = len(seg.Data)
					}
				}
				_ = s.Playlist()
				_ = s.InitSegment()
				_ = s.Ready(1)
			}
		})
	}

	wg.Wait()
}

// TestPlaylistSnapshotNeverGoesStale pins the discipline the lock-free read side
// costs. The playlist is now rendered when a segment is cut rather than when it
// is asked for, so a future path that changes the window without calling
// publishView would serve a playlist that is missing that segment, for as long
// as the stream runs, while every other playlist assertion in this file kept
// passing: they all read the same stale snapshot and would agree with each
// other about it.
func TestPlaylistSnapshotNeverGoesStale(t *testing.T) {
	t.Parallel()

	s := newTestStream(t, &fakeCodecOptions{
		frameSizes:  []int{aacFrame},
		tailSamples: aacFrame,
	})

	// Quarter-second chunks, so cuts land inside the run rather than only at
	// its edges, and enough of them to scroll the window well past its
	// capacity. Scrolling matters: until the window fills, push never takes its
	// eviction branch and Retained is just nextSeq, so the assertions below
	// would agree for the wrong reason.
	const chunk = testRate / 4
	const chunks = 80
	for i := range chunks {
		writeSamples(t, s, chunk, sampleTime(i*chunk))
		requireSnapshotMatchesWindow(t, s, i)
	}
	require.Greater(t, s.nextSeq, uint64(s.segments.capacity),
		"the window must have scrolled, or eviction went untested")

	// A stall re-anchors the timeline and cuts through a second path into
	// cutSegment, which has its own publish to get wrong.
	stall := sampleTime(chunks * chunk).Add(10 * time.Second)
	require.NoError(t, s.Write(silence(chunk, testChannels), stall))
	requireSnapshotMatchesWindow(t, s, chunks)

	require.NoError(t, s.Close())
	requireSnapshotMatchesWindow(t, s, chunks+1)
	assert.Contains(t, s.Playlist(), "#EXT-X-ENDLIST", "a closed stream must stop clients polling")
}

// requireSnapshotMatchesWindow asserts that every field a reader can see equals
// what the stream's own state says it should be.
//
// It checks all five, not just the playlist, because the three counters are
// exactly the fields cutSegment settles AFTER pushing the segment. Asserting
// only the playlist and Retained would pass against a publish hoisted above
// those updates, which is a way to get the snapshot wrong that no amount of
// playlist comparison detects.
func requireSnapshotMatchesWindow(t *testing.T, s *Stream, step int) {
	t.Helper()

	stats := s.Stats()
	require.Equal(t, renderPlaylist(s.segments, s.targetDuration, s.closed), s.Playlist(),
		"the snapshot must equal a fresh render of the window at step %d", step)
	require.Equal(t, len(s.segments.retained()), stats.Retained, "Retained at step %d", step)
	require.Equal(t, s.nextSeq, stats.Segments, "Segments at step %d", step)
	require.Equal(t, s.discontinuities, stats.Discontinuities, "Discontinuities at step %d", step)
	require.Equal(t, s.lastSegmentPDT, stats.LastSegmentPDT, "LastSegmentPDT at step %d", step)
}

// TestCloseWithNoPendingAudioEndsPlaylist covers the one path on which Close's
// own publish is the only one: a stream with nothing buffered to cut.
//
// Without it the ENDLIST assertions elsewhere are satisfied by cutSegment's
// publish instead, since Close sets s.closed before flushing. Deleting Close's
// publish then leaves the whole suite green while a stream closed on a segment
// boundary, or with no audio at all, serves a playlist that never ends and a
// client that polls it forever.
func TestCloseWithNoPendingAudioEndsPlaylist(t *testing.T) {
	t.Parallel()

	s := newTestStream(t, &fakeCodecOptions{frameSizes: []int{aacFrame}})
	require.NoError(t, s.Close())

	assert.Contains(t, s.Playlist(), "#EXT-X-ENDLIST",
		"a stream closed with nothing buffered must still stop advertising itself as live")
}

// TestCloseDoesNotEndThePlaylistBeforeTheFinalSegment pins the reason
// publishView takes an explicit ended flag rather than reading s.closed.
//
// Close sets s.closed first, so that a racing Write is refused and a second
// Close cannot re-enter a codec that just panicked. A cut triggered by the
// flush therefore runs with s.closed already true, and rendering the tag from
// that would publish a terminated playlist that omits the segment the flush is
// producing. A client honouring EXT-X-ENDLIST stops there and never fetches it.
//
// The bad state is transient, so asserting on the final playlist proves
// nothing: by the time Close returns, both the mid-flush cut and Close's own
// publish have happened and the result is correct either way. The flush hook is
// what makes the intermediate instant observable, and it can call Playlist from
// inside the muxer's own critical section precisely because the read side no
// longer takes the lock.
func TestCloseDoesNotEndThePlaylistBeforeTheFinalSegment(t *testing.T) {
	t.Parallel()

	var s *Stream
	var duringFlush string
	opts := &fakeCodecOptions{
		frameSizes:  []int{aacFrame},
		tailSamples: aacFrame,
	}
	// Runs after the tail unit has forced its cut, and therefore after that
	// cut has published a snapshot.
	opts.flushHook = func() { duringFlush = s.Playlist() }
	s = newTestStream(t, opts)

	// Fill the segment in progress to within one access unit of the target, so
	// that the flushed tail overflows it and forces a cut from inside Close.
	writeSamples(t, s, s.targetSamples, testEpoch)
	require.Positive(t, s.pendingSamples, "the flush must have a partial segment to overflow")

	require.NoError(t, s.Close())

	require.NotEmpty(t, duringFlush, "the flush hook must have run")
	require.Contains(t, duringFlush, "#EXTINF", "the tail must have forced a cut during the flush")
	assert.NotContains(t, duringFlush, "#EXT-X-ENDLIST",
		"the playlist must not declare the stream over while the flush is still producing segments")

	// And the finished playlist is complete and terminated.
	playlist := s.Playlist()
	require.Contains(t, playlist, "#EXT-X-ENDLIST")
	assert.Equal(t, len(s.segments.retained()), strings.Count(playlist, "#EXTINF"),
		"the ended playlist must advertise every retained segment")
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

// TestStereoFrameSizing exercises the channel multiplier in frameBytes.
// Dropping it survives a mono-only suite, and the consequence is that every
// segment's sample count, duration and PDT are wrong by a factor of two.
func TestStereoFrameSizing(t *testing.T) {
	t.Parallel()

	const stereo = 2
	s, err := New(&Config{
		Codec:       newFakeCodec(&fakeCodecOptions{frameSizes: []int{aacFrame}}),
		SampleRate:  testRate,
		Channels:    stereo,
		BitrateKbps: testBitrate,
	})
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, s.Close()) })

	assert.Equal(t, stereo*bytesPerSample, s.frameBytes)

	// Six seconds of stereo audio: twice the bytes of the mono case but the
	// same sample count, so it must cut the same number of segments. Writing
	// mono-sized buffers here would halve the sample count silently, which is
	// exactly the bug this test exists to catch.
	require.NoError(t, s.Write(silence(testRate*6, stereo), testEpoch))
	require.GreaterOrEqual(t, len(s.segments.retained()), 2)

	var sum int
	for _, seg := range s.segments.retained() {
		sum += seg.Samples
	}
	assert.Greater(t, sum, testRate*3,
		"a stereo stream must account for the same sample count as mono, not half or double it")
	assert.LessOrEqual(t, sum, testRate*6)

	// A single stereo sample frame is 4 bytes, so 2 bytes is misaligned even
	// though it is a whole mono frame.
	require.Error(t, s.Write(make([]byte, 2), testEpoch))
}

// TestInputClockCarryIsExact covers the second sample clock. Every chunk here
// has a sample count whose nanosecond conversion leaves a remainder at 48 kHz,
// so naive truncation accumulates. Without the carry the projected timeline
// falls behind the real one and eventually declares a stall on a healthy
// source; a suite whose chunk sizes all divide evenly never notices.
func TestInputClockCarryIsExact(t *testing.T) {
	t.Parallel()

	s := newTestStream(t, &fakeCodecOptions{frameSizes: []int{aacFrame}})

	// 1000 samples at 48 kHz is 20833.333... microseconds: never a whole
	// number of nanoseconds.
	const chunk = 1000
	const chunks = 20000
	for i := range chunks {
		writeSamples(t, s, chunk, sampleTime(i*chunk))
	}

	// The projection must land exactly on the sample after the last one
	// written. Truncation would leave it short by hundreds of microseconds
	// over this many chunks.
	want := testEpoch.Add(time.Duration(chunk) * chunks * time.Second / testRate)
	assert.Equal(t, want, s.nextSampleTime,
		"the input clock must project the exact sample time, with no accumulated truncation")

	for _, seg := range s.segments.retained() {
		assert.False(t, seg.Discontinuity,
			"an exact sample clock must never look like a stall")
	}
}

// TestStallThresholdBoundary probes both sides of the comparison the stall
// detector actually makes, which the jitter test does not come near.
func TestStallThresholdBoundary(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		offset time.Duration
		breaks bool
	}{
		{"exactly at the threshold is absorbed", DefaultSegmentDuration / 2, false},
		{"just past the threshold breaks", DefaultSegmentDuration/2 + time.Nanosecond, true},
		{"exactly at the negative threshold is absorbed", -DefaultSegmentDuration / 2, false},
		{"just past the negative threshold breaks", -DefaultSegmentDuration/2 - time.Nanosecond, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s := newTestStreamWithWindow(t, &fakeCodecOptions{frameSizes: []int{aacFrame}}, testWideWindow)

			const first = testRate
			writeSamples(t, s, first, testEpoch)
			writeSamples(t, s, testRate*3, sampleTime(first).Add(tt.offset))

			var sawBreak bool
			for _, seg := range s.segments.retained() {
				sawBreak = sawBreak || seg.Discontinuity
			}
			assert.Equal(t, tt.breaks, sawBreak)
		})
	}
}

// TestBackwardClockJumpIsDetected covers the saturating-subtraction case. A
// large enough backwards jump makes Sub clamp to the minimum Duration, and an
// implementation that took the absolute value would negate that back to the
// minimum and read the largest possible jump as no gap at all.
func TestBackwardClockJumpIsDetected(t *testing.T) {
	t.Parallel()

	s := newTestStreamWithWindow(t, &fakeCodecOptions{frameSizes: []int{aacFrame}}, testWideWindow)

	writeSamples(t, s, testRate, testEpoch)
	// Far enough back that time.Time.Sub saturates.
	writeSamples(t, s, testRate*3, time.Time{}.Add(time.Hour))

	var sawBreak bool
	for _, seg := range s.segments.retained() {
		sawBreak = sawBreak || seg.Discontinuity
	}
	assert.True(t, sawBreak, "a saturating backwards jump must break the timeline")
}

func TestWriteRejectsZeroTimestamp(t *testing.T) {
	t.Parallel()

	s := newTestStream(t, &fakeCodecOptions{})
	err := s.Write(silence(aacFrame, testChannels), time.Time{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "zero capture timestamp")
}

// TestEncodeFailureLatchesTheStream: the timeline has already consumed the
// frame's duration when the encoder fails, so continuing would silently shift
// every later PDT. The stream must refuse instead.
func TestEncodeFailureLatchesTheStream(t *testing.T) {
	t.Parallel()

	s := newTestStream(t, &fakeCodecOptions{frameSizes: []int{aacFrame}, failAfter: 2})

	require.Error(t, s.Write(silence(testRate, testChannels), testEpoch))
	assert.True(t, s.Stats().Failed)

	err := s.Write(silence(aacFrame, testChannels), sampleTime(testRate))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed encoding")
}

func TestNewRejectsNilConfigAndBadEncoder(t *testing.T) {
	t.Parallel()

	_, err := New(nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "config is nil")

	base := func(opts *fakeCodecOptions) *Config {
		return &Config{Codec: newFakeCodec(opts), SampleRate: testRate, Channels: testChannels, BitrateKbps: testBitrate}
	}

	_, err = New(base(&fakeCodecOptions{encoderErr: errFakeEncoder}))
	require.ErrorIs(t, err, errFakeEncoder)

	_, err = New(base(&fakeCodecOptions{nilEncoder: true}))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nil encoder")

	var enc *fakeEncoder
	_, err = New(base(&fakeCodecOptions{writerErr: errFakeEncoder, captured: &enc}))
	require.ErrorIs(t, err, errFakeEncoder)
	require.NotNil(t, enc)
	assert.True(t, enc.closed, "a container failure must still release the encoder")
}

func TestCloseReportsFlushAndEncoderErrors(t *testing.T) {
	t.Parallel()

	t.Run("flush error", func(t *testing.T) {
		t.Parallel()
		s := newTestStream(t, &fakeCodecOptions{flushErr: errFakeEncoder})
		require.ErrorIs(t, s.Close(), errFakeEncoder)
	})

	t.Run("close error", func(t *testing.T) {
		t.Parallel()
		s := newTestStream(t, &fakeCodecOptions{closeErr: errFakeEncoder})
		require.ErrorIs(t, s.Close(), errFakeEncoder)
	})

	t.Run("both errors are reported", func(t *testing.T) {
		t.Parallel()
		// A close error must not be swallowed just because flush also failed:
		// a pooled or cgo-backed encoder failing to release is exactly the
		// leak this reports.
		s := newTestStream(t, &fakeCodecOptions{flushErr: errFakeEncoder, closeErr: errFakeClose})
		err := s.Close()
		require.Error(t, err)
		require.ErrorIs(t, err, errFakeEncoder)
		assert.ErrorIs(t, err, errFakeClose)
	})
}

func TestReadyCountsSegmentsAndStatsTrackHealth(t *testing.T) {
	t.Parallel()

	s := newTestStream(t, &fakeCodecOptions{frameSizes: []int{aacFrame}})

	assert.False(t, s.Ready(1))
	assert.Equal(t, Stats{}, s.Stats())

	// Slightly over one segment's worth: the cut happens on the access unit
	// that would overflow the target, so exactly one segment's worth of input
	// publishes nothing yet.
	writeSamples(t, s, testRate*3, testEpoch)
	require.True(t, s.Ready(1))
	// hls.js holds off until it has several fragments, so one segment is not
	// enough to advertise the stream as playable.
	assert.False(t, s.Ready(2), "one segment must not satisfy a two-segment readiness check")

	writeSamples(t, s, testRate*2, sampleTime(testRate*3))
	assert.True(t, s.Ready(2))

	st := s.Stats()
	assert.Equal(t, uint64(2), st.Segments)
	assert.Equal(t, 2, st.Retained)
	assert.Zero(t, st.Discontinuities)
	assert.False(t, st.Failed)
	assert.False(t, st.LastSegmentPDT.IsZero(), "a cut segment must advance the health timestamp")
}

// TestDiscontinuitySequenceIsStableAcrossReloads is the end-to-end property
// EXT-X-DISCONTINUITY-SEQUENCE exists to provide: the number a client computes
// for a given segment must not change as the window scrolls. RFC 8216 section
// 6.2.1 defines it as the tag value plus the EXT-X-DISCONTINUITY tags
// preceding the segment, so this recomputes it exactly that way from the
// rendered playlist after every cut.
//
// The regression this guards is subtle: getting Segment.DiscontinuitySeq right
// internally is not enough, because the head segment's own break is emitted as
// a tag AND folded into the tag value, and a client adds both.
func TestDiscontinuitySequenceIsStableAcrossReloads(t *testing.T) {
	t.Parallel()

	s, err := New(&Config{
		Codec:       newFakeCodec(&fakeCodecOptions{frameSizes: []int{aacFrame}}),
		SampleRate:  testRate,
		Channels:    testChannels,
		BitrateKbps: testBitrate,
		WindowSize:  3,
	})
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, s.Close()) })

	seen := map[string]uint64{}
	// The defect only manifests when a segment CARRYING a break is the oldest
	// one retained, because that is the only position where the renderer emits
	// its EXT-X-DISCONTINUITY and the tag value must exclude it. A run that
	// never reaches that position would pass with the bug fully restored, so
	// count the visits and require them.
	headBreaks := 0
	at := testEpoch
	// Drive several segments with two stalls, checking the whole playlist
	// after every write so each segment is observed in many window positions.
	for i := range 24 {
		writeSamples(t, s, testRate, at)
		at = at.Add(time.Second)
		if i == 6 || i == 14 {
			at = at.Add(10 * time.Second) // stall
		}

		if w := s.segments.retained(); len(w) > 0 && w[0].Discontinuity {
			headBreaks++
		}
		for name, ds := range computeDiscontinuitySeqs(t, s.Playlist()) {
			if prev, ok := seen[name]; ok {
				assert.Equal(t, prev, ds,
					"%s changed discontinuity sequence from %d to %d between reloads", name, prev, ds)
			}
			seen[name] = ds
		}
	}

	require.NotEmpty(t, seen)
	assert.Positive(t, s.Stats().Discontinuities, "the stalls must have produced breaks")
	require.Positive(t, headBreaks,
		"this run never placed a break-carrying segment at the window head, so it "+
			"cannot have exercised the regression it exists to catch")
}

// computeDiscontinuitySeqs derives each segment's discontinuity sequence
// number from a rendered playlist the way a client does.
func computeDiscontinuitySeqs(t *testing.T, playlist string) map[string]uint64 {
	t.Helper()

	out := map[string]uint64{}
	var current uint64
	for line := range strings.SplitSeq(playlist, "\n") {
		switch {
		case strings.HasPrefix(line, "#EXT-X-DISCONTINUITY-SEQUENCE:"):
			v, err := strconv.ParseUint(strings.TrimPrefix(line, "#EXT-X-DISCONTINUITY-SEQUENCE:"), 10, 64)
			require.NoError(t, err)
			current = v
		case line == "#EXT-X-DISCONTINUITY":
			current++
		case strings.HasSuffix(line, segmentNameSuffix):
			out[line] = current
		}
	}
	return out
}

func TestParseSegmentNameRejectsNonCanonicalSpelling(t *testing.T) {
	t.Parallel()

	// ParseUint accepts any number of leading zeros, so without the canonical
	// check an unbounded family of names resolves to one segment. Since this
	// is the only validation applied to the path element, that lets a client
	// mint unlimited distinct cache keys for the same body.
	for _, name := range []string{"segment00.m4s", "segment007.m4s", "segment0000000001.m4s"} {
		_, ok := ParseSegmentName(name)
		assert.False(t, ok, "%q is not the canonical spelling and must be rejected", name)
	}
	// The one legitimate zero still parses.
	seq, ok := ParseSegmentName("segment0.m4s")
	require.True(t, ok)
	assert.Equal(t, uint64(0), seq)
}

// TestOversizedAccessUnitIsRejected covers the bound on what an out-of-tree
// FrameEncoder may emit. Without it the sample count reaches uint32 narrowing
// and a value above the segment target produces a wildly wrong EXTINF.
func TestOversizedAccessUnitIsRejected(t *testing.T) {
	t.Parallel()

	s := newTestStream(t, &fakeCodecOptions{frameSizes: []int{aacFrame}})
	err := s.appendAccessUnit(make([]byte, 4), s.targetSamples+1)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "access unit")
}

// TestPersistentSourceSlownessIsObservable: the drift absorber deliberately
// hides a steady clock difference, which also hides a source that keeps
// delivering less audio than real time. Stats must expose the accumulated
// correction so that case is diagnosable rather than silent.
func TestPersistentSourceSlownessIsObservable(t *testing.T) {
	t.Parallel()

	s := newTestStream(t, &fakeCodecOptions{frameSizes: []int{aacFrame}})

	// 90 ms of audio per 100 ms of wall clock.
	const chunk = testRate / 10
	at := testEpoch
	for range 200 {
		writeSamples(t, s, chunk*9/10, at)
		at = at.Add(100 * time.Millisecond)
	}

	st := s.Stats()
	assert.Zero(t, st.Discontinuities,
		"steady slowness is absorbed rather than reported as a stall, by design")
	assert.Greater(t, st.DriftCorrection, time.Second,
		"the absorbed correction must be observable, or sustained audio loss is invisible")
}

// errFakeSegment is returned by a stream whose segment flush is made to fail.
var errFakeSegment = errors.New("fake segment flush failure")

// TestCutFailureDuringStallIsReported covers the path where the container
// refuses the segment that closes out a stalled timeline. Reporting success
// there would be the worst outcome: the break is never published, the stream
// is latched, and the caller has been told the write landed.
func TestCutFailureDuringStallIsReported(t *testing.T) {
	t.Parallel()

	s := newTestStream(t, &fakeCodecOptions{frameSizes: []int{aacFrame}})

	// Accumulate a partial segment on the old timeline.
	const before = testRate
	writeSamples(t, s, before, testEpoch)
	require.Positive(t, s.pendingSamples)

	s.appendSegment = func([]byte) ([]byte, error) { return nil, errFakeSegment }

	// A stall now forces a cut, which fails.
	err := s.Write(silence(aacFrame, testChannels), sampleTime(before).Add(5*time.Second))
	require.Error(t, err, "a failed cut must not be reported as a successful write")
	require.ErrorIs(t, err, errFakeSegment)
	assert.True(t, s.Stats().Failed)
}

// TestCutFailureDuringNormalWriteIsReported covers the same flush failure on
// the ordinary path, where the segment is cut because it reached the target
// rather than because the timeline broke.
func TestCutFailureDuringNormalWriteIsReported(t *testing.T) {
	t.Parallel()

	s := newTestStream(t, &fakeCodecOptions{frameSizes: []int{aacFrame}})
	s.appendSegment = func([]byte) ([]byte, error) { return nil, errFakeSegment }

	err := s.Write(silence(testRate*3, testChannels), testEpoch)
	require.Error(t, err)
	require.ErrorIs(t, err, errFakeSegment)
	assert.True(t, len(s.segments.retained()) == 0 || s.Stats().Failed)
}

// TestNewRejectsSegmentTargetBelowOneAccessUnit covers the configurations that
// previously built a Stream successfully and then rejected every frame the
// encoder produced, surfacing as a latched failure on the audio goroutine
// instead of an error the caller could act on at construction.
func TestNewRejectsSegmentTargetBelowOneAccessUnit(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		rate       int
		segment    time.Duration
		frameSizes []int
		errMsg     string
	}{
		{
			name: "8 kHz with a 100ms segment cannot hold an AAC access unit",
			rate: 8000, segment: 100 * time.Millisecond,
			frameSizes: []int{aacFrame}, errMsg: "smaller than codec",
		},
		{
			name: "a large-frame codec needs a longer segment",
			rate: testRate, segment: 100 * time.Millisecond,
			frameSizes: []int{8192}, errMsg: "smaller than codec",
		},
		{
			name: "a 1 Hz rate yields less than one sample per segment",
			rate: 1, segment: MinSegmentDuration,
			frameSizes: []int{aacFrame}, errMsg: "less than one sample",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var enc *fakeEncoder
			s, err := New(&Config{
				Codec:           newFakeCodec(&fakeCodecOptions{frameSizes: tt.frameSizes, captured: &enc}),
				SampleRate:      tt.rate,
				Channels:        testChannels,
				BitrateKbps:     testBitrate,
				SegmentDuration: tt.segment,
			})
			require.Error(t, err)
			assert.Nil(t, s)
			assert.Contains(t, err.Error(), tt.errMsg)
			require.NotNil(t, enc)
			assert.True(t, enc.closed, "a rejected configuration must still release the encoder")
		})
	}
}

// TestNewAcceptsASegmentThatFitsExactlyOneAccessUnit pins the boundary: the
// check must reject a target below one access unit without also rejecting a
// target that holds exactly one.
func TestNewAcceptsASegmentThatFitsExactlyOneAccessUnit(t *testing.T) {
	t.Parallel()

	// 1024 samples at 48 kHz is 21.333ms, so a 200ms segment holds 9600.
	s, err := New(&Config{
		Codec:           newFakeCodec(&fakeCodecOptions{frameSizes: []int{aacFrame}}),
		SampleRate:      testRate,
		Channels:        testChannels,
		BitrateKbps:     testBitrate,
		SegmentDuration: 200 * time.Millisecond,
	})
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, s.Close()) })
	assert.GreaterOrEqual(t, s.targetSamples, aacFrame)

	// And it must actually stream rather than merely construct.
	writeSamples(t, s, testRate, testEpoch)
	assert.True(t, s.Ready(1))
}
