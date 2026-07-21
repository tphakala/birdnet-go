package hlsmux

import (
	"fmt"
	"sync"
	"time"

	m4a "github.com/tphakala/go-m4a"

	"github.com/tphakala/birdnet-go/internal/errors"
)

const (
	// component is the error-telemetry component name for this package.
	component = "audiocore/hlsmux"

	// bitDepth16 is the only PCM bit depth this muxer accepts. The capture
	// pipeline is 16-bit end to end, and accepting a width the encoders would
	// silently reinterpret buys nothing.
	bitDepth16 = 16

	// bytesPerSample is the width of one sample of one channel at bitDepth16.
	bytesPerSample = bitDepth16 / 8

	// DefaultSegmentDuration is the nominal length of a media segment. Two
	// seconds is the usual live-HLS compromise: short enough to keep join
	// latency low, long enough that per-segment overhead stays negligible.
	DefaultSegmentDuration = 2 * time.Second

	// DefaultWindowSize is how many media segments the playlist advertises.
	// It must stay above hls.js's liveSyncDurationCount, which is why the
	// FFmpeg path settled on six as well.
	DefaultWindowSize = 6

	// DefaultMaxStallGap is how far the incoming frame timestamps may diverge
	// from the sample clock before the timeline is treated as broken rather
	// than jittery. Below this the difference is absorbed silently; above it
	// the stream cuts a segment and declares a discontinuity.
	//
	// Half a segment is the useful boundary: smaller gaps are indistinguishable
	// from ordinary scheduling jitter and buffer bunching, while anything
	// larger means real audio is missing and a player that kept decoding
	// straight through would report a wall-clock time that never happened.
	DefaultMaxStallGap = DefaultSegmentDuration / 2
)

// Config describes the live stream to be produced. Everything codec-specific
// is carried by Codec, so this stays the same shape whichever codec is used.
type Config struct {
	// Codec supplies the encoder and the container description. Required.
	Codec Codec

	// SampleRate is the output sample rate in Hz. Required.
	//
	// Callers should pin this to 48000 rather than following the source: the
	// audio router already inserts a resampler whenever a source's rate
	// differs from the consumer's declared rate, so a fixed output rate costs
	// nothing and removes a whole class of rate-mismatch playback bugs.
	SampleRate int

	// Channels is the output channel count, 1 or 2. Required.
	Channels int

	// BitrateKbps is the target bitrate passed to the encoder. Required for
	// lossy codecs.
	BitrateKbps int

	// SegmentDuration is the nominal media segment length. Segments are cut on
	// access-unit boundaries, so the real duration lands within one access
	// unit of this. Zero selects DefaultSegmentDuration.
	SegmentDuration time.Duration

	// WindowSize is how many segments the playlist advertises and the stream
	// keeps in memory. Zero selects DefaultWindowSize.
	WindowSize int

	// MaxStallGap is the timestamp divergence that triggers a discontinuity.
	// Zero selects DefaultMaxStallGap.
	MaxStallGap time.Duration
}

// Stream encodes and muxes one live audio source into HLS media segments and
// the playlist indexing them, holding everything in memory.
//
// Write is called from the audio feed goroutine; Playlist, Segment and
// InitSegment are called from HTTP handlers. All of them are safe to call
// concurrently.
type Stream struct {
	// mu guards everything below it. The critical sections are short: an
	// encode plus a buffer append on the write side, a render or a lookup on
	// the read side.
	mu sync.Mutex

	codec    Codec
	enc      FrameEncoder
	frag     *m4a.FragmentWriter
	initSeg  []byte
	segments *ring

	// targetSamples is SegmentDuration expressed in samples, the threshold a
	// segment's accumulated access units must reach before it is cut.
	targetSamples int
	maxStallGap   time.Duration
	frameBytes    int

	// segClock converts a cut segment's sample count to its exact duration;
	// inClock does the same for incoming PCM to project the next expected
	// frame timestamp. They are separate because they advance over different
	// quantities, and each carries its own remainder.
	segClock sampleClock
	inClock  sampleClock

	// nextSeq is the sequence number the next cut segment will take.
	nextSeq uint64

	// pendingSamples is how many samples are buffered in frag for the segment
	// currently accumulating.
	pendingSamples int

	// segPDT is the wall-clock time of the first sample of the segment
	// currently accumulating.
	segPDT time.Time

	// nextSampleTime is the wall-clock time the next incoming sample is
	// expected to carry. Comparing it against the timestamp that actually
	// arrives is how a source stall is detected.
	nextSampleTime time.Time

	// discontinuities counts the timeline breaks so far, and pendingBreak
	// records that the next segment cut begins a new timeline.
	discontinuities uint64
	pendingBreak    bool

	started bool
	ended   bool
	closed  bool
}

