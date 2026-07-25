// Package profiling applies the Go runtime's block and mutex sampling rates
// from configuration.
//
// It is deliberately not part of internal/observability. That package is the
// Prometheus metrics side of the house and logs under the telemetry module,
// and keeping profiling out of it is the entire point of the diagnostics
// config section: enabling metrics must not enable profiling, and the two
// should not share a home that implies they do.
//
// The HTTP half of profiling lives in internal/api/pprof.go. This half runs
// with no HTTP involvement at all: sampling costs CPU whether or not anyone
// ever fetches a profile, so it is configured, and logged, separately.
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
// the user typed would be worse than the overhead. The warning exists because
// Go's own documentation offers rate 1 as the way to "include every blocking
// event", with no mention of what that costs a process doing real-time audio,
// and rate 1 is therefore the value people copy.
const (
	// aggressiveBlockRateNanos samples more often than once per microsecond of
	// blocked time.
	aggressiveBlockRateNanos = 1000

	// aggressiveMutexFraction reports more than one in ten contention events.
	aggressiveMutexFraction = 10
)

// GetLogger returns the package logger.
func GetLogger() logger.Logger {
	return logger.Global().Module("profiling")
}

// ApplyRates sets the runtime block and mutex profile sampling rates from
// settings and returns the values that took effect.
//
// Both rates used to be switched on at their most aggressive possible value by
// debug: true, which is a flag people turn on for verbose logging while chasing
// something unrelated, so the cost was paid by users who had not asked for a
// profile and had no way to know they were paying it. They are now explicit
// configuration, off unless set.
//
// Safe to call repeatedly and at any point in the process lifetime, which is
// what makes these settings hot-reloadable. One caveat worth knowing when
// reading a profile taken shortly after a change: lowering or zeroing a rate
// does not retroactively discard samples already collected, so such a profile
// can mix rates.
//
// The applied values are returned rather than only logged because the runtime
// offers no way to read the block profile rate back, so a test has no other
// way to assert what was applied.
func ApplyRates(settings *conf.Settings) (blockRate, mutexFraction int) {
	if settings == nil {
		return 0, 0
	}

	cfg := &settings.Diagnostics.Profiling
	blockRate = cfg.ResolvedBlockRate()
	mutexFraction = cfg.ResolvedMutexFraction()

	runtime.SetBlockProfileRate(blockRate)
	runtime.SetMutexProfileFraction(mutexFraction)

	log := GetLogger()
	if blockRate == 0 && mutexFraction == 0 {
		log.Debug("Runtime block and mutex profiling disabled")
		return blockRate, mutexFraction
	}

	log.Info("Runtime profiling rates applied",
		logger.Int("block_rate_ns", blockRate),
		logger.Int("mutex_fraction", mutexFraction))

	if blockRate > 0 && blockRate < aggressiveBlockRateNanos {
		log.Warn("Block profile rate records a large share of blocking events and costs CPU on the audio path; consider a coarser rate",
			logger.Int("block_rate_ns", blockRate),
			logger.Int("suggested_block_rate_ns", conf.DefaultBlockProfileRate))
	}
	if mutexFraction > 0 && mutexFraction < aggressiveMutexFraction {
		log.Warn("Mutex profile fraction records a large share of contention events and costs CPU on the audio path; consider a larger fraction",
			logger.Int("mutex_fraction", mutexFraction),
			logger.Int("suggested_mutex_fraction", conf.DefaultMutexProfileFraction))
	}

	return blockRate, mutexFraction
}
