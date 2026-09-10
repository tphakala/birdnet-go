package vad

// frameCount returns how many windowSamples-sized hops cover n samples, counting
// a final partial hop (zero-padded) so trailing audio is not dropped. Returns 0
// for n <= 0.
func frameCount(n int) int {
	if n <= 0 {
		return 0
	}
	return (n + windowSamples - 1) / windowSamples
}

// stackFrames lays 16 kHz mono samples out as the flat row-major
// [n, modelInputSamples] sequence the Silero sequence model consumes. Each row
// is [context(contextSamples) | window(windowSamples)]: the context of row f>0
// is the previous hop's last contextSamples samples, and row 0's context is
// prevContext (the tail of the last hop inferred before this call). A nil or
// empty prevContext means zeros (a fresh stream or a stateless one-shot). When
// non-empty, prevContext must hold exactly contextSamples samples. A short
// final hop's window is zero padded.
//
// The returned buffer is freshly allocated and owned by the caller. It returns
// nil when samples is empty.
func stackFrames(samples, prevContext []float32) []float32 {
	n := frameCount(len(samples))
	if n == 0 {
		return nil
	}
	frames := make([]float32, n*modelInputSamples)
	for f := range n {
		row := frames[f*modelInputSamples : (f+1)*modelInputSamples]
		start := f * windowSamples
		if f == 0 {
			copy(row[:contextSamples], prevContext)
		} else {
			copy(row[:contextSamples], samples[start-contextSamples:start])
		}
		end := min(start+windowSamples, len(samples))
		copy(row[contextSamples:], samples[start:end])
	}
	return frames
}