// sampleClock converts sample counts to durations without accumulating
// truncation error.
//
// The naive samples*time.Second/rate loses the remainder on every call, and a
// live stream makes that call forever. Carrying the remainder into the next
// conversion makes consecutive durations sum to exactly the duration of the
// total sample count. Computing from a running total instead would be equally
// exact but overflows int64 after about 53 hours at 48 kHz, which a live
// stream reaches.
type sampleClock struct {
	rate  int64
	carry int64
}

// advance converts samples to a duration, folding in the remainder left by
// previous conversions.
func (c *sampleClock) advance(samples int) time.Duration {
	total := int64(samples)*int64(time.Second) + c.carry
	c.carry = total % c.rate
	return time.Duration(total / c.rate)
}

// New creates a live stream. It builds the encoder and the init segment up
// front, so a caller that gets a Stream back has already had the codec
// configuration validated.
func New(cfg *Config) (*Stream, error) {
	if cfg == nil {
		return nil, errors.Newf("hlsmux: config is nil").
			Component(component).
			Category(errors.CategoryValidation).
			Build()
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}

	encCfg := EncoderConfig{
		SampleRate:  cfg.SampleRate,
		Channels:    cfg.Channels,
		BitrateKbps: cfg.BitrateKbps,
	}

	enc, err := cfg.Codec.newEncoder(encCfg)
	if err != nil {
		return nil, errors.New(err).
			Component(component).
			Category(errors.CategoryAudio).
			Context("codec", cfg.Codec.Name).
			Build()
	}

	writerCfg, err := cfg.Codec.writerConfig(encCfg, enc)
	if err != nil {
		return nil, closeEncoderAndWrap(enc, err, cfg.Codec.Name)
	}

	initSeg, err := m4a.InitSegment(writerCfg)
	if err != nil {
		return nil, closeEncoderAndWrap(enc, err, cfg.Codec.Name)
	}

	frag, err := m4a.NewFragmentWriter(writerCfg)
	if err != nil {
		return nil, closeEncoderAndWrap(enc, err, cfg.Codec.Name)
	}

	segmentDuration := cfg.SegmentDuration
	if segmentDuration == 0 {
		segmentDuration = DefaultSegmentDuration
	}
	windowSize := cfg.WindowSize
	if windowSize == 0 {
		windowSize = DefaultWindowSize
	}
	maxStallGap := cfg.MaxStallGap
	if maxStallGap == 0 {
		maxStallGap = DefaultMaxStallGap
	}

	rate := int64(cfg.SampleRate)
	return &Stream{
		codec:         cfg.Codec,
		enc:           enc,
		frag:          frag,
		initSeg:       initSeg,
		segments:      newRing(windowSize),
		targetSamples: int(segmentDuration * time.Duration(cfg.SampleRate) / time.Second),
		maxStallGap:   maxStallGap,
		frameBytes:    cfg.Channels * bytesPerSample,
		segClock:      sampleClock{rate: rate},
		inClock:       sampleClock{rate: rate},
	}, nil
}

// closeEncoderAndWrap releases a half-built stream's encoder and wraps the
// error that made it unusable. Without it a failure between constructing the
// encoder and returning the Stream would leak whatever the encoder holds.
func closeEncoderAndWrap(enc FrameEncoder, err error, codecName string) error {
	if closeErr := enc.Close(); closeErr != nil {
		err = fmt.Errorf("%w (also failed to close encoder: %w)", err, closeErr)
	}
	return errors.New(err).
		Component(component).
		Category(errors.CategoryAudio).
		Context("codec", codecName).
		Build()
}

