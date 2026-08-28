package vad

import (
	"github.com/tphakala/birdnet-go/internal/errors"
)

// Rolling-aggregation window for the streaming path.
const (
	// aggWindowSeconds is the span of recent audio the streaming aggregate
	// covers, matching the analysis chunk length the stateless detector scored.
	aggWindowSeconds = 3
	// aggWindowFrames is aggWindowSeconds expressed in hops: ceil(3 s * 16000 Hz
	// / 512) = 94, exactly the hop count of one full 3 s chunk. Aggregating the
	// last aggWindowFrames per-hop probabilities therefore reproduces the old
	// full-chunk window ("speech sustained >= minConsec hops anywhere in the
	// recent ~3 s trips the gate") while each hop is inferred only once: the
	// stateless path re-scored the same 3 s window on every overlapping chunk,
	// the streaming path scores each hop once and lets it stay eligible in the
	// ring for the same ~3 s it would have stayed inside overlapping chunks.
	// Detection coverage is therefore preserved, not weakened.
	aggWindowFrames = (aggWindowSeconds*sampleRate16k + windowSamples - 1) / windowSamples
)

// Streamer scores a continuous single-source audio stream for speech, threading
// LSTM state (h/c) and the 64-sample hop context across calls so each hop is
// inferred exactly once. It carries no ONNX session: the shared SpeechSession is
// passed to Flush, so one session serves every source. NOT safe for concurrent
// use.
type Streamer interface {
	// Append buffers newPCM (16-bit LE mono at sampleRate), audio NOT yet seen
	// for this source. It stores raw bytes; it does not run inference. Returns an
	// error and self-resets on a sampleRate change from the buffered rate.
	Append(newPCM []byte, sampleRate int) error
	// Flush resamples all buffered audio to 16k, frames it into complete 512-hops
	// (prepending the carried 64-sample context, sub-hop remainder retained for
	// next Flush), runs the sequence model over the stacked hops carrying h/c
	// forward, appends the per-hop probs to a rolling window covering the last
	// aggWindowFrames frames, and returns the aggregate speech probability over
	// that window. ok is false when no complete hop was available. framesRun is
	// the number of hops actually inferred (for metrics).
	Flush(sess SpeechSession) (prob float32, ok bool, framesRun int, err error)
	// Reset clears h/c, the sample carry buffer, the hop context, and the rolling
	// window. Call on a source discontinuity (gap, restart, sampleRate change).
	Reset()
}

// streamer is the concrete Streamer implementation.
//
// Carried state between calls:
//   - raw: PCM16 bytes appended since the last Flush (not yet resampled).
//   - rate: the sample rate of raw; 0 until the first Append (or after Reset).
//   - pending: 16 kHz float samples already resampled but shorter than one hop.
//   - ctx: the last contextSamples samples of the most recent inferred hop,
//     prepended as the first row's context of the next Flush.
//   - h/c: the LSTM state returned by the last model run.
//   - ring: the last <= aggWindowFrames per-hop probabilities, oldest first.
type streamer struct {
	minConsec int

	rate    int
	raw     []byte
	pending []float32
	ctx     [contextSamples]float32
	h       [stateWidth]float32
	c       [stateWidth]float32
	ring    []float32
}

// NewStreamer creates a per-source streaming Silero VAD scorer. It owns no ONNX
// session; the shared SpeechSession is supplied to each Flush.
// minConsecutiveFrames <= 0 uses the default sustain requirement.
func NewStreamer(minConsecutiveFrames int) Streamer {
	mc := minConsecutiveFrames
	if mc < 1 {
		mc = defaultMinConsecutiveFrames
	}
	return &streamer{minConsec: mc}
}

// Append implements Streamer.
func (s *streamer) Append(newPCM []byte, sampleRate int) error {
	if len(newPCM) == 0 {
		return nil
	}
	if sampleRate <= 0 {
		return errors.Newf("vad: sample rate must be positive, got %d", sampleRate).
			Component("inference/vad").Category(errors.CategoryValidation).Build()
	}
	if len(newPCM)%bytesPerSample != 0 {
		return errors.Newf("vad: pcm length %d is not a multiple of %d", len(newPCM), bytesPerSample).
			Component("inference/vad").Category(errors.CategoryValidation).Build()
	}
	if s.rate != 0 && sampleRate != s.rate {
		// The stream's rate changed mid-flight: everything carried (state,
		// context, buffered audio) belongs to the old rate, so drop it all and
		// report the discontinuity to the caller.
		old := s.rate
		s.Reset()
		return errors.Newf("vad: sample rate changed from %d to %d; streamer reset", old, sampleRate).
			Component("inference/vad").Category(errors.CategoryValidation).Build()
	}
	s.rate = sampleRate
	s.raw = append(s.raw, newPCM...)
	return nil
}

// Flush implements Streamer.
func (s *streamer) Flush(sess SpeechSession) (prob float32, ok bool, framesRun int, err error) {
	if len(s.raw) > 0 {
		samples, convErr := to16k(s.raw, s.rate)
		if convErr != nil {
			return 0, false, 0, convErr
		}
		s.raw = s.raw[:0]
		s.pending = append(s.pending, samples...)
	}

	n := len(s.pending) / windowSamples
	if n == 0 {
		return 0, false, 0, nil
	}
	emit := s.pending[:n*windowSamples]
	frames := stackFrames(emit, s.ctx[:])

	probs, hOut, cOut, err := sess.Run(frames, s.h[:], s.c[:])
	if err != nil {
		return 0, false, 0, err
	}
	// The returned slices alias session buffers; copy the carried state out
	// before anything else can touch the session.
	copy(s.h[:], hOut)
	copy(s.c[:], cOut)
	copy(s.ctx[:], emit[len(emit)-contextSamples:])

	// Retain the sub-hop remainder for the next Flush.
	rem := copy(s.pending, s.pending[n*windowSamples:])
	s.pending = s.pending[:rem]

	// Append the fresh per-hop probabilities and trim the ring to the last
	// aggWindowFrames, so the aggregate always covers the recent ~3 s.
	s.ring = append(s.ring, probs...)
	if over := len(s.ring) - aggWindowFrames; over > 0 {
		s.ring = s.ring[:copy(s.ring, s.ring[over:])]
	}
	return aggregate(s.ring, s.minConsec), true, n, nil
}

// Reset implements Streamer.
func (s *streamer) Reset() {
	s.rate = 0
	s.raw = s.raw[:0]
	s.pending = s.pending[:0]
	s.ring = s.ring[:0]
	clear(s.ctx[:])
	clear(s.h[:])
	clear(s.c[:])
}
