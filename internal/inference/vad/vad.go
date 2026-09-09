// Package vad provides a standalone Silero voice-activity-detection (VAD)
// speech classifier used to gate BirdNET-Go's privacy filter.
//
// It runs the Silero VAD sequence ONNX model and reports an aggregated speech
// probability in [0,1]. It detects speech PRESENCE only, not content or speaker
// identity, which is exactly the privacy-friendly property the filter needs:
// nothing is transcribed or identified, the caller only decides whether to drop
// the chunk's detections.
//
// Two entry points share the same SpeechSession. Detector is a stateless
// one-shot scorer: it frames a whole chunk into one stacked sequence, runs it
// through the model in a single call with zeroed LSTM state, and aggregates the
// per-hop probabilities (the sequence-model equivalent of the original per-hop
// recurrent path, bit-exact to it). Streamer (see streamer.go) scores a
// continuous per-source stream incrementally, carrying LSTM state and hop
// context across calls so each hop of audio is inferred exactly once. A single
// SpeechSession is shared across all Streamers because the LSTM state is passed
// through Run rather than held in the session.
//
// The detector is deliberately decoupled from the multi-model classifier
// orchestrator: it owns its own ONNX Runtime session and is invoked directly
// from the processor's privacy path, independent of what the bird model ranked
// (and of the top-K truncation that makes the label-based path unreliable).
//
// A Detector, a Streamer, and a SpeechSession are each NOT safe for concurrent
// use; callers must serialise access.
package vad

import (
	"github.com/tphakala/birdnet-go/internal/errors"
)

// Defaults for detector and streamer configuration.
const (
	// SampleRate is the native sample rate of the Silero VAD model (16 kHz).
	SampleRate = sampleRate16k

	// defaultMinConsecutiveFrames is how many consecutive 32 ms frames must stay
	// above a level for that level to count, suppressing single-frame transients
	// (~96 ms of sustained speech).
	defaultMinConsecutiveFrames = 3
)

// Sentinel errors.
var (
	// ErrSessionClosed is returned when a session or streamer is used after Close.
	ErrSessionClosed = errors.NewStd("vad: session is closed")
	// ErrModelPathRequired is returned when a session is built without a model.
	ErrModelPathRequired = errors.NewStd("vad: model path is required")
)

// zeroLSTMState is a shared read-only zero LSTM state for the stateless one-shot
// Detector path. Run copies hIn/cIn into its own buffers and never mutates them,
// so a single shared array is safe to pass on every call, avoiding a per-call
// allocation.
var zeroLSTMState [stateWidth]float32

// EmbeddedModelData returns the embedded Silero VAD model bytes, or nil if the
// binary was built without embedded models (-tags noembed).
func EmbeddedModelData() []byte { return embeddedModel }

// HasEmbeddedModel reports whether an embedded VAD model is available in this build.
func HasEmbeddedModel() bool { return len(embeddedModel) > 0 }

// Config configures a Silero VAD session, detector, or streamer.
//
// Provide the model as either ModelData (in-memory, e.g. the embedded model) or
// ModelPath (a file). ModelData takes precedence; at least one is required.
type Config struct {
	// ModelData is the ONNX model bytes to load in memory. Takes precedence over
	// ModelPath. Use EmbeddedModelData() for the built-in model.
	ModelData []byte
	// ModelPath is the path to a silero sequence-export .onnx file. Used when
	// ModelData is empty.
	ModelPath string
	// LibraryPath overrides the ONNX Runtime shared library location. Empty uses
	// the runtime's auto-detected library.
	LibraryPath string
	// MinConsecutiveFrames is the sustain requirement for the aggregator. <= 0
	// uses the default.
	MinConsecutiveFrames int
}

// resolveMinConsec returns the effective sustain requirement for cfg.
func resolveMinConsec(cfg *Config) int {
	if cfg.MinConsecutiveFrames < 1 {
		return defaultMinConsecutiveFrames
	}
	return cfg.MinConsecutiveFrames
}

// Detector scores one audio chunk for speech presence.
type Detector interface {
	// SpeechProbability returns the aggregated speech probability in [0,1] for
	// one chunk of 16-bit little-endian mono PCM at sampleRate Hz. The chunk is
	// resampled to 16 kHz internally. An empty chunk returns 0 without error.
	SpeechProbability(pcm []byte, sampleRate int) (float32, error)
	// Strategy returns the active windowing strategy name (for logging/metrics).
	Strategy() string
	// Close releases the ONNX session. Idempotent.
	Close() error
}

// detector is the concrete Detector implementation.
type detector struct {
	sess      SpeechSession
	minConsec int
}

// New creates a stateless one-shot Silero VAD detector. It initialises the ONNX
// Runtime library if it is not already initialised.
//
//nolint:gocritic // hugeParam: Config is a public constructor argument; value semantics are intentional.
func New(cfg Config) (Detector, error) {
	sess, err := NewSession(cfg)
	if err != nil {
		return nil, err
	}
	return &detector{sess: sess, minConsec: resolveMinConsec(&cfg)}, nil
}

// SpeechProbability implements Detector.
func (d *detector) SpeechProbability(pcm []byte, sampleRate int) (float32, error) {
	if d.sess == nil {
		return 0, ErrSessionClosed
	}

	samples, err := to16k(pcm, sampleRate)
	if err != nil {
		return 0, err
	}
	if len(samples) == 0 {
		return 0, nil
	}

	frames := stackFrames(samples, nil)
	// Zeroed LSTM state for a stateless one-shot. The same shared zero array backs
	// both h and c on every call because Run only reads hIn/cIn (copying each into
	// its own buffer), never mutating them.
	probs, _, _, err := d.sess.Run(frames, zeroLSTMState[:], zeroLSTMState[:])
	if err != nil {
		return 0, err
	}
	return aggregate(probs, d.minConsec), nil
}

// Strategy implements Detector.
func (d *detector) Strategy() string { return StrategySequence }

// Close implements Detector.
func (d *detector) Close() error {
	if d.sess == nil {
		return nil
	}
	err := d.sess.Close()
	d.sess = nil
	return err
}
