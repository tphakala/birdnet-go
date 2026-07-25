package conf

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

	// maxBlockProfileRate bounds ResolvedBlockRate from above, at 10ms.
	//
	// Two reasons, and the second is why the bound is this low rather than
	// merely finite.
	//
	// First, correctness: the runtime converts nanoseconds to cycles as
	// int64(float64(rate) * float64(ticksPerSecond()) / 1e9), which on a ~3GHz
	// TSC overflows int64 above roughly 3e18 and wraps negative, and
	// blocksampled reads a negative rate as "off". A large enough rate would
	// therefore disable the profiler while the log reported it as applied.
	//
	// Second, and the reason for 10ms specifically: the cost of block profiling
	// is dominated by the on/off decision rather than by the rate. Any non-zero
	// rate arms an unconditional cputicks() read at every channel send, channel
	// receive, select and semacquire. Benchmarked on a Raspberry Pi 5, a
	// blocking channel round-trip costs +55% at rate 1 and still +28% at rate
	// 100000, against rate 0. So a very coarse rate buys most of the cost and
	// almost none of the signal. At 10ms the profiler still records every block
	// long enough to matter on an audio path, which keeps the accepted range one
	// where the profile is worth what it costs. Zero remains the only free
	// setting, and the field documentation says so.
	maxBlockProfileRate = 10_000_000
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
