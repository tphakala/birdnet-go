package vad

// strategy turns 16 kHz mono float32 samples into one speech probability per
// windowSamples-sized hop. Implementations differ only in how the LSTM state
// and the 64-sample context are threaded across hops.
type strategy interface {
	// run returns one probability per hop covering samples.
	run(s *session, samples []float32) ([]float32, error)
	// name identifies the strategy for logging and benchmarks.
	name() string
	// batch is the ONNX session batch size this strategy requires.
	batch() int
}

// numFrames returns how many windowSamples-sized hops cover n samples, counting
// a final partial hop (zero-padded) so trailing audio is not dropped. Returns 0
// for n <= 0.
func numFrames(n int) int {
	if n <= 0 {
		return 0
	}
	return (n + windowSamples - 1) / windowSamples
}

// recurrentStrategy runs hops sequentially at batch size 1, threading both the
// LSTM state and the 64-sample context from each hop to the next exactly as
// Silero intends. It is the safest, most faithful strategy and the default.
type recurrentStrategy struct{}

func (recurrentStrategy) name() string { return "recurrent" }
func (recurrentStrategy) batch() int   { return 1 }

func (recurrentStrategy) run(s *session, samples []float32) ([]float32, error) {
	frames := numFrames(len(samples))
	if frames == 0 {
		return nil, nil
	}

	probs := make([]float32, 0, frames)
	// input layout: [context(contextSamples) | window(windowSamples)].
	input := make([]float32, modelInputSamples)
	state := make([]float32, stateDepth*stateWidth) // batch 1, starts at zeros

	for f := range frames {
		// Preserve the context prefix (previous hop's tail, zero for the first
		// hop); clear and refill only the window region so a short final hop is
		// zero-padded.
		clear(input[contextSamples:])
		start := f * windowSamples
		end := min(start+windowSamples, len(samples))
		copy(input[contextSamples:], samples[start:end])

		out, newState, err := s.run(input, state)
		if err != nil {
			return nil, err
		}
		probs = append(probs, out[0])
		copy(state, newState)

		// Next context = the last contextSamples of the window just processed.
		copy(input[:contextSamples], input[modelInputSamples-contextSamples:])
	}
	return probs, nil
}

// segmentBatchedStrategy splits the chunk into `segments` contiguous time
// segments and processes them as one batch, advancing every segment by one hop
// per ONNX call. Both the LSTM state and the 64-sample context are threaded
// per lane, so recurrence is preserved WITHIN each segment; only the
// cross-segment context at the seams is lost. This cuts the ONNX call count
// from ~frames to ~frames/segments.
//
// EXPERIMENTAL / not the default. Each segment restarts from a zeroed LSTM state
// and zeroed context, so the model needs ~100 ms to warm up at every seam. A
// short speech burst that falls in a segment's warmup window, or straddles a
// seam, can score falsely low. recurrentStrategy has no such blind spot and is
// the default; do NOT make this the default without the birdsong/edge-case
// false-positive-and-recall validation described in the tracking issue.
type segmentBatchedStrategy struct{ segments int }

func (st segmentBatchedStrategy) name() string { return "segment-batched" }
func (st segmentBatchedStrategy) batch() int   { return st.segments }

func (st segmentBatchedStrategy) run(s *session, samples []float32) ([]float32, error) {
	total := numFrames(len(samples))
	if total == 0 {
		return nil, nil
	}

	b := max(st.segments, 1)
	// Hops per lane, rounded up; the final lane may be shorter and any lane that
	// runs past `total` simply contributes ignored zero-padded hops.
	perLane := (total + b - 1) / b

	probs := make([]float32, total)
	input := make([]float32, b*modelInputSamples)     // per-lane [context|window]
	state := make([]float32, stateDepth*b*stateWidth) // per-lane state, starts at zeros

	for t := range perLane {
		for lane := range b {
			base := lane * modelInputSamples
			clear(input[base+contextSamples : base+modelInputSamples])
			absFrame := lane*perLane + t
			if absFrame >= total {
				continue
			}
			start := absFrame * windowSamples
			end := min(start+windowSamples, len(samples))
			copy(input[base+contextSamples:base+modelInputSamples], samples[start:end])
		}

		out, newState, err := s.run(input, state)
		if err != nil {
			return nil, err
		}

		for lane := range b {
			base := lane * modelInputSamples
			absFrame := lane*perLane + t
			if absFrame < total {
				probs[absFrame] = out[lane]
			}
			// Next context for this lane = last contextSamples of its window.
			copy(input[base:base+contextSamples], input[base+modelInputSamples-contextSamples:base+modelInputSamples])
		}
		copy(state, newState)
	}
	return probs, nil
}
