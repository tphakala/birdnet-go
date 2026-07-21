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

	// bitDepth16 is the PCM sample width this muxer assumes. The capture
	// pipeline is 16-bit end to end.
	//
	// Note this is an assumption, not a check: Config carries no bit depth and
	// Write takes raw bytes, so a wider input is only caught when it happens to
	// break the sample-frame alignment test, which 24- and 32-bit stereo do
	// not. Callers are responsible for delivering 16-bit PCM.
	bitDepth16 = 16

	// bytesPerSample is the width of one sample of one channel at bitDepth16.
	bytesPerSample = bitDepth16 / 8

	// DefaultSegmentDuration is the nominal length of a media segment. Two
	// seconds is the usual live-HLS compromise: short enough to keep join
	// latency low, long enough that per-segment overhead stays negligible.
	DefaultSegmentDuration = 2 * time.Second

	// MinSegmentDuration and MaxSegmentDuration bound what a caller may ask
	// for. The floor keeps SegmentDuration above one sample period, below
	// which every access unit would become its own segment; the ceiling keeps
	// the segment inside go-m4a's own per-segment limits and keeps the
	// duration-to-samples conversion far from overflow.
	MinSegmentDuration = 100 * time.Millisecond
	MaxSegmentDuration = 60 * time.Second

	// DefaultWindowSize is how many media segments the playlist advertises.
	// It must stay above hls.js's liveSyncDurationCount, which is why the
	// FFmpeg path settled on six as well.
	DefaultWindowSize = 6

	// defaultStallGapFraction divides the effective segment duration to give
	// the default stall threshold, so the default tracks the configured
	// segment length instead of pinning to the package default.
	//
	// Half a segment is the useful boundary: smaller divergences are absorbed
	// as drift (see driftCorrectionShift), while a jump larger than this means
	// real audio is missing and a player that decoded straight through would
	// report a wall-clock time that never happened.
	defaultStallGapFraction = 2

	// driftCorrectionShift sets how fast the projected sample clock is pulled
	// back toward the timestamps actually arriving, as a right shift of the
	// observed difference (4 => one sixteenth per write).
	//
	// Without this the projection only ever advances by sample count, so any
	// difference between the source's sample clock and wall clock accumulates
	// forever. A commodity 100 ppm crystal reaches a one-second divergence in
	// under three hours, which would then be reported as a stall on a
	// perfectly healthy stream. Correcting a fraction per write absorbs that
	// steady error while leaving a genuine step change well above the
	// threshold on the write that carries it, because the threshold is tested
	// before the correction is applied.
	driftCorrectionShift = 4
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

	// BitrateKbps is the target bitrate passed to the encoder. Zero leaves the
	// choice to the codec.
	BitrateKbps int

	// SegmentDuration is the maximum media segment length. Segments are cut on
	// access-unit boundaries at or below this, never above it, so the playlist
	// can advertise it as a true target duration. Zero selects
	// DefaultSegmentDuration; a non-zero value must lie between
	// MinSegmentDuration and MaxSegmentDuration.
	SegmentDuration time.Duration

	// WindowSize is how many segments the playlist advertises and the stream
	// keeps in memory. Zero selects DefaultWindowSize.
	WindowSize int

	// MaxStallGap is the timestamp divergence that triggers a discontinuity.
	// Zero selects half the effective SegmentDuration.
	MaxStallGap time.Duration
}

// Stats is a point-in-time view of a stream's health, for the caller to log or
// surface. The muxer itself neither logs nor measures anything.
type Stats struct {
	// Segments is how many media segments have been cut for the life of the
	// stream, which is also the sequence number the next one will take.
	Segments uint64

	// Retained is how many segments the playlist currently advertises.
	Retained int

	// Discontinuities is how many timeline breaks have been declared.
	Discontinuities uint64

	// LastSegmentPDT is the wall-clock start of the most recently cut segment,
	// zero before the first cut. A value that stops advancing is the native
	// equivalent of the FFmpeg path's stale-segment check.
	LastSegmentPDT time.Time

	// DriftCorrection is the cumulative adjustment applied to the projected
	// sample clock. A value that keeps growing in one direction means the
	// source is delivering audio persistently slower or faster than real time,
	// which is absorbed silently and would otherwise be invisible.
	DriftCorrection time.Duration

	// Failed reports that an encode error has latched the stream unusable.
	Failed bool
}

