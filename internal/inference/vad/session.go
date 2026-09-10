package vad

import (
	ort "github.com/yalue/onnxruntime_go"

	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/inference"
)

// Silero VAD sequence-model tensor contract (the official
// examples/onnx_sequence export of upstream snakers4/silero-vad, folded to
// 16 kHz; verified with ORT 1.x):
//
//	inputs:  input [sequenceLength, modelInputSamples] float32 (mono PCM in [-1,1])
//	         h [1, 1, stateWidth] float32 (LSTM hidden state; zeros to start)
//	         c [1, 1, stateWidth] float32 (LSTM cell state; zeros to start)
//	outputs: speech_probs [sequenceLength] float32 (one speech probability per hop)
//	         hn [1, 1, stateWidth] float32 (hidden state to carry into the next call)
//	         cn [1, 1, stateWidth] float32 (cell state to carry into the next call)
//
// There is no sr input (the 16 kHz rate is folded into the export), and the
// frame model's combined [2, 1, stateWidth] state is split into h and c. Each
// input row is 64 samples of context from the previous hop PREPENDED to 512 new
// samples, exactly as the frame model expected; feeding bare 512-sample windows
// makes the model emit near-zero probabilities for real speech. One Run
// processes the whole stacked sequence with full internal recurrence (no seams),
// bit-exact to threading the hops one at a time through the upstream recurrent
// frame model.
const (
	// windowSamples is the hop: new 16 kHz samples consumed per VAD frame (32 ms).
	windowSamples = 512
	// contextSamples is the count of previous-hop samples prepended as context.
	contextSamples = 64
	// modelInputSamples is the model's actual input row width (context + window).
	modelInputSamples = contextSamples + windowSamples
	// stateWidth is the trailing dimension of each LSTM state tensor (h and c).
	stateWidth = 128
	// sampleRate16k is the only sample rate this package feeds the model.
	sampleRate16k = 16000
	// StrategySequence names the windowing strategy for logging and the dashboard.
	// The model architecture is fixed to the sequence export, so this is constant.
	StrategySequence = "sequence"
)

// Input/output tensor names, in the order the session binds them.
var (
	vadInputNames  = []string{"input", "h", "c"}
	vadOutputNames = []string{"speech_probs", "hn", "cn"}
)

// hcStateShape is the fixed ONNX shape of each LSTM state tensor (h and c):
// one layer, batch 1, stateWidth values.
func hcStateShape() ort.Shape { return ort.NewShape(1, 1, stateWidth) }

// SpeechSession is a loaded Silero VAD sequence-model ONNX session. One session
// is shared across every per-source Streamer and the stateless Detector: the
// LSTM state is passed in and out of Run per call, so the session itself carries
// no source-specific state and a single ONNX session (and its ORT thread pool)
// serves all sources. It is NOT safe for concurrent use; the caller must
// serialise Run and Close (the processor gate holds one mutex across both).
type SpeechSession interface {
	// Run scores len(frames)/modelInputSamples stacked hops in one model call.
	// frames is row-major [n, modelInputSamples] (each row [context|window] as
	// built by stackFrames); hIn and cIn are the LSTM state (each stateWidth
	// values, zeros to start a stream). It returns one probability per hop
	// (len n) and the LSTM states to carry into the next call (each stateWidth).
	// All three returned slices alias reused session buffers and are only valid
	// until the next Run or Close; callers that carry state must copy them out.
	Run(frames, hIn, cIn []float32) (probs, hOut, cOut []float32, err error)
	// Close releases the ONNX session and its tensors. Idempotent.
	Close() error
	// Strategy returns the windowing strategy name for logging and the dashboard.
	Strategy() string
}

