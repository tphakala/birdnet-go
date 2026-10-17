package vad

import (
	"github.com/tphakala/birdnet-go/internal/errors"
)

// Rolling-aggregation window for the streaming path.
const (
	// aggWindowSeconds is the span of recent audio the rolling window keeps
	// between flushes. It does not need to span a whole model clip: the gate
	// stamps each speech hit with the end of the audio it scored, and every hop
	// is fresh in exactly one Flush, so a hop's hit is recorded by the Flush
	// that inferred it. A single Flush that infers more than this span is
	// scored over all of its new hops (see Flush).
	aggWindowSeconds = 3
	// aggWindowFrames is aggWindowSeconds expressed in hops: ceil(3 s * 16000 Hz
	// / 512) = 94. Between Flush calls the ring holds the last aggWindowFrames
	// per-hop probabilities, so a hop stays eligible for the aggregate for about
	// 3 s while being inferred only once. When one Flush infers more hops than
	// aggWindowFrames (a step longer than ~3 s, as with 5 s models), the
	// aggregate instead spans every new hop plus the minConsec-1 hops before
	// them, so no inferred hop goes unscored and speech straddling the flush
	// boundary still counts.
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
	// forward, and returns the aggregate speech probability over every newly
	// inferred hop plus the prior hops that fill aggWindowFrames, or the last
	// minConsec-1 prior hops when that is more, so a run straddling the flush
	// boundary counts. The rolling window is then trimmed to the last
	// aggWindowFrames hops for the next Flush. The window need not span a whole
	// model clip, because the caller stamps hits with the end of the scored
	// audio and each hop is inferred in exactly one Flush. ok is false when no complete hop
	// was available. framesRun is the number of hops actually inferred (for
	// metrics).
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

	// Aggregate before trimming so every newly inferred hop is scored, even when
	// one Flush yields more than aggWindowFrames hops. The prior ring is first
	// cut to whichever is larger: the hops that fill the window to
	// aggWindowFrames, or the minConsec-1 hops that let speech straddling the
	// flush boundary reach the sustain length. After aggregating, the ring is
	// trimmed back to aggWindowFrames for the next Flush.
	keepPrior := max(aggWindowFrames-len(probs), s.minConsec-1, 0)
	s.ring = trimToLast(s.ring, keepPrior)
	s.ring = append(s.ring, probs...)
	res := aggregate(s.ring, s.minConsec)
	s.ring = trimToLast(s.ring, aggWindowFrames)
	return res, true, n, nil
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

// trimToLast keeps the last keep elements of ring, moving them to the front in
// place (the backing array is reused). It returns ring unchanged when it holds
// keep elements or fewer.
func trimToLast(ring []float32, keep int) []float32 {
	if len(ring) <= keep {
		return ring
	}
	return ring[:copy(ring, ring[len(ring)-keep:])]
}