// Stream encodes and muxes one live audio source into HLS media segments and
// the playlist indexing them, holding everything in memory.
//
// Write is called from the audio feed goroutine; Playlist, Segment, Stats and
// InitSegment are called from HTTP handlers. All of them are safe to call
// concurrently.
type Stream struct {
	// mu guards everything below it. The critical sections are short: an
	// encode plus a buffer append on the write side, a render or a lookup on
	// the read side.
	mu sync.Mutex

	codecName string
	enc       FrameEncoder
	frag      *m4a.FragmentWriter
	initSeg   []byte
	segments  *ring

	// emit is appendAccessUnit bound to this Stream, built once. Passing the
	// method value directly would build a fresh closure on every Write, which
	// escapes because it crosses an interface call, so it would be one heap
	// allocation per audio frame for a value that never changes.
	emit EmitFunc

	// targetSamples is the segment length in samples; a segment is cut before
	// it would exceed this, never after. maxSegmentDuration is the same bound
	// as a duration, and targetDuration is its ceiling in whole seconds.
	targetSamples      int
	maxSegmentDuration time.Duration
	targetDuration     int

	maxStallGap time.Duration
	frameBytes  int

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
	// currently accumulating; lastSegmentPDT is that of the last one cut.
	segPDT         time.Time
	lastSegmentPDT time.Time

	// nextSampleTime is the wall-clock time the next incoming sample is
	// expected to carry. Comparing it against the timestamp that actually
	// arrives is how a source stall is detected.
	nextSampleTime time.Time

	// driftCorrection is the total the projected sample clock has been nudged
	// toward the arriving timestamps. It only grows in one direction while a
	// source runs persistently fast or slow, so a caller watching it can tell
	// steady clock error (bounded, harmless) from sustained audio loss
	// (unbounded), which the stall threshold alone cannot distinguish because
	// the absorber is what stops the latter from ever reaching it.
	driftCorrection time.Duration

	// discontinuities counts the timeline breaks so far, and pendingBreak
	// records that the next segment cut begins a new timeline.
	discontinuities uint64
	pendingBreak    bool

	// segBufHint sizes the buffer each cut segment is built into, tracking the
	// previous segment's length so a steady-state stream stops re-growing.
	segBufHint int

	started bool
	failed  bool
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
			Category(errors.CategoryValidation).
			Context("codec", cfg.Codec.Name).
			Build()
	}
	if enc == nil {
		return nil, errors.Newf("hlsmux: codec %q built a nil encoder", cfg.Codec.Name).
			Component(component).
			Category(errors.CategoryValidation).
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
		maxStallGap = segmentDuration / defaultStallGapFraction
	}

	rate := int64(cfg.SampleRate)
	s := &Stream{
		codecName:          cfg.Codec.Name,
		enc:                enc,
		frag:               frag,
		initSeg:            initSeg,
		segments:           newRing(windowSize),
		targetSamples:      int(int64(segmentDuration) * rate / int64(time.Second)),
		maxSegmentDuration: segmentDuration,
		targetDuration:     ceilSeconds(segmentDuration),
		maxStallGap:        maxStallGap,
		frameBytes:         cfg.Channels * bytesPerSample,
		segClock:           sampleClock{rate: rate},
		inClock:            sampleClock{rate: rate},
	}
	s.emit = s.appendAccessUnit
	return s, nil
}

// ceilSeconds rounds a duration up to whole seconds, with a floor of one.
//
// EXT-X-TARGETDURATION is a whole number of seconds that every segment must
// fit within, so the ceiling of the segment bound is the smallest value that
// is always truthful. Rounding to nearest instead would understate a 2.4 s
// bound as 2, which RFC 8216 tolerates on a rounding reading but Apple's
// mediastreamvalidator treats as a segment overrunning its target.
func ceilSeconds(d time.Duration) int {
	secs := int((d + time.Second - 1) / time.Second)
	if secs < 1 {
		return 1
	}
	return secs
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
		Category(errors.CategoryValidation).
		Context("codec", codecName).
		Build()
}

// validate checks the configuration and reports the first problem found.
func (c *Config) validate() error {
	switch {
	case !c.Codec.valid():
		return validationErr("hlsmux: codec is not set")
	case c.SampleRate <= 0:
		return validationErr("hlsmux: sample rate must be positive, got %d", c.SampleRate)
	case c.Channels != 1 && c.Channels != 2:
		return validationErr("hlsmux: channel count must be 1 or 2, got %d", c.Channels)
	case c.BitrateKbps < 0:
		return validationErr("hlsmux: bitrate must not be negative, got %d kbps", c.BitrateKbps)
	case c.SegmentDuration != 0 && c.SegmentDuration < MinSegmentDuration:
		return validationErr("hlsmux: segment duration %s is below the minimum of %s", c.SegmentDuration, MinSegmentDuration)
	case c.SegmentDuration > MaxSegmentDuration:
		return validationErr("hlsmux: segment duration %s exceeds the maximum of %s", c.SegmentDuration, MaxSegmentDuration)
	case c.WindowSize < 0:
		return validationErr("hlsmux: window size must not be negative, got %d", c.WindowSize)
	case c.MaxStallGap < 0:
		return validationErr("hlsmux: max stall gap must not be negative, got %s", c.MaxStallGap)
	}
	return nil
}