// session wraps a Silero VAD sequence-model ONNX Runtime session.
//
// The fixed-shape h and c input tensors are allocated once and reused across Run
// calls (their backing buffers are mutated in place), keeping the LSTM state
// path zero-alloc. The input tensor's sequence dimension is dynamic, so it is
// created per Run directly over the caller's flat frames slice (no data copy;
// one small tensor-object allocation per Run, roughly once per second per
// source, negligible next to the inference it dispatches, matching the
// resampling path's stated philosophy). Outputs are auto-allocated by ONNX
// Runtime (nil entries passed to DynamicAdvancedSession.Run; verified against
// yalue/onnxruntime_go v1.30.1, whose RunWithOptions replaces each nil entry
// with a Go-managed tensor that this session copies out of and destroys before
// returning). A session is NOT safe for concurrent use.
type session struct {
	sess *ort.DynamicAdvancedSession

	// Reused input buffers backing the h/c tensors (mutated per run).
	hBuf []float32 // len = stateWidth
	cBuf []float32 // len = stateWidth

	hTensor *ort.Tensor[float32] // input h [1, 1, stateWidth], reused
	cTensor *ort.Tensor[float32] // input c [1, 1, stateWidth], reused

	// Reused output copy buffers returned (aliased) by Run.
	probsBuf []float32 // grown to the largest sequence length seen
	hOutBuf  []float32 // len = stateWidth
	cOutBuf  []float32 // len = stateWidth
}

// NewSession initialises the ONNX Runtime library if needed and loads the Silero
// VAD sequence model from cfg (ModelData in memory takes precedence over
// ModelPath). The returned session is shared across Streamers and Detectors.
//
//nolint:gocritic // hugeParam: Config is a public constructor argument; value semantics are intentional.
func NewSession(cfg Config) (SpeechSession, error) {
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
	return newSession(cfg.ModelPath, cfg.ModelData, nil)
}

// newSession creates a Silero VAD sequence session. The model is loaded from
// modelData when it is non-empty, otherwise from modelPath. The ONNX Runtime
// library must already be initialised (inference.InitONNXRuntime).
func newSession(modelPath string, modelData []byte, sessionOptsFn func(*ort.SessionOptions)) (s *session, err error) {
	sessOpts, err := ort.NewSessionOptions()
	if err != nil {
		return nil, errors.New(err).
			Component("inference/vad").
			Category(errors.CategoryModelInit).
			Context("stage", "session_options").
			Build()
	}
	defer func() { _ = sessOpts.Destroy() }()

	if err = sessOpts.SetIntraOpNumThreads(1); err != nil {
		return nil, errors.New(err).Component("inference/vad").Category(errors.CategoryModelInit).Build()
	}
	if err = sessOpts.SetInterOpNumThreads(1); err != nil {
		return nil, errors.New(err).Component("inference/vad").Category(errors.CategoryModelInit).Build()
	}
	if sessionOptsFn != nil {
		sessionOptsFn(sessOpts)
	}

	var ortSess *ort.DynamicAdvancedSession
	if len(modelData) > 0 {
		ortSess, err = ort.NewDynamicAdvancedSessionWithONNXData(modelData, vadInputNames, vadOutputNames, sessOpts)
	} else {
		ortSess, err = ort.NewDynamicAdvancedSession(modelPath, vadInputNames, vadOutputNames, sessOpts)
	}
	if err != nil {
		return nil, errors.New(err).
			Component("inference/vad").
			Category(errors.CategoryModelInit).
			Context("model_path", modelPath).
			Context("from_embedded_data", len(modelData) > 0).
			Build()
	}

	// From here on, clean up the ORT session if tensor allocation fails.
	defer func() {
		if err != nil && ortSess != nil {
			_ = ortSess.Destroy()
		}
	}()

	s = &session{
		sess:    ortSess,
		hBuf:    make([]float32, stateWidth),
		cBuf:    make([]float32, stateWidth),
		hOutBuf: make([]float32, stateWidth),
		cOutBuf: make([]float32, stateWidth),
	}

	s.hTensor, err = ort.NewTensor(hcStateShape(), s.hBuf)
	if err != nil {
		return nil, s.wrapAllocErr(err, "h")
	}
	s.cTensor, err = ort.NewTensor(hcStateShape(), s.cBuf)
	if err != nil {
		return nil, s.wrapAllocErr(err, "c")
	}
	return s, nil
}

// wrapAllocErr destroys any partially-allocated tensors and wraps the error.
func (s *session) wrapAllocErr(err error, tensor string) error {
	s.destroyTensors()
	return errors.New(err).
		Component("inference/vad").
		Category(errors.CategoryModelInit).
		Context("tensor", tensor).
		Build()
}

// Strategy implements SpeechSession.
func (s *session) Strategy() string { return StrategySequence }