// validate checks the configuration and reports the first problem found.
func (c *Config) validate() error {
	switch {
	case !c.Codec.valid():
		return errors.Newf("hlsmux: codec is not set").
			Component(component).
			Category(errors.CategoryValidation).
			Build()
	case c.SampleRate <= 0:
		return errors.Newf("hlsmux: sample rate must be positive, got %d", c.SampleRate).
			Component(component).
			Category(errors.CategoryValidation).
			Build()
	case c.Channels != 1 && c.Channels != 2:
		return errors.Newf("hlsmux: channel count must be 1 or 2, got %d", c.Channels).
			Component(component).
			Category(errors.CategoryValidation).
			Build()
	case c.BitrateKbps < 0:
		return errors.Newf("hlsmux: bitrate must not be negative, got %d kbps", c.BitrateKbps).
			Component(component).
			Category(errors.CategoryValidation).
			Build()
	case c.SegmentDuration < 0:
		return errors.Newf("hlsmux: segment duration must not be negative, got %s", c.SegmentDuration).
			Component(component).
			Category(errors.CategoryValidation).
			Build()
	case c.WindowSize < 0:
		return errors.Newf("hlsmux: window size must not be negative, got %d", c.WindowSize).
			Component(component).
			Category(errors.CategoryValidation).
			Build()
	case c.MaxStallGap < 0:
		return errors.Newf("hlsmux: max stall gap must not be negative, got %s", c.MaxStallGap).
			Component(component).
			Category(errors.CategoryValidation).
			Build()
	}
	return nil
}

// Write encodes interleaved PCM and appends the resulting access units to the
// segment being accumulated, cutting a segment whenever it reaches the target
// duration.
//
// ts is the wall-clock capture time of the first sample in pcm. It is what
// anchors EXT-X-PROGRAM-DATE-TIME to real time. Deriving the time from the
// sample count alone would be wrong for a monitoring system: a source that
// stalls and resumes produces no samples for the gap, so every later timestamp
// would be early by the length of the stall, permanently and silently. When
// the gap exceeds MaxStallGap the stream instead declares a discontinuity and
// re-anchors to ts.
func (s *Stream) Write(pcm []byte, ts time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return errors.Newf("hlsmux: write to a closed stream").
			Component(component).
			Category(errors.CategoryAudio).
			Build()
	}
	if len(pcm) == 0 {
		return nil
	}
	if len(pcm)%s.frameBytes != 0 {
		return errors.Newf("hlsmux: PCM length %d is not a multiple of the %d-byte sample frame", len(pcm), s.frameBytes).
			Component(component).
			Category(errors.CategoryValidation).
			Build()
	}

	if err := s.syncTimeline(ts); err != nil {
		return err
	}

	// Project where the following frame should start before encoding, so the
	// projection is unaffected by how the encoder buffers this input.
	s.nextSampleTime = s.nextSampleTime.Add(s.inClock.advance(len(pcm) / s.frameBytes))

	if err := s.enc.EncodeInterleaved(pcm, s.appendAccessUnit); err != nil {
		return errors.New(err).
			Component(component).
			Category(errors.CategoryAudio).
			Context("codec", s.codec.Name).
			Build()
	}
	return nil
}

// syncTimeline anchors the stream on its first write and, on later writes,
// decides whether the incoming timestamp represents ordinary jitter or a real
// break in the source.
func (s *Stream) syncTimeline(ts time.Time) error {
	if !s.started {
		s.started = true
		s.segPDT = ts
		s.nextSampleTime = ts
		return nil
	}

	gap := ts.Sub(s.nextSampleTime)
	if gap < 0 {
		gap = -gap
	}
	if gap <= s.maxStallGap {
		return nil
	}

	// The timeline broke. Close out whatever belongs to the old one before
	// re-anchoring, so the partial segment keeps its correct duration and PDT
	// instead of being merged across the gap.
	if s.pendingSamples > 0 {
		if err := s.cutSegment(); err != nil {
			return err
		}
	}
	s.pendingBreak = true
	s.segPDT = ts
	s.nextSampleTime = ts
	return nil
}