// validationErr builds a configuration error with this package's component and
// category, so the many validate arms stay readable.
func validationErr(format string, args ...any) error {
	return errors.Newf(format, args...).
		Component(component).
		Category(errors.CategoryValidation).
		Build()
}

// audioErr builds a runtime error with this package's component and category.
func (s *Stream) audioErr(err error) error {
	return errors.New(err).
		Component(component).
		Category(errors.CategoryAudio).
		Context("codec", s.codecName).
		Build()
}

// Write encodes interleaved PCM and appends the resulting access units to the
// segment being accumulated, cutting a segment whenever the next unit would
// take it past the target duration.
//
// ts is the wall-clock capture time of the first sample in pcm. It is what
// anchors EXT-X-PROGRAM-DATE-TIME to real time. Deriving the time from the
// sample count alone would be wrong for a monitoring system: a source that
// stalls and resumes produces no samples for the gap, so every later timestamp
// would be early by the length of the stall, permanently and silently. When
// the divergence exceeds MaxStallGap the stream declares a discontinuity and
// re-anchors to ts; smaller divergences are absorbed as clock drift.
//
// An encode failure latches the stream: the timeline cannot be reconciled with
// audio that was consumed but never encoded, so every later Write is refused
// and the caller must tear the stream down rather than continue with a
// silently shifted timeline.
func (s *Stream) Write(pcm []byte, ts time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	switch {
	case s.closed:
		return validationErr("hlsmux: write to a closed stream")
	case s.failed:
		return validationErr("hlsmux: write to a stream that failed encoding")
	case ts.IsZero():
		return validationErr("hlsmux: write with a zero capture timestamp")
	case len(pcm) == 0:
		return nil
	case len(pcm)%s.frameBytes != 0:
		return validationErr("hlsmux: PCM length %d is not a multiple of the %d-byte sample frame", len(pcm), s.frameBytes)
	}

	if err := s.syncTimeline(ts); err != nil {
		return err
	}

	// Project where the following frame should start before encoding, so the
	// projection is unaffected by how the encoder buffers this input.
	s.nextSampleTime = s.nextSampleTime.Add(s.inClock.advance(len(pcm) / s.frameBytes))

	if err := s.enc.EncodeInterleaved(pcm, s.emit); err != nil {
		s.failed = true
		return s.audioErr(err)
	}
	return nil
}

// syncTimeline anchors the stream on its first write and, on later writes,
// decides whether the incoming timestamp represents ordinary drift or a real
// break in the source.
func (s *Stream) syncTimeline(ts time.Time) error {
	if !s.started {
		s.started = true
		s.segPDT = ts
		s.nextSampleTime = ts
		return nil
	}

	// Compare against both bounds rather than taking an absolute value: a
	// clock that jumps far enough backwards makes Sub saturate at the minimum
	// Duration, and negating that yields the minimum again, which would read
	// as no gap at all and let the largest possible jump through unnoticed.
	gap := ts.Sub(s.nextSampleTime)
	if gap >= -s.maxStallGap && gap <= s.maxStallGap {
		// Within tolerance: pull the projection a fraction of the way toward
		// the observed time so a steady clock difference cannot accumulate
		// into a false stall.
		s.nextSampleTime = s.nextSampleTime.Add(gap >> driftCorrectionShift)
		s.driftCorrection += gap >> driftCorrectionShift
		return nil
	}

	// The timeline broke. Close out whatever belongs to the old one before
	// re-anchoring, so the published segment keeps its correct duration and
	// PDT. Note the encoder may still hold a partial access unit of pre-gap
	// audio, which will be emitted into the first post-gap segment; the
	// container has no way to represent a break inside an access unit.
	if s.pendingSamples > 0 {
		if err := s.cutSegment(); err != nil {
			// The container refused the segment, so the timeline cannot be
			// closed out cleanly. Latch and surface it rather than reporting
			// the write as successful: a caller that kept feeding would build
			// every later segment on a timeline whose break was never
			// published.
			s.failed = true
			return err
		}
	}
	s.pendingBreak = true
	s.segPDT = ts
	s.nextSampleTime = ts
	return nil
}

