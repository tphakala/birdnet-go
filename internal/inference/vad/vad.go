// Package vad provides a standalone Silero voice-activity-detection (VAD)
// speech classifier used to gate BirdNET-Go's privacy filter.
//
// It runs the Silero VAD ONNX model on a single analysis chunk and reports an
// aggregated speech probability in [0,1]. It detects speech PRESENCE only, not
// content or speaker identity, which is exactly the privacy-friendly property
// the filter needs: nothing is transcribed or identified, the caller only
// decides whether to drop the chunk's detections.
//
// The detector is deliberately decoupled from the multi-model classifier
// orchestrator: it owns its own ONNX Runtime session and is invoked directly
// from the processor's privacy path, independent of what the bird model ranked
// (and of the top-K truncation that makes the label-based path unreliable).
//
// A Detector is NOT safe for concurrent use; callers must serialise access.
package vad

import (
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/inference"
)

// Defaults for detector configuration.
const (
	// defaultMinConsecutiveFrames is how many consecutive 32 ms frames must
	// stay above a level for that level to count, suppressing single-frame
	// transients (~96 ms of sustained speech).
	defaultMinConsecutiveFrames = 3
	// defaultSegments is the batch size (number of contiguous time segments)
	// for the segment-batched strategy.
	defaultSegments = 6
)

// Sentinel errors.
var (
	// ErrSessionClosed is returned when a detector is used after Close.
	ErrSessionClosed = errors.NewStd("vad: session is closed")
	// ErrModelPathRequired is returned when New is called without a model path.
	ErrModelPathRequired = errors.NewStd("vad: model path is required")
)

// StrategyKind selects how the LSTM state is threaded across a chunk's frames.
type StrategyKind int

const (
	// StrategyRecurrent runs frames sequentially at batch size 1, threading
	// the LSTM state exactly as Silero intends. Most faithful; the safe default.
	StrategyRecurrent StrategyKind = iota
	// StrategySegmentBatched splits the chunk into contiguous time segments and
	// batches them, preserving recurrence within each segment. Fewer ORT calls
	// per chunk at the cost of dropping cross-segment context at the seams.
	StrategySegmentBatched
)

// EmbeddedModelData returns the embedded Silero VAD model bytes, or nil if the
// binary was built without embedded models (-tags noembed).
func EmbeddedModelData() []byte { return embeddedModel }

// HasEmbeddedModel reports whether an embedded VAD model is available in this build.
func HasEmbeddedModel() bool { return len(embeddedModel) > 0 }

// Config configures a Silero VAD detector.
//
// Provide the model as either ModelData (in-memory, e.g. the embedded model) or
// ModelPath (a file). ModelData takes precedence; at least one is required.
type Config struct {
	// ModelData is the ONNX model bytes to load in memory. Takes precedence over
	// ModelPath. Use EmbeddedModelData() for the built-in model.
	ModelData []byte
	// ModelPath is the path to a silero .onnx file. Used when ModelData is empty.
	ModelPath string
	// LibraryPath overrides the ONNX Runtime shared library location.
	// Empty uses the runtime's auto-detected library.
	LibraryPath string
	// Strategy selects the windowing strategy. Defaults to StrategyRecurrent.
	Strategy StrategyKind
	// Segments sets the batch size for StrategySegmentBatched. <= 0 uses the
	// default. Ignored by StrategyRecurrent.
	Segments int
	// MinConsecutiveFrames is the sustain requirement for the aggregator.
	// <= 0 uses the default.
	MinConsecutiveFrames int
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
	sess      *session
	strategy  strategy
	minConsec int
}

// New creates a Silero VAD detector from an installed model file.
// It initialises the ONNX Runtime library if it is not already initialised.
//
//nolint:gocritic // hugeParam: Config is a public constructor argument; value semantics are intentional.
func New(cfg Config) (Detector, error) {
	if cfg.ModelPath == "" && len(cfg.ModelData) == 0 {
		return nil, ErrModelPathRequired
	}

	if err := inference.InitONNXRuntime(cfg.LibraryPath); err != nil {
		return nil, errors.New(err).
			Component("inference/vad").
			Category(errors.CategoryModelInit).
			Context("stage", "ort_init").
			Build()
	}

	st := newStrategy(&cfg)

	sess, err := newSession(cfg.ModelPath, cfg.ModelData, st.batch(), nil)
	if err != nil {
		return nil, err
	}

	minConsec := cfg.MinConsecutiveFrames
	if minConsec < 1 {
		minConsec = defaultMinConsecutiveFrames
	}

	return &detector{sess: sess, strategy: st, minConsec: minConsec}, nil
}

// newStrategy builds the windowing strategy selected by cfg.
func newStrategy(cfg *Config) strategy {
	if cfg.Strategy == StrategySegmentBatched {
		segs := cfg.Segments
		if segs < 1 {
			segs = defaultSegments
		}
		return segmentBatchedStrategy{segments: segs}
	}
	return recurrentStrategy{}
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

	probs, err := d.strategy.run(d.sess, samples)
	if err != nil {
		return 0, err
	}

	return aggregate(probs, d.minConsec), nil
}

// Strategy implements Detector.
func (d *detector) Strategy() string { return d.strategy.name() }

// Close implements Detector.
func (d *detector) Close() error {
	if d.sess == nil {
		return nil
	}
	err := d.sess.close()
	d.sess = nil
	return err
}
