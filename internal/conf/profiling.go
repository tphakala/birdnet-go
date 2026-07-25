package conf

import "math"

// Recommended sampling rates for the two runtime profilers.
//
// Named Recommended rather than Default because they are deliberately NOT what
// an unset config resolves to: an unset rate means off, and every other
// Default* constant in this package IS the value its key defaults to. These are
// the numbers to reach for when a rate has to be chosen without the user naming
// one, which is what the documentation quotes and what a UI toggle would write.
//
// Go documents rate 1 for the block profiler as the way to "include every
// blocking event", and since the mutex fraction reports one event in N, rate 1
// there records every contention event too. That is the right choice for a
// microbenchmark and the wrong one for a long-running appliance doing real-time
// audio, which is why these constants exist rather than deferring to the
// runtime's idea of a starting point.
const (
	// RecommendedBlockProfileRate samples one blocking event per 10
	// microseconds of blocked time. Coarse enough to be cheap, fine enough that
	// a contended lock or a starved channel still shows up.
	RecommendedBlockProfileRate = 10000

	// RecommendedMutexProfileFraction reports one in 100 contention events. A
	// real contention problem produces far more than 100 events, so the shape
	// of the profile survives the sampling.
	RecommendedMutexProfileFraction = 100

	// maxBlockProfileRate bounds ResolvedBlockRate from above, for correctness
	// only, and is deliberately far coarser than any sane configuration.
	//
	// The runtime converts nanoseconds to cycles as
	// int64(float64(rate) * float64(ticksPerSecond()) / 1e9), and the Go spec
	// leaves an out-of-range float-to-int conversion implementation-defined.
	// amd64 (CVTTSD2SI) yields MinInt64, which blocksampled reads as off, so the
	// profiler silently stops while the log reports the rate as applied. arm64
	// (FCVTZS) saturates to MaxInt64 instead, so it stays armed and merely never
	// samples. Different failure, same root cause, and neither is what the
	// operator asked for; the bound removes the whole class rather than the one
	// architecture's version of it. Overflow needs roughly 9e17 even on an
	// implausible 10GHz clock; 1e15 ns is about 11 days per sample, which is
	// already meaningless, so the clamp cannot reach a rate anyone chose on
	// purpose.
	//
	// It deliberately does NOT enforce a rate worth its cost. Block profiling is
	// dominated by the on/off decision rather than the rate: any non-zero value
	// arms an unconditional cputicks() read at every channel send, channel
	// receive, select and semacquire. Benchmarked on a Raspberry Pi 5, a
	// blocking channel round-trip costs +55% at rate 1 and still +28% at rate
	// 100000, against rate 0. So a very coarse rate does buy most of the cost
	// and little of the signal, and coarseBlockProfileRate exists to say so in
	// the log. Saying so is the right response, not overriding the number: this
	// package already declines to clamp a too-aggressive rate on the grounds
	// that silently overriding what the user typed is worse than the overhead,
	// and the same reasoning applies at the other end.
	//
	// Capped at MaxInt so the package still builds for a 32-bit GOARCH, where
	// the literal does not fit an int. No behaviour is lost there: a 32-bit int
	// cannot hold a rate large enough to overflow the conversion in the first
	// place, so the clamp correctly becomes unreachable rather than absent.
	maxBlockProfileRate = min(1_000_000_000_000_000, math.MaxInt)
)

// ResolvedBlockRate returns the value to hand runtime.SetBlockProfileRate.
//
// Non-positive resolves to 0, which disables block profiling. The runtime
// already treats every value <= 0 as off, so that clamp changes no behaviour;
// it exists so the value logged as applied is the value that took effect.
// The upper clamp is load-bearing: see maxBlockProfileRate.
func (p *ProfilingConfig) ResolvedBlockRate() int {
	switch {
	case p == nil, p.BlockRate <= 0:
		return 0
	case p.BlockRate > maxBlockProfileRate:
		return maxBlockProfileRate
	default:
		return p.BlockRate
	}
}

// ResolvedMutexFraction returns the value to hand
// runtime.SetMutexProfileFraction.
//
// The clamp is load-bearing here, unlike the block-rate one. A negative
// argument tells SetMutexProfileFraction to report the current fraction and
// leave it unchanged, so passing a negative config value straight through would
// silently keep mutex profiling at whatever it already was, which is the
// opposite of what someone writing a negative number into a config file wants.
//
// No upper clamp: the runtime stores the fraction verbatim and samples when
// cheaprand64()%N == 0, so a huge value degrades honestly toward never sampling
// rather than wrapping into a different meaning.
func (p *ProfilingConfig) ResolvedMutexFraction() int {
	if p == nil || p.MutexFraction <= 0 {
		return 0
	}
	return p.MutexFraction
}