// Run implements SpeechSession; see the interface doc for the contract.
func (s *session) Run(frames, hIn, cIn []float32) (probs, hOut, cOut []float32, err error) {
	if s.sess == nil {
		return nil, nil, nil, ErrSessionClosed
	}
	if len(frames) == 0 || len(frames)%modelInputSamples != 0 {
		return nil, nil, nil, errors.Newf("vad: frames length %d, want a positive multiple of %d", len(frames), modelInputSamples).
			Component("inference/vad").Category(errors.CategoryValidation).Build()
	}
	if len(hIn) != stateWidth || len(cIn) != stateWidth {
		return nil, nil, nil, errors.Newf("vad: state lengths h=%d c=%d, want %d", len(hIn), len(cIn), stateWidth).
			Component("inference/vad").Category(errors.CategoryValidation).Build()
	}
	n := len(frames) / modelInputSamples

	copy(s.hBuf, hIn)
	copy(s.cBuf, cIn)

	// The sequence dimension is dynamic, so the input tensor is created per Run
	// over the caller's flat slice (NewTensor wraps the Go slice; no data copy).
	inputTensor, err := ort.NewTensor(ort.NewShape(int64(n), modelInputSamples), frames)
	if err != nil {
		return nil, nil, nil, errors.New(err).
			Component("inference/vad").Category(errors.CategoryModelLoad).
			Context("tensor", "input").Build()
	}
	defer func() { _ = inputTensor.Destroy() }()

	// nil entries ask ONNX Runtime to allocate the outputs; Run replaces them
	// with Go-managed tensors that must be destroyed after copying out. The defer
	// is registered BEFORE Run so a partially-allocated output set cannot leak on
	// an inference error.
	outputs := make([]ort.Value, len(vadOutputNames))
	defer destroyValues(outputs)
	if err = s.sess.Run([]ort.Value{inputTensor, s.hTensor, s.cTensor}, outputs); err != nil {
		return nil, nil, nil, errors.New(err).
			Component("inference/vad").
			Category(errors.CategoryModelLoad).
			Context("operation", "vad_inference").
			Build()
	}

	probsT, ok1 := outputs[0].(*ort.Tensor[float32])
	hnT, ok2 := outputs[1].(*ort.Tensor[float32])
	cnT, ok3 := outputs[2].(*ort.Tensor[float32])
	if !ok1 || !ok2 || !ok3 {
		return nil, nil, nil, errors.Newf("vad: unexpected output tensor types (probs=%T hn=%T cn=%T)", outputs[0], outputs[1], outputs[2]).
			Component("inference/vad").Category(errors.CategoryModelLoad).Build()
	}
	got := probsT.GetData()
	hn := hnT.GetData()
	cn := cnT.GetData()
	if len(got) != n || len(hn) != stateWidth || len(cn) != stateWidth {
		return nil, nil, nil, errors.Newf("vad: output shapes probs=%d hn=%d cn=%d, want %d/%d/%d", len(got), len(hn), len(cn), n, stateWidth, stateWidth).
			Component("inference/vad").Category(errors.CategoryModelLoad).Build()
	}

	if cap(s.probsBuf) < n {
		s.probsBuf = make([]float32, n)
	}
	s.probsBuf = s.probsBuf[:n]
	copy(s.probsBuf, got)
	copy(s.hOutBuf, hn)
	copy(s.cOutBuf, cn)
	return s.probsBuf, s.hOutBuf, s.cOutBuf, nil
}

// destroyValues destroys every non-nil ORT value in vals.
func destroyValues(vals []ort.Value) {
	for _, v := range vals {
		if v != nil {
			_ = v.Destroy()
		}
	}
}

// Close implements SpeechSession. It is idempotent.
func (s *session) Close() error {
	if s == nil {
		return nil
	}
	s.destroyTensors()
	if s.sess != nil {
		err := s.sess.Destroy()
		s.sess = nil
		return err
	}
	return nil
}

// destroyTensors destroys the reused h/c input tensors and nils the references.
// Per-Run tensors (input and auto-allocated outputs) are destroyed in Run.
func (s *session) destroyTensors() {
	if s.hTensor != nil {
		_ = s.hTensor.Destroy()
		s.hTensor = nil
	}
	if s.cTensor != nil {
		_ = s.cTensor.Destroy()
		s.cTensor = nil
	}
}
