package vad

import (
	ort "github.com/microsoft/onnxruntime/go/onnxruntime"

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
func hcStateShape() []int64 { return []int64{1, 1, stateWidth} }

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
// Everything except the stacked input is bound once through an IOBinding. The
// h and c inputs and the hn and cn outputs are fixed-shape tensors created over
// reused Go buffers (hBuf, cBuf, hOutBuf, cOutBuf); each Run copies the carried
// state into hBuf and cBuf, and ONNX Runtime writes the next state straight into
// hOutBuf and cOutBuf. The speech_probs output has a dynamic length, so it is
// bound over probsBuf[:n] and rebound only when the hop count n changes.
//
// The stacked input has a dynamic first dimension, so each Run creates its
// input tensor over the caller's flat frames slice (no data copy) and binds it
// under the input name. The previous input tensor is closed only after the new
// one is bound, and the new one stays open until the next Run or Close: the
// binding references the OrtValue, which points at the caller's Go memory, and
// closing the tensor would unpin that memory while C still holds the pointer.
// The cost is one retained frames buffer (about 72 KB at 31 hops). The measured
// per-Run cost is one tensor object and one BindInput, 5 Go allocations. A
// session is NOT safe for concurrent use.
type session struct {
	sess    *ort.Session
	binding *ort.IOBinding

	// Reused buffers backing the bound state tensors (mutated per run).
	hBuf    []float32 // input h, len = stateWidth
	cBuf    []float32 // input c, len = stateWidth
	hOutBuf []float32 // output hn, len = stateWidth, returned (aliased) by Run
	cOutBuf []float32 // output cn, len = stateWidth, returned (aliased) by Run

	// probsBuf backs the speech_probs output, grown to the largest sequence
	// length seen; Run returns probsBuf[:n] (aliased).
	probsBuf []float32

	// Bound once at construction.
	hTensor  *ort.Tensor // input h [1, 1, stateWidth]
	cTensor  *ort.Tensor // input c [1, 1, stateWidth]
	hnTensor *ort.Tensor // output hn [1, 1, stateWidth]
	cnTensor *ort.Tensor // output cn [1, 1, stateWidth]

	// Replaced as the stream goes: the stacked input of the latest Run, and the
	// speech_probs output tensor currently bound, which covers boundHops hops.
	inputTensor *ort.Tensor
	probsTensor *ort.Tensor
	boundHops   int
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
	defer func() { _ = sessOpts.Close() }()

	if err = sessOpts.SetIntraOpNumThreads(1); err != nil {
		return nil, errors.New(err).Component("inference/vad").Category(errors.CategoryModelInit).Build()
	}
	if err = sessOpts.SetInterOpNumThreads(1); err != nil {
		return nil, errors.New(err).Component("inference/vad").Category(errors.CategoryModelInit).Build()
	}
	if sessionOptsFn != nil {
		sessionOptsFn(sessOpts)
	}

	var ortSess *ort.Session
	if len(modelData) > 0 {
		ortSess, err = ort.NewSessionFromBytes(modelData, sessOpts)
	} else {
		ortSess, err = ort.NewSession(modelPath, sessOpts)
	}
	if err != nil {
		return nil, errors.New(err).
			Component("inference/vad").
			Category(errors.CategoryModelInit).
			Context("model_path", modelPath).
			Context("from_embedded_data", len(modelData) > 0).
			Build()
	}

	s = &session{
		sess:    ortSess,
		hBuf:    make([]float32, stateWidth),
		cBuf:    make([]float32, stateWidth),
		hOutBuf: make([]float32, stateWidth),
		cOutBuf: make([]float32, stateWidth),
	}

	// Release the binding, tensors and ORT session if the binding setup fails.
	if bindErr := s.bindState(); bindErr != nil {
		_ = s.Close()
		return nil, bindErr
	}
	return s, nil
}

// bindState creates the binding and the fixed-shape h, c, hn and cn tensors and
// binds them under the model's tensor names. On error the caller closes the
// session, which releases whatever was created.
func (s *session) bindState() error {
	var err error
	if s.binding, err = ort.NewIOBinding(s.sess); err != nil {
		return stateAllocErr(err, "io_binding")
	}
	fixed := []struct {
		name   string
		buf    []float32
		tensor **ort.Tensor
		input  bool
	}{
		{vadInputNames[1], s.hBuf, &s.hTensor, true},
		{vadInputNames[2], s.cBuf, &s.cTensor, true},
		{vadOutputNames[1], s.hOutBuf, &s.hnTensor, false},
		{vadOutputNames[2], s.cOutBuf, &s.cnTensor, false},
	}
	for _, f := range fixed {
		if *f.tensor, err = ort.CreateTensor(hcStateShape(), f.buf); err != nil {
			return stateAllocErr(err, f.name)
		}
		if f.input {
			err = s.binding.BindInput(f.name, *f.tensor)
		} else {
			err = s.binding.BindOutput(f.name, *f.tensor)
		}
		if err != nil {
			return stateAllocErr(err, f.name)
		}
	}
	return nil
}

// stateAllocErr wraps a tensor or binding setup error.
func stateAllocErr(err error, tensor string) error {
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

	if bindErr := s.bindInput(frames, n); bindErr != nil {
		return nil, nil, nil, bindErr
	}
	if n != s.boundHops {
		if bindErr := s.bindProbs(n); bindErr != nil {
			return nil, nil, nil, bindErr
		}
	}

	if err = s.binding.Run(nil); err != nil {
		return nil, nil, nil, errors.New(err).
			Component("inference/vad").
			Category(errors.CategoryModelLoad).
			Context("operation", "vad_inference").
			Build()
	}
	return s.probsBuf[:n], s.hOutBuf, s.cOutBuf, nil
}

// bindInput creates the stacked input tensor over frames and binds it, then
// closes the previous one. The new tensor stays open until the next call so
// the memory the binding points at stays pinned (see the session comment).
func (s *session) bindInput(frames []float32, n int) error {
	t, err := ort.CreateTensor([]int64{int64(n), modelInputSamples}, frames)
	if err != nil {
		return errors.New(err).
			Component("inference/vad").Category(errors.CategoryModelLoad).
			Context("tensor", vadInputNames[0]).Build()
	}
	if err := s.binding.BindInput(vadInputNames[0], t); err != nil {
		_ = t.Close()
		return errors.New(err).
			Component("inference/vad").Category(errors.CategoryModelLoad).
			Context("tensor", vadInputNames[0]).Build()
	}
	if s.inputTensor != nil {
		_ = s.inputTensor.Close()
	}
	s.inputTensor = t
	return nil
}

// bindProbs binds the speech_probs output over probsBuf[:n], growing probsBuf
// when needed, then closes the previously bound output tensor. Binding by name
// replaces the earlier binding.
func (s *session) bindProbs(n int) error {
	// A grown buffer replaces probsBuf only once it is bound: until then the
	// bound tensor still points at the old one.
	buf := s.probsBuf
	if len(buf) < n {
		buf = make([]float32, n)
	}
	t, err := ort.CreateTensor([]int64{int64(n)}, buf[:n])
	if err != nil {
		return errors.New(err).
			Component("inference/vad").Category(errors.CategoryModelLoad).
			Context("tensor", vadOutputNames[0]).Build()
	}
	if err := s.binding.BindOutput(vadOutputNames[0], t); err != nil {
		_ = t.Close()
		return errors.New(err).
			Component("inference/vad").Category(errors.CategoryModelLoad).
			Context("tensor", vadOutputNames[0]).Build()
	}
	if s.probsTensor != nil {
		_ = s.probsTensor.Close()
	}
	s.probsTensor = t
	s.probsBuf = buf
	s.boundHops = n
	return nil
}

// Close implements SpeechSession. It is idempotent. It closes the binding
// first, then every tensor it referenced, then the session.
func (s *session) Close() error {
	if s == nil {
		return nil
	}
	_ = s.binding.Close()
	s.binding = nil
	for _, t := range []**ort.Tensor{&s.hTensor, &s.cTensor, &s.hnTensor, &s.cnTensor, &s.inputTensor, &s.probsTensor} {
		if *t != nil {
			_ = (*t).Close()
			*t = nil
		}
	}
	s.boundHops = 0
	if s.sess != nil {
		err := s.sess.Close()
		s.sess = nil
		return err
	}
	return nil
}