// appendAccessUnit buffers one coded access unit into the current segment and
// cuts the segment once it has reached the target duration. It is the EmitFunc
// handed to the encoder, so it runs with s.mu already held.
func (s *Stream) appendAccessUnit(au []byte, samples int) error {
	if samples <= 0 {
		return errors.Newf("hlsmux: encoder emitted an access unit with %d samples", samples).
			Component(component).
			Category(errors.CategoryAudio).
			Build()
	}

	// WriteFrameDuration rather than WriteFrame: it carries the unit's own
	// duration, so a codec with variable-length frames works unchanged, and
	// go-m4a copies the access unit, which is what makes the borrowed-slice
	// contract on EmitFunc safe to honour without copying here.
	if err := s.frag.WriteFrameDuration(au, uint32(samples)); err != nil {
		return errors.New(err).
			Component(component).
			Category(errors.CategoryAudio).
			Context("codec", s.codec.Name).
			Build()
	}
	s.pendingSamples += samples

	if s.pendingSamples >= s.targetSamples {
		return s.cutSegment()
	}
	return nil
}

// cutSegment flushes the buffered access units into a finished media segment
// and publishes it. It must be called with s.mu held and with at least one
// access unit buffered.
func (s *Stream) cutSegment() error {
	// A fresh destination every time: a published segment's bytes are handed
	// out to HTTP handlers that may still be writing them to a client, so the
	// arena must never be reused underneath them.
	data, err := s.frag.AppendSegment(nil)
	if err != nil {
		return errors.New(err).
			Component(component).
			Category(errors.CategoryAudio).
			Context("codec", s.codec.Name).
			Build()
	}

	duration := s.segClock.advance(s.pendingSamples)
	s.segments.push(&Segment{
		Seq:              s.nextSeq,
		Data:             data,
		Samples:          s.pendingSamples,
		Duration:         duration,
		PDT:              s.segPDT,
		Discontinuity:    s.pendingBreak,
		DiscontinuitySeq: s.discontinuities,
	})

	if s.pendingBreak {
		s.discontinuities++
		s.pendingBreak = false
	}
	// The next segment starts exactly where this one ended. Durations are
	// exact, so accumulating PDT this way stays locked to the sample timeline
	// until a discontinuity re-anchors it to wall clock.
	s.segPDT = s.segPDT.Add(duration)
	s.nextSeq++
	s.pendingSamples = 0
	return nil
}

// Close drains the encoder, publishes whatever audio remains as a final
// segment, and marks the playlist ended so clients stop polling.
//
// Closing twice is not an error, so a caller can Close on a deferred cleanup
// path without tracking whether an earlier error path already did.
func (s *Stream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return nil
	}
	s.closed = true
	s.ended = true

	flushErr := s.enc.Flush(s.appendAccessUnit)
	if flushErr == nil && s.pendingSamples > 0 {
		flushErr = s.cutSegment()
	}

	closeErr := s.enc.Close()
	if flushErr != nil {
		return errors.New(flushErr).
			Component(component).
			Category(errors.CategoryAudio).
			Context("codec", s.codec.Name).
			Build()
	}
	if closeErr != nil {
		return errors.New(closeErr).
			Component(component).
			Category(errors.CategoryAudio).
			Context("codec", s.codec.Name).
			Build()
	}
	return nil
}

// InitSegment returns the fragmented-MP4 initialization segment the playlist
// names in EXT-X-MAP. It is fixed for the life of the stream and safe to serve
// repeatedly; callers must not modify it.
func (s *Stream) InitSegment() []byte {
	return s.initSeg
}

// Segment returns the retained media segment with the given sequence number.
// It reports false once the segment has scrolled out of the window, which is
// what a client that fell too far behind should see as a 404.
func (s *Stream) Segment(seq uint64) (Segment, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.segments.get(seq)
}

// Playlist renders the current HLS media playlist.
func (s *Stream) Playlist() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return renderPlaylist(s.segments, s.ended)
}

// Ready reports whether at least one media segment has been published. A
// player given a playlist with no segments has nothing to fetch, so callers
// should wait for this before advertising the stream.
func (s *Stream) Ready() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.segments.empty()
}
