// Package profiling applies the Go runtime's block and mutex sampling rates
// from configuration.
//
// The HTTP half of profiling lives in internal/api/pprof.go. This half runs
// with no HTTP involvement at all: sampling costs CPU whether or not anyone
// ever fetches a profile, which is why it is configured separately. The
// reasoning for keeping the whole feature out of the Prometheus telemetry
// settings is recorded once, on conf.DiagnosticsConfig, rather than restated
// here.
package profiling

import (
	"runtime"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// Thresholds below which a configured rate records so large a share of events
// that it is worth a line in the log.
//
// Neither is enforced. Someone chasing a deadlock on a development machine has
// a legitimate reason to record everything, and silently overriding a number
// the user typed would be worse than the overhead. The note exists because Go
// documents rate 1 for the block profiler as the way to "include every blocking
// event", with no mention of what that costs a process doing real-time audio,
// and rate 1 is therefore the value people copy.
const (
	// aggressiveBlockRateNanos samples more often than once per microsecond of
	// blocked time.
	aggressiveBlockRateNanos = 1000

	// aggressiveMutexFraction reports more than one in ten contention events.
	aggressiveMutexFraction = 10

	// coarseBlockProfileRate is the other end of the same advice. Above 100ms
	// per sample the profile records almost nothing, while the process still
	// pays the unconditional cputicks() read that any non-zero rate arms at
	// every channel and semaphore operation: measured on a Raspberry Pi 5, a
	// blocking channel round-trip costs +28% at rate 100000 against rate 0, so
	// most of the cost survives however coarse the rate goes. Zero is the only
	// free setting, and someone who set a very large number probably believed
	// otherwise.
	coarseBlockProfileRate = 100_000_000
)

// blockRateIsAggressive reports whether a configured block rate records so
// large a share of blocking events that it is worth telling the operator.
//
// Split out from ApplyRates purely so it is testable: the branch it guards
// produces only a log line, and inverting the comparison would otherwise be
// invisible to the whole suite.
func blockRateIsAggressive(rate int) bool {
	return rate > 0 && rate < aggressiveBlockRateNanos
}

// mutexFractionIsAggressive is the mutex-side counterpart to
// blockRateIsAggressive.
func mutexFractionIsAggressive(fraction int) bool {
	return fraction > 0 && fraction < aggressiveMutexFraction
}

// blockRateIsCoarse reports whether a configured block rate is so coarse that
// the operator is paying for sampling that records almost nothing.
func blockRateIsCoarse(rate int) bool {
	return rate > coarseBlockProfileRate
}

// GetLogger returns the package logger.
//
// Fetched from the global logger on each call rather than cached, so it always
// uses the current centralized logger, which may be installed after package
// init. Do not hoist this into a package-level var: that captures whatever is
// installed at init time, which at init is the fallback console logger.
func GetLogger() logger.Logger {
	return logger.Global().Module("profiling")
}

// ApplyRates sets the runtime block and mutex profile sampling rates.
//
// Both rates used to be switched on at their most aggressive possible value by
// debug: true, which is a flag people turn on for verbose logging while chasing
// something unrelated, so the cost was paid by users who had not asked for a
// profile and had no way to know they were paying it. They are now explicit
// configuration, off unless set.
//
// Safe to call repeatedly and at any point in the process lifetime, which is
// what makes these settings hot-reloadable: both setters are atomic stores, so
// there is no stop-the-world and no lock, and calling this from a request
// goroutine on a loaded system is fine. One caveat worth knowing when reading a
// profile taken shortly after a change: lowering or zeroing a rate does not
// retroactively discard samples already collected, so such a profile can mix
// rates.
//
// A nil config is a no-op rather than a reset, because disabling a profiler the
// operator asked for is not an improvement on leaving it alone. Note what that
// guard does NOT buy: every caller passes the address of a struct field, so a
// nil settings pointer upstream panics on the address-of before this function is
// entered. It covers a literal ApplyRates(nil) and nothing else.
func ApplyRates(cfg *conf.ProfilingConfig) {
	if cfg == nil {
		return
	}

	blockRate := cfg.ResolvedBlockRate()
	mutexFraction := cfg.ResolvedMutexFraction()

	runtime.SetBlockProfileRate(blockRate)
	runtime.SetMutexProfileFraction(mutexFraction)

	log := GetLogger()
	if blockRate == 0 && mutexFraction == 0 {
		// Serving /debug/pprof with no sampling configured is a supported
		// combination, not a mistake: heap, goroutine, CPU and trace all work.
		// But /debug/pprof/block and /debug/pprof/mutex then answer 200 with an
		// empty profile, which reads as "nothing is contending" rather than
		// "nothing was recorded". That false negative is worth one line at a
		// level the operator will actually see, since they just asked for
		// profiling.
		//
		// In practice this is a startup line. The settings-API path reaches
		// ApplyRates only when profilingRatesChanged fires, and that compares
		// the two rates and not Enabled, so switching the endpoint on at runtime
		// with both rates at 0 does not produce it. That is the right trade:
		// widening the gate to Enabled would reapply sampling every time
		// somebody toggled the endpoint, and with no UI for this section the
		// realistic path is editing config.yaml and restarting anyway.
		if cfg.Enabled {
			log.Info("Profiling endpoints are enabled but block and mutex sampling are off; those two profiles will be empty until blockrate or mutexfraction is set",
				logger.Int("suggested_block_rate_ns", conf.RecommendedBlockProfileRate),
				logger.Int("suggested_mutex_fraction", conf.RecommendedMutexProfileFraction))
			return
		}
		log.Debug("Runtime block and mutex profiling disabled")
		return
	}

	log.Info("Runtime profiling rates applied",
		logger.Int("block_rate_ns", blockRate),
		logger.Int("mutex_fraction", mutexFraction))

	// Info, not Warn. Every Warn in this process is captured by the health
	// error-buffer handler installed in main and listed back to the user as a
	// recent error by the diagnostics API, which counts entries with no level
	// filter. A rate the operator typed on purpose is not an error, and
	// reporting it as one would push a deliberate debugging session toward the
	// degraded-health thresholds.
	if blockRateIsAggressive(blockRate) {
		log.Info("Block profile rate records a large share of blocking events and costs CPU on the audio path; consider a coarser rate",
			logger.Int("block_rate_ns", blockRate),
			logger.Int("suggested_block_rate_ns", conf.RecommendedBlockProfileRate))
	}
	if blockRateIsCoarse(blockRate) {
		log.Info("Block profile rate is coarse enough to record almost nothing, but sampling still costs CPU on the audio path; set blockrate to 0 to stop paying for it",
			logger.Int("block_rate_ns", blockRate),
			logger.Int("suggested_block_rate_ns", conf.RecommendedBlockProfileRate))
	}
	if mutexFractionIsAggressive(mutexFraction) {
		log.Info("Mutex profile fraction records a large share of contention events and costs CPU on the audio path; consider a larger fraction",
			logger.Int("mutex_fraction", mutexFraction),
			logger.Int("suggested_mutex_fraction", conf.RecommendedMutexProfileFraction))
	}
}
