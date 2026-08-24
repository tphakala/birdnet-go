package vad

import (
	ort "github.com/yalue/onnxruntime_go"

	"github.com/tphakala/birdnet-go/internal/errors"
)

// Silero VAD tensor contract (upstream v5/v6.2.1, verified with ORT 1.27):
//
//	inputs:  input [B, modelInputSamples] float32 (mono PCM in [-1,1])
//	         state [2, B, stateWidth] float32 (LSTM state; zeros to start)
//	         sr    int64 scalar (16000 or 8000)
//	outputs: output [B, 1] float32 (speech probability in [0,1])
//	         stateN [2, B, stateWidth] float32 (state to carry into the next call)
//
// Silero processes 512-sample hops at 16 kHz but each model call is fed 64
// samples of context from the previous hop PREPENDED to the 512 new samples,
// so the model input width is 576. Feeding a bare 512-sample window (without
// the context) is silently accepted by ONNX Runtime but makes the model emit
// near-zero probabilities for real speech. Both the LSTM state and the 64-sample
// context are threaded across hops.
const (
	// windowSamples is the hop: new 16 kHz samples consumed per VAD frame (32 ms).
	windowSamples = 512
	// contextSamples is the count of previous-hop samples prepended as context.
	contextSamples = 64
	// modelInputSamples is the model's actual input width (context + window).
	modelInputSamples = contextSamples + windowSamples
	// stateDepth is the leading dimension of the LSTM state tensor.
	stateDepth = 2
	// stateWidth is the trailing dimension of the LSTM state tensor.
	stateWidth = 128
	// sampleRate16k is the only sample rate this package feeds the model.
	sampleRate16k = 16000
)

// Input/output tensor names, in the order the session binds them.
var (
	vadInputNames  = []string{"input", "state", "sr"}
	vadOutputNames = []string{"output", "stateN"}
)

// session wraps a Silero VAD ONNX Runtime session for a fixed batch size.
//
// It pre-allocates every input and output tensor once and reuses them across
// run calls, so steady-state inference performs no per-call heap allocation.
// The backing buffers for the input and state tensors are mutated in place
// between runs. A session is NOT safe for concurrent use.
type session struct {
	sess  *ort.DynamicAdvancedSession
	batch int

	// Reused input buffers (wrapped by the input tensors, mutated per run).
	inputBuf []float32 // len = batch * frameSamples
	stateBuf []float32 // len = stateDepth * batch * stateWidth

	inputTensor *ort.Tensor[float32]
	stateTensor *ort.Tensor[float32]
	srScalar    *ort.Scalar[int64]

	probTensor  *ort.Tensor[float32] // output [batch, 1]
	stateOutput *ort.Tensor[float32] // output [stateDepth, batch, stateWidth]

	inputs  []ort.Value
	outputs []ort.Value
}

// newSession creates a Silero VAD session for the given batch size. The model
// is loaded from modelData when it is non-empty, otherwise from modelPath.
// The ONNX Runtime library must already be initialised (inference.InitONNXRuntime).
func newSession(modelPath string, modelData []byte, batch int, sessionOptsFn func(*ort.SessionOptions)) (s *session, err error) {
	if batch < 1 {
		return nil, errors.Newf("vad: batch size must be >= 1, got %d", batch).
			Component("inference/vad").
			Category(errors.CategoryValidation).
			Build()
	}

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
		sess:     ortSess,
		batch:    batch,
		inputBuf: make([]float32, batch*modelInputSamples),
		stateBuf: make([]float32, stateDepth*batch*stateWidth),
	}

	s.inputTensor, err = ort.NewTensor(ort.NewShape(int64(batch), modelInputSamples), s.inputBuf)
	if err != nil {
		return nil, s.wrapAllocErr(err, "input")
	}
	s.stateTensor, err = ort.NewTensor(ort.NewShape(stateDepth, int64(batch), stateWidth), s.stateBuf)
	if err != nil {
		return nil, s.wrapAllocErr(err, "state")
	}
	// sr is a rank-0 scalar (the model declares its dims as []). NewScalar builds
	// a true rank-0 tensor; the length-1 vector form (NewShape(1)) is rejected by
	// the model's shape expectations for a scalar input.
	s.srScalar, err = ort.NewScalar[int64](sampleRate16k)
	if err != nil {
		return nil, s.wrapAllocErr(err, "sr")
	}
	s.probTensor, err = ort.NewEmptyTensor[float32](ort.NewShape(int64(batch), 1))
	if err != nil {
		return nil, s.wrapAllocErr(err, "output")
	}
	s.stateOutput, err = ort.NewEmptyTensor[float32](ort.NewShape(stateDepth, int64(batch), stateWidth))
	if err != nil {
		return nil, s.wrapAllocErr(err, "stateN")
	}

	s.inputs = []ort.Value{s.inputTensor, s.stateTensor, s.srScalar}
	s.outputs = []ort.Value{s.probTensor, s.stateOutput}
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

// run performs one inference step for all batch lanes.
//
// input must hold batch*frameSamples float32 samples (lane-major) and stateIn
// must hold stateDepth*batch*stateWidth float32 values. run copies both into
// the session's reused input buffers, executes the model, and returns the
// per-lane probabilities (len batch) and the next state (len stateDepth*batch*
// stateWidth). Both returned slices alias internal buffers and are only valid
// until the next call to run or close.
func (s *session) run(input, stateIn []float32) (probs, stateOut []float32, err error) {
	if s.sess == nil {
		return nil, nil, ErrSessionClosed
	}
	if len(input) != len(s.inputBuf) {
		return nil, nil, errors.Newf("vad: input length %d, want %d", len(input), len(s.inputBuf)).
			Component("inference/vad").Category(errors.CategoryValidation).Build()
	}
	if len(stateIn) != len(s.stateBuf) {
		return nil, nil, errors.Newf("vad: state length %d, want %d", len(stateIn), len(s.stateBuf)).
			Component("inference/vad").Category(errors.CategoryValidation).Build()
	}

	copy(s.inputBuf, input)
	copy(s.stateBuf, stateIn)

	if err = s.sess.Run(s.inputs, s.outputs); err != nil {
		return nil, nil, errors.New(err).
			Component("inference/vad").
			Category(errors.CategoryModelLoad).
			Context("operation", "vad_inference").
			Build()
	}

	return s.probTensor.GetData(), s.stateOutput.GetData(), nil
}

// close releases the ONNX session and all tensors. It is idempotent.
func (s *session) close() error {
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

// destroyTensors destroys any allocated tensors and nils the references.
func (s *session) destroyTensors() {
	if s.inputTensor != nil {
		_ = s.inputTensor.Destroy()
		s.inputTensor = nil
	}
	if s.stateTensor != nil {
		_ = s.stateTensor.Destroy()
		s.stateTensor = nil
	}
	if s.srScalar != nil {
		_ = s.srScalar.Destroy()
		s.srScalar = nil
	}
	if s.probTensor != nil {
		_ = s.probTensor.Destroy()
		s.probTensor = nil
	}
	if s.stateOutput != nil {
		_ = s.stateOutput.Destroy()
		s.stateOutput = nil
	}
}
