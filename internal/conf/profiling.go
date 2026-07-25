package conf

// Recommended sampling rates for the two runtime profilers.
//
// These are the values to reach for when a rate has to be chosen without the
// user naming one: the documentation quotes them, and a UI toggle would write
// them. They deliberately are NOT what an unset config resolves to, because an
// unset rate means "off" and opening a profile endpoint must not start charging
// the audio path for samples nobody asked for.
//
// The Go documentation's own examples use rate 1 for both, which records every
// blocking event and every contention event. That is the right choice for a
// microbenchmark and the wrong one for a long-running appliance doing real-time
// audio, which is why these constants exist rather than deferring to the
// runtime's idea of a starting point.
const (
	// DefaultBlockProfileRate samples one blocking event per 10 microseconds of
	// blocked time. Coarse enough to be cheap, fine enough that a contended
	// lock or a starved channel still shows up.
	DefaultBlockProfileRate = 10000

	// DefaultMutexProfileFraction reports 1 in 100 contention events. A real
	// contention problem produces far more than 100 events, so the shape of the
	// profile survives the sampling.
	DefaultMutexProfileFraction = 100
)

// ResolvedBlockRate returns the value to hand runtime.SetBlockProfileRate.
//
// Non-positive resolves to 0, which disables block profiling. The runtime
// already treats every value <= 0 as off, so the clamp changes no behaviour;
// it exists so the value that gets logged as "applied" is the value that took
// effect, rather than whatever the user typed.
func (p *ProfilingConfig) ResolvedBlockRate() int {
	if p == nil || p.BlockRate <= 0 {
		return 0
	}
	return p.BlockRate
}

// ResolvedMutexFraction returns the value to hand
// runtime.SetMutexProfileFraction.
//
// The clamp is load-bearing here, unlike the block-rate one. A negative
// argument tells SetMutexProfileFraction to report the current fraction and
// leave it unchanged, so passing a negative config value straight through would
// silently keep mutex profiling at whatever it already was, which is the
// opposite of what someone writing a negative number into a config file wants.
func (p *ProfilingConfig) ResolvedMutexFraction() int {
	if p == nil || p.MutexFraction <= 0 {
		return 0
	}
	return p.MutexFraction
}
