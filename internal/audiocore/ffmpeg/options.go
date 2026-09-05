package ffmpeg

import "time"

// Options carries manager-level defaults that apply to every stream a Manager
// starts, as opposed to the per-stream settings on StreamConfig. The zero value
// reproduces the manager's historic behaviour, so the plain NewManager
// constructor (which passes a zero Options) is unaffected.
//
// The only knob today is SilenceTimeout, which exists so characterization tests
// can drive the silence watchdog in seconds instead of the production 90 s
// without mutating the package-level silenceTimeout constant. Later phases fold
// the remaining per-manager settings (FFmpeg binary path, extra FFmpeg
// parameters, log level) into this struct as well.
type Options struct {
	// SilenceTimeout overrides the per-stream silence watchdog timeout for every
	// stream started through this manager, unless a stream's own
	// StreamConfig.SilenceTimeout is already set. Zero keeps the production
	// default (silenceTimeout, 90 s).
	//
	// NOTE: the silence-restart error message embeds this value ("stream stopped
	// producing data for N seconds"), and internal/errors/telemetry_integration.go
	// suppresses that error from Sentry by matching the literal 90-second text. A
	// future phase that wires a non-90s value into a PRODUCTION path must update
	// that suppression signature to a prefix match or keep the two in sync.
	SilenceTimeout time.Duration
}

// withManagerDefaults returns cfg with manager-level defaults applied where the
// per-stream config leaves them unset. It never mutates the caller's config:
// when a default has to be applied it returns a shallow copy, otherwise it
// returns cfg unchanged.
func (m *Manager) withManagerDefaults(cfg *StreamConfig) *StreamConfig {
	// Treat any non-positive per-stream value as "unset", matching
	// effectiveSilenceTimeout, so a negative config value cannot both suppress
	// the manager option here and fall back to the default there.
	if cfg.SilenceTimeout <= 0 && m.opts.SilenceTimeout > 0 {
		c := *cfg
		c.SilenceTimeout = m.opts.SilenceTimeout
		return &c
	}
	return cfg
}