// appendAccessUnit buffers one coded access unit into the current segment,
// cutting the current segment first when the unit would take it past the
// target. It is the EmitFunc handed to the encoder, so it runs with s.mu
// already held; an implementation that called back into a Stream method from
// here would deadlock.
func (s *Stream) appendAccessUnit(au []byte, samples int) error {
	if samples <= 0 || samples > s.targetSamples {
		return validationErr("hlsmux: encoder emitted an access unit of %d samples, want 1 to %d", samples, s.targetSamples)
	}

	// Cut before overflowing rather than after reaching the target, so a
	// segment never exceeds the duration the playlist advertises.
	if s.pendingSamples > 0 && s.pendingSamples+samples > s.targetSamples {
		if err := s.cutSegment(); err != nil {
			return err
		}
	}

	// WriteFrameDuration rather than WriteFrame: it carries the unit's own
	// duration, so a codec with variable-length frames works unchanged, and
	// go-m4a copies the access unit, which is what makes the borrowed-slice
	// contract on EmitFunc safe to honour without copying here.
	if err := s.frag.WriteFrameDuration(au, uint32(samples)); err != nil { //nolint:gosec // samples is bounded by targetSamples above
		return s.audioErr(err)
	}
	s.pendingSamples += samples
	return nil
}

// cutSegment flushes the buffered access units into a finished media segment
// and publishes it. It must be called with s.mu held and with at least one
// access unit buffered.
func (s *Stream) cutSegment() error {
	// A fresh destination every time: a published segment's bytes are handed
	// out to HTTP handlers that may still be writing them to a client, so the
	// arena must never be reused underneath them. Sized from the previous
	// segment, which in a constant-bitrate stream is within a few percent, so
	// the build stops re-growing after the first cut.
	data, err := s.frag.AppendSegment(make([]byte, 0, s.segBufHint))
	if err != nil {
		return s.audioErr(err)
	}
	s.segBufHint = len(data) + len(data)/16

	duration := s.segClock.advance(s.pendingSamples)
	// Count the break before publishing: a segment preceded by
	// EXT-X-DISCONTINUITY has the predecessor's discontinuity sequence number
	// plus one (RFC 8216 section 6.2.1). Assigning the predecessor's value and
	// deferring the increment would make the number a client computes for a
	// given segment change as the window scrolls, which is precisely what
	// EXT-X-DISCONTINUITY-SEQUENCE exists to keep stable.
	if s.pendingBreak {
		s.discontinuities++
	}
	s.segments.push(&Segment{
		Seq:              s.nextSeq,
		Data:             data,
		Samples:          s.pendingSamples,
		Duration:         duration,
		PDT:              s.segPDT,
		Discontinuity:    s.pendingBreak,
		DiscontinuitySeq: s.discontinuities,
	})
	s.pendingBreak = false

	// The next segment starts exactly where this one ended. Durations are
	// exact, so accumulating PDT this way stays locked to the sample timeline
	// until a discontinuity re-anchors it to wall clock.
	s.lastSegmentPDT = s.segPDT
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

	var flushErr error
	if !s.failed {
		flushErr = s.enc.Flush(s.emit)
		if flushErr == nil && s.pendingSamples > 0 {
			flushErr = s.cutSegment()
		}
	}

	closeErr := s.enc.Close()
	switch {
	case flushErr != nil && closeErr != nil:
		return s.audioErr(fmt.Errorf("%w (also failed to close encoder: %w)", flushErr, closeErr))
	case flushErr != nil:
		return s.audioErr(flushErr)
	case closeErr != nil:
		return s.audioErr(closeErr)
	}
	return nil
}

// InitSegment returns the fragmented-MP4 initialization segment the playlist
// names in EXT-X-MAP. It is fixed for the life of the stream and safe to serve
// repeatedly; callers must not modify it.
//
// No lock: initSeg is written once in New before the Stream is published and
// never again.
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
	return renderPlaylist(s.segments, s.targetDuration, s.closed)
}

// Ready reports whether the playlist advertises at least n media segments.
//
// Callers should wait for more than one before advertising the stream: hls.js
// holds off starting playback until it has several fragments, so a player
// handed a one-segment playlist spends a full reload cycle waiting, which is
// audible as a delayed start.
func (s *Stream) Ready(n int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.segments.len() >= n
}

// Stats returns a point-in-time view of the stream's health.
func (s *Stream) Stats() Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Stats{
		Segments:        s.nextSeq,
		Retained:        s.segments.len(),
		Discontinuities: s.discontinuities,
		LastSegmentPDT:  s.lastSegmentPDT,
		DriftCorrection: s.driftCorrection,
		Failed:          s.failed,
	}
}
